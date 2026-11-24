# Contributing to trim-1

Thank you for contributing to `caveman-ai/trim-1`. Read [`AGENTS.md`](AGENTS.md)
for invariants before opening a pull request.

## Prerequisites

- Go 1.23 or newer

## Verification

```bash
gofmt -l .
go vet ./...
go test -race ./...
go build ./cmd/trim
```

## Guidelines

- Keep the module standard-library only (no external Go dependencies).
- Preserve bit-exact parity (`TestPredictionParity`), golden outputs (`TestGolden`), and fail-closed behavior.
- Use scoped conventional commits (for example, `fix(trim): ...`, `docs(repo): ...`).
- Report security issues privately per [`SECURITY.md`](SECURITY.md).
