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
  is opened. State progresses through `building` → `gated` → `publishing` →
  `done`.

  The gate (`gate/run-gate.sh`) runs five stages: hygiene, build, lint, unit,
  and e2e. The e2e stage is an **orchestrator** action (not part of the
  sandboxed build-stage Claude invocation): it copies the built extension's
  `dist/` into the running oCIS container (auto-discovered from `/web/apps/`),
  restarts the container so oCIS picks up the new app, and runs the extension's
  Playwright acceptance tests. It is skipped when no web-extensions checkout is
  passed (the gate's optional 5th argument), and is serialized across
  concurrently-built candidates via a lock so their Playwright sessions don't
  collide on the shared admin user.

  Before every gate invocation (initial and each repair retry), `gate.Run` in
  `internal/gate/clean.go` runs `git status --porcelain` and, for anything
  outside `packages/web-app-<id>/` and the allowlisted root files
  (`pnpm-lock.yaml`, `docker-compose.yml`, `dev/docker/ocis.apps.yaml`,
  `support/actions/ocis.apps.yaml`), deletes untracked stray files/dirs and
  discards uncommitted edits to tracked ones. This is an orchestrator action —
  the sandboxed build/repair Claude invocation has no way to clean up an
  artifact it accidentally writes outside its own package directory, so
  leaving that to the orchestrator prevents both a permanently-failing
  hygiene stage and a repair session working around it by editing shared repo
  config (e.g. root `.gitignore`) instead.
- `extctl release` — scans the web-extensions checkout for extensions that have
  been merged to the default branch (`packages/web-app-*` present on
  `origin/<default_branch>`) but never released, and creates + pushes a signed
  git tag `<app-id>-v<version>` (version read from each `package.json`) for each.
  The GitHub Action in web-extensions picks up the pushed tag and builds the
  release — extctl only pushes the tag. An extension counts as released once any
  tag with prefix `<app-id>-v` exists, so the command is idempotent. Only the
  newly created tags are pushed. `extctl release --dry-run` lists what would be
  tagged without creating or pushing anything. Signed tags require a signing key
  in the target repo's git config (`user.signingkey`). Logic lives in
  `internal/release/release.go`; git primitives in `internal/git/tags.go`.
- `extctl stats [--days=N]` — three-section dashboard (default: last 30 days).
  **TODAY**: today's slate breakdown (total candidates, fresh vs carryover,
  per-status counts, in-flight builds with phase/stage/cost).
  **PIPELINE**: pick rate, build success rate (done+gated / picked), avg repair
  attempts, avg Claude turns — all scoped to the requested date window.
  **COST**: total spend, avg and highest per-build cost, budget utilization
  (`BudgetUSDPerBuild × builds` in window). Reads `runs/*/slate.json` via
  `state.LoadAll` and `runs/*/*/state.json` via `build.LoadAllStates`.

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

  Package scripts: each `packages/web-app-*` only defines `build`, `build:w`,
  `check:types`, `test:unit`, `test:e2e` — there is no per-package `test` or
  `lint` script. `lint` is a workspace-root-only script (globs `packages/**`
  and `support/**`); it must always run from the repo root, never `cd`'d into
  a package directory.

No `git push`, no `gh`, no network tools — those are always orchestrator
actions. The same applies to the gate's e2e stage: Docker and Playwright
execution are orchestrator actions in `gate/run-gate.sh`, never granted to the
build-stage Claude invocation. Claude only writes the Playwright spec files; the
gate runs them.

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
