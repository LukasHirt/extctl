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

**Phase B — pick-driven build**
Polls Jira for a candidate transitioned to `Doing` by the manager. On a pick it:
1. Creates a git worktree in the target repo for the extension branch
2. Copies the scaffold template and runs Claude Code to implement the extension
3. Validates the result through the gate (hygiene, build, lint, unit checks)
4. Repairs failures by resuming the Claude session (up to `max_repair_attempts`)
5. Pushes the branch and opens a GitHub PR
6. Transitions the Jira issue to `Done` automatically when the PR is merged

If all repair attempts are exhausted, a draft PR is opened with the gate
failure details as a comment. The Jira issue stays in `Doing` for manual
resolution. All other Jira status changes are made by the manager or developer.

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

### Phase B — build pipeline

```bash
# Poll Jira for a pick and trigger the build (run on a schedule every ~10 min)
extctl poll

# Preview what poll would do without side-effects
extctl poll --dry-run

# Manually trigger a build for a specific candidate
extctl build <candidate-id>

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
