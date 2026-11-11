<div align="center">

<a href="https://github.com/caveman-ai/trim-1"><img src="docs/assets/banner.png" alt="Caveman / trim-1. The word TRIM-1 in solid pixel letters with a double-line shadow. Why read many line when few do trick. Agent read whole log to find one error. trim-1 keep the lines the ask need and drop the rest. Every kept line exact, nothing rewritten. Say this to shell: trim --query &quot;why fail&quot; build.log. How big is rock: 1.2 MB model, 7 ms for 5k tokens on one core, 0 GPUs" width="880"></a>

</div>

# trim-1

trim-1 is a small model that decides which lines of a context an AI agent
needs for its current ask, and drops the rest.

Give it a context (a tool result, a file, a log, a long chat) and the current
query. It returns the context's own lines, in their original order, with a
short marker wherever lines were removed. It never rewrites or summarizes a
line, so identifiers, paths and error text come through exactly as they were.

- **1.2 MB.** 712 gradient-boosted trees, not a neural network.
- **Milliseconds on one CPU core.** No GPU, no network call.
- **Deterministic.** No sampling and no randomness: the same context and query
  give the same output.
- **No dependencies.** Pure Go, standard library only. The model is embedded.

The model card, with benchmark results and limits, is on Hugging Face:
[caveman-so/trim-1](https://huggingface.co/caveman-so/trim-1).

## Install

Needs Go 1.23 or newer.

```bash
go install github.com/caveman-ai/trim-1/cmd/trim@latest
```

## Use

From the command line:

```bash
trim --query "why does the build fail" build.log > build.trimmed.log
```

```bash
cat long-document.md | trim --query "what is the refund policy" --point balanced
```

The trimmed context goes to stdout. A one-line summary goes to stderr:

```
trim: kept 246 lines, removed 189, 47831 -> 36344 bytes
```

From Go:

```go
import trim "github.com/caveman-ai/trim-1"

res := trim.Compress(context, query, trim.Options{Point: "balanced"})
fmt.Print(res.Output)
```

`trim.Score(context, query)` returns every line with its keep probability, if
you want to make the selection yourself.

## Which setting to use

`--point` decides how hard trim-1 cuts: `conservative`, `balanced` (the
default) or `aggressive`. Cutting harder costs quality, because every removed
line is one the model can no longer read.

- **Long-document retrieval** is what trim-1 is for. Use `balanced`.
- **Coding-agent tool outputs** are short and dense. Stay at `balanced` or
  `conservative` and expect small savings.

The [model card](https://huggingface.co/caveman-so/trim-1#which-setting-to-use)
has the numbers behind this.

`--max-bytes` (`Options.MaxBytes`) caps the output's size, markers included.
When the point's output is over the cap, trim-1 keeps fewer of the
best-scoring lines until it fits. It is bytes, not tokens: estimate about four
bytes a token.

```bash
trim --query "why does the build fail" --max-bytes 4000 build.log
```

## When it does nothing

trim-1 fails closed. It returns the context unchanged, and says why on stderr,
when:

- the query is blank,
- the context is under 2,000 bytes,
- the context is over 8 MiB or 200,000 lines,
- the context already has a removal marker line, such as trim-1's own output,
- the cut would remove less than 10% of the bytes (unless `--max-bytes`
  forces it),
- even the three best lines do not fit in `--max-bytes`.

The `trim` command exits with status 2 on a missing `--query`, an unknown
`--point` or a negative `--max-bytes`. Flags go before the file name.

## Test

```bash
go test ./...
```

`TestPredictionParity` checks the Go tree evaluation against the training
library's own predictions, bit for bit. `TestGolden` pins the full output on a
sample log.

## License

Apache-2.0. `model.go` includes a copy of Go's `math.Exp`, under the BSD license
in `LICENSE-go`. trim-1 is the model inside
[Caveman](https://github.com/JuliusBrussee/caveman).
