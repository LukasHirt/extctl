# extctl

A local CLI pipeline that generates daily oCIS Web extension candidates as
one-page specs, submits them to Jira for review, and builds the picked one
into a reviewable GitHub pull request using Claude Code in headless mode.

Built for the [ownCloud Infinite Scale](https://github.com/owncloud/ocis)
web extension ecosystem.

## How it works

Every workday extctl runs two phases:

**Phase A — morning gen run**
Generates 3 agentic extension specs via Claude Code, creates a Jira issue for
each, and writes today's candidate slate to `runs/<date>/slate.json`.

**Phase B — pick-driven staged build**
Polls Jira for a candidate transitioned to `Doing` by the manager. On a pick it
fetches all comments from the Jira issue and passes them as context to every
Claude phase, so reviewer notes and constraints are reflected in the plan,
stages, and implementation. It then runs a human-in-the-loop planning phase
before writing any code:

1. **Planning:** creates a git worktree and runs Claude Code to write
   `runs/<date>/<id>/plan.md` — a structured plan for the extension
2. **Plan review:** the developer reads and optionally edits `plan.md`, then
   runs `extctl approve-plan <id>` to proceed
3. **Stage derivation:** Claude reads the approved plan and writes
   `runs/<date>/<id>/stages.md` — an ordered checklist of build stages
4. **Stage review:** the developer reads and optionally edits `stages.md`,
   then runs `extctl approve-stages <id>` to start building
5. **Staged build:** Claude implements each stage in sequence, running the
   gate (hygiene, build, lint, unit, and e2e checks) after every stage;
   failures trigger one repair attempt per stage. The e2e stage runs the
   extension's Playwright acceptance tests against a local oCIS started via
   `docker compose up -d` in the web-extensions checkout — it copies the
   built `dist/` into the running container, restarts it, and runs the tests.
   If oCIS is not running, the e2e stage fails. The e2e stage is serialized
   across concurrently-built candidates so their Playwright sessions don't
   collide.
6. **Publish:** pushes the branch and opens a GitHub PR once all stages pass

If a stage fails after repair, the build is paused and a blocked state is
recorded in `runs/<date>/slate.json` for manual resolution.

## Requirements

- Go >=1.26.4
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) installed and
  authenticated (`claude --version`)
- [gh CLI](https://cli.github.com/) installed and authenticated (`gh auth status`)
- A Jira Cloud instance with an API token
- A local checkout of `owncloud/web-extensions` (or equivalent target repo)

## Installation

```bash
git clone https://github.com/LukasHirt/extctl
cd extctl
go install ./cmd/extctl
```

## Configuration

Copy `extctl.example.yaml` to `extctl.yaml` and fill in the values.
Credentials are set via environment variables — never in the config file:

```bash
export EXTCTL_JIRA_EMAIL="your@email.com"
export EXTCTL_JIRA_TOKEN="your-api-token"
```

## Usage

### Phase A — spec generation

```bash
# Generate today's 3 specs and create Jira issues
extctl gen

# Preview the prompt and carryover context without calling Claude or Jira
extctl gen --dry-run

# Run Claude but skip Jira (useful for validating generation quality)
extctl gen --skip-jira

# Show today's candidate slate
extctl slate status
```

### Phase B — plan review and staged build

```bash
# Poll Jira for a pick and trigger the planning phase (run on a schedule every ~10 min)
extctl poll

# Preview what poll would do without side-effects
extctl poll --dry-run

# Manually trigger planning for a specific candidate
extctl build <candidate-id>

# After poll detects a pick and writes plan.md:
cat runs/<date>/<id>/plan.md       # review the plan
extctl approve-plan <candidate-id>  # derive stages and proceed

# After approve-plan writes stages.md:
cat runs/<date>/<id>/stages.md        # review the stages
extctl approve-stages <candidate-id>  # build stage by stage and open PR

# Re-run the gate on an existing worktree (for debugging)
extctl gate <candidate-id>
```

### Scaffold

```bash
# Populate scaffold/ from the owncloud/web-app-skeleton repo
extctl scaffold fetch
```

The scaffold is used as the starting template for every extension build.
Custom files (`src/composables/useLLM.ts`, `tests/e2e/`) are preserved
across fetches. Configure `scaffold.exclude` in `extctl.yaml` to adjust
which skeleton files are copied.

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All commits require a DCO sign-off.
