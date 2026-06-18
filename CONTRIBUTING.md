# Contributing to extctl

Thank you for your interest in contributing. This document covers the essentials.

## Developer Certificate of Origin (DCO)

All contributions must be signed off under the
[Developer Certificate of Origin (DCO) v1.1](https://developercertificate.org/).
This certifies that you wrote or otherwise have the right to submit the code
you are contributing.

Add a `Signed-off-by` line to every commit message:

```
git commit -s -m "feat: add decay action for aged-out candidates"
```

Which produces:

```
feat: add decay action for aged-out candidates

Signed-off-by: Your Name <your@email.com>
```

Commits without a sign-off will not be merged.

## How to contribute

1. Fork the repository and create a branch from `main`.
2. Make your changes. Keep commits small and focused.
3. Ensure `go build ./...` and `go test ./...` pass.
4. Run `go vet ./...` and `golangci-lint run` if you have it installed.
5. Sign off every commit (`git commit -s`).
6. Open a pull request against `main` with a clear description of what and why.

## Reporting issues

Open a GitHub issue. Include the extctl version (`extctl --version`), your OS,
and the full command + output that demonstrates the problem.

## Code style

Standard Go conventions apply (`gofmt`, meaningful names, errors wrapped with
`fmt.Errorf("context: %w", err)`). No external linter config is required beyond
`go vet`.

## License

By contributing you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE).
