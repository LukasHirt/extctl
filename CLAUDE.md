# CLAUDE.md — extctl

## What this is

`extctl` is a local CLI pipeline that automates daily oCIS Web extension
candidate generation for ownCloud. Every workday it:

1. **Phase A (morning):** generates 3 agentic extension specs via Claude Code
   headless, creates Jira issues for human review.
2. **Phase B (event-driven):** polls Jira for a pick, builds the chosen
   extension into a reviewable GitHub PR using Claude Code headless + a
   validation gate.

The contractual unit is **1 delivered extension per workday**, chosen by the
ownCloud manager from 3 fresh candidates (plus any carryovers). Only Phase A
is currently implemented. Phase B is next.

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
prompts/
  gen-specs.md              # Phase A prompt (read-only, grounded in web-extensions)
  build-extension.md        # Phase B prompt (builds the picked candidate)
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

## What's already working

- `extctl gen` — full Phase A: loads state, builds prompt with carryover +
  delivered dedup context, runs `claude -p` headless (Read/Grep/Glob only),
  parses 3 `## CANDIDATE` blocks, creates Jira issues, writes
  `runs/<date>/slate.json`.
- `extctl gen --dry-run` — shows carryovers, delivered IDs, and the full
  prompt without calling Claude or touching Jira.
- `extctl gen --skip-jira` — runs Claude, prints parsed candidates, skips
  Jira and slate write. Use this to validate generation quality.
- `extctl gen --model <model>` — override Claude model.
- `extctl slate status` — shows latest slate.
- `extctl slate carryovers [--format=dedup-hint]` — lists live carryovers.
- `extctl version` — prints version.

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
- Add `StatusRejected` to the candidate status enum in `state.go` — distinct
  from `StatusDeclined`. Declined = "not today, may reappear". Rejected =
  "permanently invalid (e.g. already exists in oCIS natively), never
  repropose". Wire `StatusRejected` into `DeliveredIDs()` so rejected
  candidates appear in the dedup guard.

### 2. `extctl gen --rerun-one`
A flag to regenerate a single replacement candidate when one is rejected
mid-day. Takes the rejected candidate ID, adds it to the dedup list with a
reason, produces exactly 1 new spec. Currently done manually by re-running
with `{{N}}=1` substitution in the shell; this should be a first-class
command.

### 3. Poll loop — `extctl poll`
Polls Jira every N minutes during business hours. When it detects a
candidate issue transitioned to the pick status ("Doing"), it:
1. Transitions the other open candidates to decline status ("Not Doing")
2. Creates a `git worktree` on `target_repo.checkout` for the picked branch
3. Copies scaffold + CLAUDE.md into `extensions/<id>/`
4. Runs `claude -p` with `build-extension.md` prompt (Phase B)
5. Runs the gate (`gate/run-gate.sh`)
6. On pass: pushes branch, opens GitHub PR, transitions Jira issue to build
   status ("In Review")
7. On fail after one repair attempt: transitions to blocked status, comments
   failure summary on the issue
8. Updates `runs/<date>/slate.json` with build outcome

The poll loop is the biggest remaining piece. Design it as an idempotent
reconciliation loop (check desired state vs observed state, do only the
missing work) so it's safe to restart.

### 4. Scheduling
- macOS: launchd plist, Mon–Fri 06:30 → `extctl gen`, business hours every
  10 min → `extctl poll`, login hook → `extctl reconcile`
- Linux: systemd user timers (same schedule)
- `extctl reconcile` — idempotent catch-up: runs gen if today's slate is
  missing, runs poll pass if any candidate is in "Doing" state

## Conventions

**Error handling:** wrap with `fmt.Errorf("context: %w", err)`. No panics
outside of `main()` init.

**State writes:** always write to a temp file then `os.Rename()` — see
`state.Save()` for the pattern. Never partial writes.

**Claude invocations:** always scoped tools, never open-ended Bash. For
Phase A: `Read,Grep,Glob` only. For Phase B:
`Read,Edit,Write,Grep,Glob,Bash(pnpm install),Bash(pnpm build),
Bash(pnpm test *),Bash(pnpm lint *),Bash(git add *),Bash(git commit *),
Bash(git status),Bash(git diff *)`. No `git push`, no `gh`, no network
tools — those are always orchestrator actions.

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
