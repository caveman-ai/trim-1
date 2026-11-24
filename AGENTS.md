# trim-1 (`caveman-ai/trim-1`)

Public Apache-2.0 pure-Go library and CLI (`cmd/trim`) embedding the 1.2 MB
`trim-1` gradient-boosted tree model (`model.bin`) that keeps the exact lines
of a context an AI agent needs for its current query and drops the rest.

`CLAUDE.md` is a symlink to this file.

## Layout

| Path | Role |
|---|---|
| `trim.go` | Public Go API (`Compress`, `Score`, `Options`), fail-closed guards, CRLF preservation |
| `features.go` | Per-line feature extraction |
| `bm25.go` | Pure-Go BM25 lexical scoring |
| `model.go`, `model.bin` | Embedded 712-tree GBDT evaluator (includes BSD `math.Exp`, see `LICENSE-go`) |
| `cmd/trim/main.go` | `trim` CLI (`--query`, `--point conservative\|balanced\|aggressive`) |
| `trim_test.go`, `testdata/` | `TestPredictionParity` (bit-exact vs training library) and `TestGolden` |
| `docs/assets/` | Repository banner and social preview assets |

## Checks

```bash
go vet ./...
go test -race ./...
go build ./cmd/trim
```

## Invariants

- **Zero external dependencies.** Standard library only (`go.mod` must have no third-party `require` entries).
- **Deterministic and lossless on kept lines.** Same `(context, query, point)` always returns identical bytes; kept lines are never rewritten or summarized; CRLF line endings are preserved.
- **Fail closed.** Return context unchanged when query is blank, input is < 2 KB or > 8 MiB / 200,000 lines, input already contains a trim marker, or savings are < 10%.
- **Bit-exact parity.** `TestPredictionParity` and `TestGolden` must stay green on every change.
- **Public repo.** Never reference private Caveman repositories or unpublished training pipelines.
