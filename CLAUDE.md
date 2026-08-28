# CLAUDE.md — extctl

## What this is

`extctl` is a local CLI pipeline that automates daily oCIS Web extension
candidate generation for ownCloud. Every workday it:

1. **Phase A (morning):** generates 3 agentic extension specs via Claude Code
   headless, creates Jira issues for human review.
2. **Phase B (event-driven):** polls Jira for picks, runs a human-in-the-loop
   planning phase (plan → plan review → stages → stage review), then builds
   all chosen extensions stage by stage into reviewable GitHub PRs using
   Claude Code headless + a per-stage validation gate.

The manager can pick one or more candidates (by moving them to "Doing" in
Jira); all picked candidates are built concurrently. Phase A and Phase B are
both implemented.

## Repo layout

```
cmd/extctl/main.go          # CLI entrypoint (cobra)
internal/
  config/config.go          # extctl.yaml loader + env var helpers
  state/state.go            # slate.json read/write, carryover/delivered logic
  claude/
    run.go                  # shells out to `claude -p` headless, parses JSON
    parse.go                # strict ## CANDIDATE block parser
  jira/
    client.go               # Jira Cloud REST v2 client (Basic auth)
    format.go               # issue body + summary formatters
  gen/gen.go                # Phase A orchestrator (the core of `extctl gen`)
  build/
    plan.go                 # Plan() – planning phase orchestration
    stages.go               # DeriveStages/ParseStages/CheckStage/AppendDocStage
    stage.go                # BuildStage() – per-stage build orchestration
prompts/
  gen-specs.md              # Phase A prompt (read-only, grounded in web-extensions)
  plan-extension.md         # planning prompt (Read/Grep/Glob/Write)
  derive-stages.md          # stage derivation prompt (Read/Write)
  build-stage.md            # per-stage build prompt
idea-pool.yaml              # seed ideas for the spec generator
extctl.example.yaml         # config template (copy to extctl.yaml, never commit)
```

## Key facts

- **Module:** `github.com/LukasHirt/extctl`
- **Go version:** 1.22+
- **Dependencies:** `github.com/spf13/cobra`, `gopkg.in/yaml.v3` — nothing else
- **License:** Apache 2.0, copyright Lukáš Hirt (personal, not LemonITech)
- **DCO:** all commits must be signed off (`git commit -s`)
- **Jira:** Cloud (*.atlassian.net), Basic auth via `EXTCTL_JIRA_EMAIL` +
  `EXTCTL_JIRA_TOKEN` env vars. Never Bearer, never hardcoded.
- **Config file:** `extctl.yaml` in the working directory (gitignored).
  `extctl.example.yaml` is the committed template.
- **Target repo checkout:** `target_repo.checkout` is a fixed local path
  extctl owns exclusively — never a checkout a developer works in by hand.
  Every command that touches it (`gen`, `poll`, `gate`, `approve-plan`,
  `approve-stages`, `release`) calls `git.EnsureCheckout` from
  `PersistentPreRunE` in `cmd/extctl/main.go` first: clones it via
  `gh repo clone` if the path has no `.git` yet, otherwise fetches and
  hard-resets it onto `origin/<default_branch>`. Because extctl is the only
  writer, a hard reset there is always safe. Defaults to `./.extctl-checkout`
  if unset. Must be a real (non-bare) working tree, not just a `.git` object
  store — the gate's e2e stage runs `docker compose` directly inside it.

## What's already working

- `extctl gen` — full Phase A: loads state, builds prompt with carryover +
  delivered dedup context, runs `claude -p` headless (Read/Grep/Glob only),
  parses 3 `## CANDIDATE` blocks, then enters an **interactive review loop**
  before creating any Jira issues. Each candidate's spec is immediately written
  to `runs/<date>/review-<id>.md` — edit it in any editor before deciding.
  Options: `a` approve (re-reads the file to pick up edits), `d` discard with a
  reason, `s` show full spec. Discarded candidates are persisted in the slate with
  `status: rejected` and their reason (so they never reappear in the dedup
  guard), and replacement candidates are generated automatically until all target
  slots are filled. Only approved candidates become Jira issues. Writes
  `runs/<date>/slate.json` with `review_done: true` on completion.
- `extctl gen --no-review` — skips the interactive review and pushes all
  generated candidates directly to Jira (use for scheduled/automated runs).
- `extctl gen --dry-run` — shows carryovers, delivered IDs, and the full
  prompt without calling Claude or touching Jira.
- `extctl gen --skip-jira` — runs Claude, prints parsed candidates, skips
  Jira, review, and slate write. Use this to validate generation quality.
- `extctl gen --model <model>` — override Claude model.
- `extctl slate status` — shows latest slate.
- `extctl slate carryovers [--format=dedup-hint]` — lists live carryovers.
- `extctl version` — prints version.
- `extctl poll` — polls Jira for picks; on a pick, fetches Jira issue comments
  and stores them as `issue_comments` in `slate.json`, creates a worktree, runs
  Claude with `plan-extension.md` to write `runs/<date>/<id>/plan.md`, and
  sets the candidate state to `plan_review`. Issue comments are passed as
  `{{ISSUE_COMMENTS}}` to the plan, derive-stages, and build-stage prompts.
- `extctl poll --dry-run` — shows candidates in each build state without
  triggering any Claude invocations or Jira transitions.
- `extctl approve-plan <id>` — reads the approved `plan.md`, runs Claude with
  `derive-stages.md` to write `runs/<date>/<id>/stages.md`, sets state to
  `stages_review`.
- `extctl approve-stages <id>` — reads the approved `stages.md` and builds
  each stage in sequence. Before the first Claude invocation, extctl
  automatically copies `scaffold/` into `packages/web-app-<id>/` in the
  worktree and adds registration entries to `docker-compose.yml`,
  `dev/docker/ocis.apps.yaml`, and `support/actions/ocis.apps.yaml` — no
  user action required. Claude then runs `build-stage.md` per stage, the gate
  runs after each stage, and on full pass the branch is pushed and a GitHub PR
  is opened. State progresses through `building` → `gated` → `rebasing`
  (only when needed) → `publishing` → `done`.

  Once the gate passes, and before pushing, extctl rebases the build's branch
  onto the current tip of `origin/<default_branch>` (a build can span gate
  retries and human review pauses, so the base may have moved on). On a
  conflict, a scoped Claude invocation (`rebase-repair.md`) resolves it — see
  `internal/poll/poll.go`'s `rebaseOntoDefault`. It can edit conflicted files,
  stage them, and run `git rebase --continue`, but never `git rebase --abort`
  — only the orchestrator aborts, so it can always tell a finished rebase
  apart from one Claude gave up on. The gate is re-run once against the
  rebased tree before push. Exhausting `claude.max_rebase_attempts` (default
  2), or a gate failure after rebasing, falls back to the same blocked-draft-PR
  path as gate-repair exhaustion.

  The gate (`gate/run-gate.sh`) runs five stages: hygiene, build, lint, unit,
  and e2e. The e2e stage is an **orchestrator** action (not part of the
  sandboxed build-stage Claude invocation): it copies the built extension's
  `dist/` into the running oCIS container, restarts it, and runs the
  extension's Playwright acceptance tests. Skipped when no web-extensions
  checkout is passed; serialized across concurrently-built candidates via a
  lock so Playwright sessions don't collide on the shared admin user.

  Before every gate invocation, `gate.Run` (`internal/gate/clean.go`) runs
  `git status --porcelain` and deletes/discards anything outside
  `packages/web-app-<id>/` and an allowlist of shared root files — the
  sandboxed build/repair Claude invocation has no way to clean up a stray
  artifact it wrote outside its own package directory, so the orchestrator
  does it instead.
- `extctl release` — scans the web-extensions checkout for extensions merged
  to the default branch but never released, and creates + pushes a signed
  git tag `<app-id>-v<version>` (version from `package.json`) for each. The
  web-extensions GitHub Action picks up the tag and builds the release —
  extctl only pushes the tag. Idempotent (an extension counts as released once
  any `<app-id>-v*` tag exists). `--dry-run` lists without creating/pushing.
  Signed tags require `user.signingkey` in the target repo's git config.
  Logic in `internal/release/release.go`; git primitives in
  `internal/git/tags.go`.
- `extctl publish [--id <app-id>] [--dry-run]` — scans web-extensions GitHub
  Releases for extensions not yet in `owncloud/marketplace` and **stages** a
  submission per extension (downloads, resolves metadata, captures
  screenshots, commits locally) without pushing or opening a PR — that's the
  separate, explicit `approve` step, so a submission always gets reviewed
  before it goes public and a bad screenshot capture can be retried without
  redoing everything else. Three commands:
  - `extctl publish [--id <app-id>] [--dry-run]` — the scan+stage step (see
    below).
  - `extctl publish approve <app-id>[@<version>]` — pushes the staged
    `publish/<app-id>-v<version>` branch and opens its PR. Bare `<app-id>`
    works if exactly one version is pending; disambiguate with `@<version>`
    otherwise. Idempotent — returns the existing PR's URL if one's already
    open on that branch instead of duplicating it.
  - `extctl publish retry-screenshots <app-id>[@<version>]` — checks out the
    staged branch, generates a fresh screenshot spec and recaptures, reusing
    the already-downloaded bundle and already-resolved tags/minOCIS/license
    off the checked-out `extension.yaml`. New screenshots/captions are
    amended onto the existing commit (`git commit --amend`), so `approve`
    still ever pushes exactly one commit per submission.
  - `extctl publish verify-minocis <app-id>[@<version>]` — re-runs, on an
    already-staged submission, the same e2e minOCIS verification the main
    staging step already runs automatically (see below) — for when it was
    skipped at staging time (`--skip-minocis-verify`, or Docker wasn't
    available then), or the extension's own e2e tests changed since.
    Updates `extension.yaml` and amends it onto the existing commit
    (`AmendSubmissionMinOCIS`) only if the verified value differs from what
    was staged; no-op otherwise. Must run before `approve` — `approve` is
    idempotent (returns the existing PR's URL instead of re-pushing once
    one is open), so an amend after that point never reaches a live PR.

  **minOCIS e2e verification** (automatic during staging, by default):
  root cause addressed: `ResolveMinOCIS`'s "reuse from the previous
  release" tier was originally documented as reusing an "already-approved"
  value, but nothing ever re-approved it — it just carried forward release
  after release, unexamined, which is exactly `owncloud/marketplace#240`
  (minOCIS constant across an extension's entire version range regardless
  of what a newer release actually needs). `stageOne` now runs the
  extension's own e2e Playwright suite against real `owncloud/ocis` Docker
  images (`verifyMinOCISDuringStaging`/`verifyMinOCISBundle`,
  `internal/marketplace/min_ocis_verify.go`) before minOCIS is ever
  committed, bisecting (`BisectMinOCIS`, `min_ocis_bisect.go`) over every
  stable "X.Y.Z" tag Docker Hub actually has (`AvailableOCISImageVersions`,
  `ocis_versions.go` — Docker Hub is missing an entire major series, oCIS
  6.x, confirmed to have zero images of any kind; the candidate list is
  fetched live rather than hardcoded, so any such gap is just skipped over,
  never a hard failure) to find the lowest version the tests actually pass
  against. The search seeds from `ResolveMinOCIS`'s heuristic guess and
  checks that ONE version first — bisection only runs (and only searches
  ABOVE the seed, never below) if that no longer passes — so the common
  case (floor hasn't moved) costs one Docker bring-up/teardown cycle, not
  several; a later version of the same extension staged in the same batch
  seeds from an earlier sibling's just-verified value
  (`runMetadataCache.recordMinOCIS`), not the original unverified guess.
  Reuses `pinExtensionSourceToRelease`/`PreparePlaywrightRun` from the
  screenshot-capture path to get the exact released source+bundle in
  place, and `freshOCISUpWithImage` (a `freshOCISUp` variant taking an
  `OCIS_IMAGE` override — `docker-compose.yml`'s `ocis` service already
  reads that env var) to bring up each candidate version. Serialized via
  the same `withE2ELock` the gate's e2e stage and screenshot capture use.

  Best-effort, same philosophy as screenshot capture: a Docker/infra
  failure, or the extension failing its e2e tests against every available
  oCIS image, is logged and staging falls back to the unverified heuristic
  value rather than aborting the submission — requiring Docker to succeed
  would make an otherwise fast, reliable command newly fragile. Opt out
  entirely with `extctl publish --skip-minocis-verify` (e.g. no Docker in
  this environment, or a quick dry run) and run `verify-minocis` on the
  staged branch later instead.

  **Staging step:** downloads the release's zip asset via `gh release
  download` and uses it directly as `bundle.zip` (already byte-identical to
  what marketplace needs). `license`/`subtitle` come from that extension's
  own `package.json` — no `license` field means the extension fails to stage
  rather than getting a guessed value. `tags`/`minOCIS`
  (`marketplace.ResolveTags`/`ResolveMinOCIS`) reuse the extension's most
  recent prior marketplace release verbatim if one exists; otherwise tags
  fall back to Claude inference (`infer-tags.md`) or are left unset (no
  placeholder), and minOCIS — never guessed by Claude, since it's a hard
  compatibility claim — falls back to `InferMinOCISFromHistory` (highest
  stable `owncloud/ocis` release before the extension's first commit date) or
  is left unset. That value is then, by default, automatically confirmed or
  corrected by actually running the extension's e2e tests against real oCIS
  images before it's ever committed — see "minOCIS e2e verification" below.
  None of this inferred/heuristic context goes into the PR body —
  `printReviewNotes` prints it to the terminal at staging time, since a
  human reviews it before `approve` ever runs.

  Screenshots come from a fresh, dedicated `tests/e2e/marketplace-
  screenshots.spec.ts` Claude writes per extension (`marketplace-
  screenshots.md`, see `GenerateScreenshotSpec`) — never `acceptance.spec.ts`,
  which optimizes for functional assertions, not for looking good publicly.
  The spec is never committed (written into `target_repo.checkout`, removed
  after the run; a copy is kept alongside `playwright.log`/`e2e-report.json`
  in the review dir if capture comes up with zero screenshots).

  Before `GenerateScreenshotSpec` runs, `pinExtensionSourceToRelease`
  (`pin_release.go`) switches `packages/web-app-<appID>` in
  `target_repo.checkout` to the exact tree it had at the release tag being
  published (`git checkout <tag> -- <path>`, restored back to `HEAD` once
  capture is done) — scoped to that one path, nothing else in the checkout
  moves. Without this, Claude reads whatever the default branch currently
  looks like to pick selectors, while `PreparePlaywrightRun` serves the
  OLD release's built `dist/` to Playwright — for any release the source has
  since moved past (a renamed class, a component swapped for a
  design-system one), every selector Claude writes targets markup that
  release never had, and no number of fix-and-rerun cycles (inner OR outer,
  see `captureScreenshotsWithRetry` below) can converge, since the bug is
  the version skew itself. Best-effort like the rest of this path: a fetch
  or checkout failure is logged and capture proceeds anyway rather than
  aborting the submission.

  `prepareOCISForCapture` brings up a fresh oCIS stack (full `docker compose
  down`+`up -d`, not `restart` — see `freshOCISUp`'s doc comment) with the
  released `dist/` staged in, installs dependencies (`pnpm install
  --frozen-lockfile` — `node_modules` is never committed and doesn't exist
  on a fresh checkout, same reason `gate/run-gate.sh` does this before its
  own e2e stage), clears stale auth state (`clearStaleAuthCache`), and
  writes the force-screenshot config — all BEFORE Claude is invoked, so
  `GenerateScreenshotSpec`'s Claude session has a live oCIS to test against.
  That session is granted scoped Bash access
  (`Bash(pnpm playwright test *)`, restricted to one project via
  `publish.screenshot_project`/`publish.screenshot_project_overrides` rather
  than the full browser matrix) specifically so it can run the spec it just
  wrote and fix real failures (selector mismatches, mock/assertion drift)
  before returning, instead of writing blind and hoping — see
  `prompts/marketplace-screenshots.md`'s "Run it yourself and fix real
  failures" section, which caps Claude's own fix-and-rerun cycles at ~3 so a
  stubborn single assertion can't loop indefinitely. `playwrightCaptureEnv`
  sets `BASE_URL_OCIS`/`OCIS_URL` for both Claude's own Bash calls and the
  orchestrator's fallback run — some extensions ship their own
  `global-setup.ts` that falls back to an unrelated external URL when
  neither is set (see `ocis.go`). If Claude's session never leaves a report
  behind at all (wrote the spec but never ran it), `runPlaywrightDirect`
  runs it once directly as a fallback so the attempt still yields something
  to collect. Best-effort throughout: a submission still gets staged without
  screenshots if spec generation or capture fails.

  `captureScreenshotsWithRetry` layers an OUTER retry on top of Claude's own
  inner one: up to `maxFreshSpecAttempts` (3) full attempts — fresh oCIS
  bring-up, fresh Claude session, fresh spec each time, never re-running a
  previous attempt's spec — stopping as soon as one attempt has zero test
  failures (`gate.AllTestsPassed`) AND `CollectScreenshots` dropped nothing
  (`len(warnings) == 0`; see `isVisuallyDegenerate` below — a passed test
  whose screenshot got dropped is treated the same as a failed test, so it
  actually triggers a retry instead of silently shipping fewer screenshots
  than asked for). If every attempt runs out without a full pass, the last
  attempt's (partial, or empty) result is used — not necessarily the best
  one seen across attempts, since `CollectScreenshots` clears its
  destination each call. Each outer attempt costs a full Claude call
  (itself possibly several internal test-and-fix cycles) plus an oCIS
  teardown/bring-up cycle (~2-4+ min), so this meaningfully multiplies
  capture time/cost — a deliberate tradeoff for reliability over a
  single-shot attempt.

  `validateScreenshot` (`screenshots.go`) also rejects a screenshot whose
  content is almost entirely a single flat color
  (`isVisuallyDegenerate`) — a test can pass its own assertions (e.g. "a
  marker element exists") while the screenshot itself shows nothing real
  (blank grey map tiles that hadn't finished painting; a placeholder image
  stretched to fill the frame). This is a genuinely coarse safety net, not
  a general solution — empirically tested against a real captured "blank
  map" screenshot this session, it did NOT reliably distinguish it from a
  legitimately fine screenshot (both scored similarly on whole-image color
  diversity/dominant-color-fraction/edge-density; surrounding UI chrome —
  headers, buttons, text — provides just enough incidental variety to mask
  a blank CONTENT region specifically). It only reliably catches the
  extreme case: the ENTIRE frame is one near-flat color (e.g. the original
  1x1-pixel-photo bug, which affected the whole viewport, not one region).
  The map-tiles case is instead addressed at the prompt level —
  `marketplace-screenshots.md`'s "locator present ≠ visually rendered" rule
  — since a targeted "wait for this specific async content to actually
  paint" instruction is more precise than a generic pixel heuristic can be
  for a region-specific blank state.

  `freshOCISUp` also runs `ensureExternalSitesManifest` before `docker
  compose up` — a Docker Desktop/virtiofs bug where stacking a file-level
  bind mount on top of a directory mount fails if the underlying file doesn't
  exist yet, which it never does on a fresh `target_repo.checkout` (build
  output isn't committed). `gate/run-gate.sh`'s e2e stage brings up the same
  stack from the same checkout and could plausibly hit the same bug — not yet
  fixed there since it hasn't been observed.

  `clearStaleAuthCache` deletes `<ext>/tests/e2e/.auth/` before every
  capture run. Some extensions (confirmed: photo-addon) cache Playwright
  `storageState` there keyed only on file existence, no TTL — harmless in a
  normal long-lived dev oCIS, but `freshOCISUp` just tore down and recreated
  oCIS, which regenerates its IDP signing key, so a cached token immediately
  fails with `token signature is invalid`. Surfaces to Playwright as a stuck
  OIDC login redirect loop and a 30s timeout — indistinguishable from a
  genuine oCIS-readiness flake without reading the container's own logs.
  `CollectScreenshots`/`gate.AllScreenshots` only accept `passed` test
  results, specifically so a failure like this can never silently surface a
  login-page screenshot as if it were a real capture — Playwright's
  `screenshot: 'on'` attaches a screenshot to every result regardless of
  outcome, so a naive "grab whatever's attached" reading previously reported
  a fully-failed run as a successful capture. `CollectScreenshots` also
  clears its destDir (`<review dir>/screenshots/`) unconditionally before
  writing, even when this run captures zero shots — otherwise a retry that
  captures fewer screenshots than a previous attempt (e.g. 1 this time vs. 3
  before) leaves the earlier run's leftover files sitting alongside the new
  one, silently misrepresenting what the current run actually produced.

  **Review dir:** every staged extension gets `runs/publish/<app-id>-
  <version>/` (durable, not a temp dir — must survive until a human reviews
  it, possibly much later) holding the bundle, Playwright log/report, and any
  captured screenshots. The canonical state, though, is the git commit itself
  — `bundle.zip`, `extension.yaml`, `screenshots/` committed on
  `publish/<app-id>-v<version>` in `marketplace_repo.checkout`, which is what
  `approve`/`retry-screenshots` actually act on.

  Multiple unpublished releases of one extension (e.g. v0.1.0–v0.3.0 all
  tagged before publish ever ran) all get staged in the same run, oldest
  first. `Run`'s per-app-id metadata cache makes tags/minOCIS consistent
  across that batch — the first version to resolve them is what every later
  version in the same run reuses, since a staged-but-unapproved sibling
  branch isn't visible to `PreviousRelease` (it only sees merged history).
  Logic lives in `internal/marketplace/`.
- `extctl stats [--days=N]` — three-section dashboard (default: last 30 days).
  **TODAY**: today's slate breakdown (total candidates, fresh vs carryover,
  per-status counts, in-flight builds with phase/stage/cost).
  **PIPELINE**: pick rate, build success rate (done+gated / picked), avg repair
  attempts, avg Claude turns — all scoped to the requested date window.
  **COST**: total spend, avg and highest per-build cost, budget utilization
  (`BudgetUSDPerBuild × builds` in window). Reads `runs/*/slate.json` via
  `state.LoadAll` and `runs/*/*/state.json` via `build.LoadAllStates`.
- `extctl doctor` — read-only health check of the local installation: parses
  `extctl.yaml` and flags unknown/unsupported keys (derived via reflection
  over `config.Config`'s yaml tags, so it can't drift out of sync with the
  struct), flags required-but-empty fields (`jira.base_url`, `jira.project`,
  `target_repo.remote`), confirms `EXTCTL_JIRA_EMAIL`/`EXTCTL_JIRA_TOKEN` are
  set, confirms `git`/`gh`/`claude`/`docker`/`pnpm` (and `ffmpeg` if
  `media.enabled`) are on PATH, confirms referenced paths (prompt files,
  `idea_pool`, `scaffold_dir`) exist, and reports the shape of
  `target_repo.checkout`. Makes no network calls and never mutates anything
  (does not call `git.EnsureCheckout`) — safe to run any time, including with
  a missing or broken `extctl.yaml`. Exits non-zero if any error-level finding
  exists. Logic lives in `internal/doctor/`.

## What's next (in priority order)

### 1. Housekeeping (do this first)
- Add `.gitignore`: ignore `runs/*/`, `extctl.yaml` (keep
  `extctl.example.yaml`)
- Add `runs/delivered.yaml` support to `state.DeliveredIDs()` — a manually
  maintained list of extension IDs that predate extctl (built before the
  pipeline existed). Format:
  ```yaml
  - id: web-app-ai-doc-summary
    title: AI Document Summarizer Sidebar
  - id: web-app-chat-with-file
    title: Chat with File
  ```
  `LoadAll()` or a separate `LoadDelivered()` function should read this file
  and merge its IDs into the dedup guard.

### 2. Scheduling
- macOS: launchd plist, Mon–Fri 06:30 → `extctl gen`, business hours every
  10 min → `extctl poll`, login hook → `extctl reconcile`
- Linux: systemd user timers (same schedule)
- `extctl reconcile` — idempotent catch-up: runs gen if today's slate is
  missing, runs poll pass if any candidate is in "Doing" or `plan_review` /
  `stages_review` state awaiting human action

## Conventions

**Error handling:** wrap with `fmt.Errorf("context: %w", err)`. No panics
outside of `main()` init.

**State writes:** always write to a temp file then `os.Rename()` — see
`state.Save()` for the pattern. Never partial writes.

**Claude invocations:** always scoped tools, never open-ended Bash. Tool
allowlists by prompt:

- Phase A (`gen-specs.md`): `Read,Grep,Glob`
- Planning (`plan-extension.md`): `Read,Grep,Glob,Write`
- Stage derivation (`derive-stages.md`): `Read,Write`
- Per-stage build (`build-stage.md`) and repair (`repair.md`, same allowlist):
  `Read,Edit,Write,Grep,Glob,Bash(pnpm install *),Bash(pnpm build),
  Bash(pnpm test:unit *),Bash(pnpm lint *),Bash(pnpm check:types),
  Bash(git add *),Bash(git commit *),Bash(git status),Bash(git diff *),
  Bash(git rm -f *)`
- Rebase-conflict repair (`rebase-repair.md`) — narrower than build/repair:
  `Read,Grep,Glob,Edit,Bash(git status),Bash(git diff *),Bash(git add *),
  Bash(git rebase --continue),Bash(pnpm install *)`. No `git commit` (rebase
  carries the original commits forward, nothing new to commit) and no
  `git rebase --abort` — only the orchestrator aborts, so it can always tell a
  finished rebase apart from one Claude gave up on mid-conflict.

  Package scripts: each `packages/web-app-*` only defines `build`, `build:w`,
  `check:types`, `test:unit`, `test:e2e` — there is no per-package `test` or
  `lint` script. `lint` is a workspace-root-only script (globs `packages/**`
  and `support/**`); it must always run from the repo root, never `cd`'d into
  a package directory.
- Marketplace tag inference (`infer-tags.md`), used by `extctl publish` only
  when an extension has no prior marketplace release to reuse tags from:
  `Read,Grep,Glob`. Read-only — it classifies an existing extension, never
  edits anything.
- Marketplace screenshot spec generation (`marketplace-screenshots.md`),
  used by every `extctl publish` run: `Read,Grep,Glob,Write,Edit,Bash(pnpm
  playwright test *)`. Write/Edit are scoped by instruction to exactly one
  new file (`tests/e2e/marketplace-screenshots.spec.ts`, never committed) —
  Claude reads the extension's existing source/acceptance spec for context
  but must not touch either. Edit is granted alongside Write (not Write
  alone) because Claude routinely needs to revise the file it just wrote
  (e.g. a selector fix after re-reading a source component) — observed
  failing with only Write granted: Claude wrote the spec correctly, then
  tried `Edit` on it, got denied, and gave up mid-task instead of falling
  back to a full rewrite via `Write`. Bash is the one deliberate exception
  to "Claude never runs Playwright itself" below — scoped to exactly that
  one command (not a bare `Bash` grant) so this session can run the spec it
  wrote against the already-brought-up oCIS instance and fix real failures
  before returning, rather than writing blind; see `internal/marketplace`'s
  section above for the full flow.

No `git push`, no `gh`, no network tools — those are always orchestrator
actions. The same applies to the gate's e2e stage: Docker and Playwright
execution are orchestrator actions in `gate/run-gate.sh`, never granted to the
build-stage Claude invocation — Claude only writes the Playwright spec files
there; the gate runs them, and a failure there feeds a SEPARATE repair
invocation (`repair.md`) with its own retry cycle. `extctl publish`'s
screenshot capture is the one exception (above): it has no equivalent
separate gate+repair pipeline downstream, so a content bug in the spec has
nowhere else to get caught and fixed — granting the writing session itself
scoped, read-only-in-effect Playwright-execution access is what closes that
gap instead.

**Prompt anchoring:** `claude.Run` (`internal/claude/run.go`) appends a fixed
trailing `TASK: Follow the instructions above now.` directive to every prompt
before it hits the subprocess's stdin (see `anchorPrompt`'s doc comment for
why — a `claude` CLI quirk where long, structured prompts can otherwise get
misread as reference context instead of a request).

**Issue comments:** `{{ISSUE_COMMENTS}}` is substituted into the planning,
derive-stages, and build-stage prompts. It contains all Jira comments in
chronological order, formatted with author and timestamp. Later comments
(including replies) take precedence over earlier ones on the same point and
are treated as binding constraints that override the original spec.

**Jira transitions:** always look up the transition ID by name at runtime
(see `client.Transition()`) — never hardcode transition IDs, they vary per
instance.

**Secrets:** `EXTCTL_JIRA_EMAIL` and `EXTCTL_JIRA_TOKEN` only. Never in
config files, never logged, never passed to the Claude subprocess.

## Running locally

```bash
cp extctl.example.yaml extctl.yaml
# edit extctl.yaml: base_url, project key, target_repo.checkout path

export EXTCTL_JIRA_EMAIL="your@email.com"
export EXTCTL_JIRA_TOKEN="your-api-token"

go build ./cmd/extctl

./extctl gen --dry-run       # verify prompt + context
./extctl gen --skip-jira     # verify candidate quality
./extctl gen                 # full run
```

The working directory for `extctl gen` must be the `extctl` folder
(where `extctl.yaml`, `prompts/`, and `idea-pool.yaml` live). The
`target_repo.checkout` path in config points to the separate
`web-extensions` checkout where Claude Code actually runs.
