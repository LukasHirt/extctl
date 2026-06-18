# extctl

A local CLI pipeline that generates daily oCIS Web extension candidates as
one-page specs, submits them to Jira for review, and builds the picked one
into a reviewable GitHub pull request using Claude Code in headless mode.

Built for the [ownCloud Infinite Scale](https://github.com/owncloud/ocis)
web extension ecosystem.

## Status

Early development — Phase 1 (morning gen run + Jira issue creation).

## Requirements

- Go 1.26+
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) installed and
  authenticated (`claude --version`)
- Access to a Jira Server / Data Center instance with a Personal Access Token
- A checkout of `owncloud/web-extensions` (or equivalent target repo)

## Installation

```bash
git clone https://github.com/<your-org>/extctl
cd extctl
go install ./cmd/extctl
```

## Configuration

Copy `extctl.example.yaml` to `extctl.yaml` in your `extctl-pipeline` working
directory and fill in the values. See comments in the example file for details.

## Usage

```bash
# Generate today's 3 agentic extension specs and create Jira issues
extctl gen

# Preview what would be generated without creating issues
extctl gen --dry-run

# Show today's slate status
extctl slate status
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All commits require a DCO sign-off.
