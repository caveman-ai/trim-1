package trim

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.txt")

func logContext() string {
	var b strings.Builder
	b.WriteString("$ kubectl logs deploy/checkout --since=1h\n")
	for i := range 400 {
		fmt.Fprintf(&b, "2026-10-11T03:%02d:%02dZ INFO checkout request %d served in %dms\n", i/60%60, i%60, 1000+i, 10+i%17)
		if i == 271 {
			b.WriteString("2026-10-11T03:04:31Z ERROR payment client: ledger-db timeout after 30s (order 1271)\n")
			b.WriteString("    at LedgerClient.charge (ledger/client.go:88)\n")
		}
	}
	b.WriteString("exit status 1\n")
	return b.String()
}

const logQuery = "Why did the checkout payment fail? Find the ledger error."

func TestDeterministicVerbatimInOrder(t *testing.T) {
	ctx := logContext()
	a := Compress(ctx, logQuery, Options{})
	b := Compress(ctx, logQuery, Options{})
	if a.Output != b.Output || a.KeptLines != b.KeptLines {
		t.Fatal("two calls on the same input differ")
	}
	if a.Unchanged {
		t.Fatalf("log context returned unchanged: %s", a.Reason)
	}
	if !strings.HasSuffix(a.Output, "\n") {
		t.Fatal("the context's final newline was dropped")
	}
	lines := strings.Split(strings.TrimSuffix(ctx, "\n"), "\n")
	next, kept := 0, 0
	for _, out := range strings.Split(strings.TrimSuffix(a.Output, "\n"), "\n") {
		if strings.HasPrefix(out, "… ") && strings.HasSuffix(out, " …") {
			continue
		}
		for next < len(lines) && lines[next] != out {
			next++
		}
		if next == len(lines) {
			t.Fatalf("output line %q is not a later line of the input", out)
		}
		next++
		kept++
	}
	if kept != a.KeptLines || a.KeptLines+a.RemovedLines != len(lines) {
		t.Fatalf("kept %d, result says %d kept + %d removed of %d", kept, a.KeptLines, a.RemovedLines, len(lines))
	}
	if !strings.Contains(a.Output, "ledger-db timeout") {
		t.Fatalf("the error line the query asks for was dropped:\n%s", a.Output)
	}
}

func TestRefusals(t *testing.T) {
	ctx := logContext()
	for name, tc := range map[string]struct {
		context, query string
		opts           Options
		reason         string
	}{
		"unknown point": {ctx, logQuery, Options{Point: "turbo"}, "unknown operating point"},
		"small input":   {"one line\ntwo lines\n", logQuery, Options{}, "context below the minimum size"},
		"cut too small": {ctx, logQuery, Options{Policy: &Policy{MinCut: 1}}, "cut below the minimum"},
		"blank query":   {ctx, " \n\t ", Options{}, "no query"},
		"over 8 MiB":    {strings.Repeat(ctx, maxContextBytes/len(ctx)+1), logQuery, Options{}, "context over 8 MiB"},
		"many lines":    {strings.Repeat("x\n", maxContextLines+1), logQuery, Options{}, "context over 200,000 lines"},
		"marker line":   {strings.Replace(ctx, "exit status 1", "… 3 lines …", 1), logQuery, Options{}, "context has a line that reads as an elision marker"},
	} {
		res := Compress(tc.context, tc.query, tc.opts)
		if !res.Unchanged || res.Output != tc.context || res.Reason != tc.reason {
			t.Errorf("%s: want the input back unchanged for %q, got unchanged=%v reason=%q", name, tc.reason, res.Unchanged, res.Reason)
		}
	}
}

// Only the query's last 8 KiB count: text before it changes nothing.
func TestQueryCap(t *testing.T) {
	ctx := logContext()
	tail := strings.Repeat("é", maxQueryBytes) + logQuery // the cut lands inside an "é"
	_, a := LineFeatures(ctx, "ledger payment "+tail)
	_, b := LineFeatures(ctx, "checkout timeout "+tail)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("text before the query's last 8 KiB changed the features")
	}
}

// The empty piece after a final "\n" is not a line: it is never in a marker,
// and the output keeps the final newline.
func TestFinalNewlineIsNotALine(t *testing.T) {
	lines := strings.Split("a\nb\nc\nd\n", "\n")
	p := Policy{Floor: 1, TopK: 1}
	for _, tc := range []struct {
		scores []float64
		want   string
	}{
		{[]float64{1, 0, 0, 0, 0}, "a\n… 3 lines …\n"},
		{[]float64{0, 0, 0, 1, 0}, "… 3 lines …\nd\n"},
	} {
		if out, kept := p.Apply(lines, tc.scores); out != tc.want || kept != 1 {
			t.Errorf("got %q with %d kept, want %q with 1", out, kept, tc.want)
		}
	}
}

// A CRLF context gets CRLF markers, so the output never mixes line endings.
func TestCRLFMarkers(t *testing.T) {
	lines := strings.Split("a\r\nb\r\nc\r\nd\r\n", "\n")
	p := Policy{Floor: 1, TopK: 1}
	if out, _ := p.Apply(lines, []float64{1, 0, 0, 0, 0}); out != "a\r\n… 3 lines …\r\n" {
		t.Errorf("got %q", out)
	}
	// Without a final newline the last marker is unterminated, like a last kept line.
	if out, _ := p.Apply(strings.Split("a\r\nb\r\nc", "\n"), []float64{1, 0, 0}); out != "a\r\n… 2 lines …" {
		t.Errorf("got %q", out)
	}
	res := Compress(strings.ReplaceAll(logContext(), "\n", "\r\n"), logQuery, Options{})
	if res.Unchanged {
		t.Fatalf("CRLF log context returned unchanged: %s", res.Reason)
	}
	for _, line := range strings.Split(strings.TrimSuffix(res.Output, "\n"), "\n") {
		if !strings.HasSuffix(line, "\r") {
			t.Fatalf("line %q does not end in CRLF", line)
		}
	}
}

func TestGolden(t *testing.T) {
	got := Compress(logContext(), logQuery, Options{}).Output
	path := "testdata/golden.txt"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("output changed; run go test -update after a deliberate model or policy change\n got:\n%s", got)
	}
}

func TestModelLoads(t *testing.T) {
	if len(FeatureNames) != nFeatures {
		t.Fatalf("%d feature names, %d feature slots", len(FeatureNames), nFeatures)
	}
	m, err := model()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.roots) < 10 {
		t.Fatalf("embedded model has %d trees", len(m.roots))
	}
	if _, err := parseModel(modelBytes[:len(modelBytes)-1]); err == nil {
		t.Fatal("a truncated model parsed")
	}
}

// A header that does not match the model's size fails before anything it
// sizes is allocated.
func TestModelHeaderBounds(t *testing.T) {
	huge := slices.Clone(modelBytes)
	binary.LittleEndian.PutUint32(huge[12:], 0xFFFFFFF0) // trees
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for name, b := range map[string][]byte{"truncated header": modelBytes[:20], "header only": modelBytes[:24], "huge tree count": huge} {
		if _, err := parseModel(b); err == nil {
			t.Errorf("%s parsed", name)
		}
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Fatalf("parsing bad headers allocated %d bytes", n)
	}
}

// TestPredictionParity replays rows LightGBM scored at export time
// (the training script writes testdata/parity.bin with the model) and wants the same
// raw scores, bit for bit.
func TestPredictionParity(t *testing.T) {
	b, err := os.ReadFile("testdata/parity.bin")
	if err != nil {
		t.Fatal(err)
	}
	n, width := int(binary.LittleEndian.Uint32(b)), int(binary.LittleEndian.Uint32(b[4:]))
	if width != nFeatures || len(b) != 8+n*width*4+n*8 {
		t.Fatalf("parity fixture: %d rows of %d features in %d bytes", n, width, len(b))
	}
	m, err := model()
	if err != nil {
		t.Fatal(err)
	}
	rows, raws := b[8:8+n*width*4], b[8+n*width*4:]
	row := make([]float32, width)
	for i := range n {
		for j := range row {
			row[j] = math.Float32frombits(binary.LittleEndian.Uint32(rows[(i*width+j)*4:]))
		}
		want := math.Float64frombits(binary.LittleEndian.Uint64(raws[i*8:]))
		if got := m.raw(row); got != want {
			t.Fatalf("row %d: Go %v, LightGBM %v", i, got, want)
		}
	}
}

// benchContext mixes log, code and prose lines up to about n cl100k tokens
// (four bytes a token).
func benchContext(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < 4*n; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "2026-10-11T03:%02d:%02dZ INFO worker-%d processed batch %d in %dms\n", i/60%60, i%60, i%7, i, 10+i%31)
		case 1:
			fmt.Fprintf(&b, "    def handle_%d(self, request, retries=%d):\n", i, i%5)
		case 2:
			fmt.Fprintf(&b, "        return self.client.fetch(request.path, timeout=%d)  # see docs/api_%d.md\n", i%30, i)
		default:
			fmt.Fprintf(&b, "The scheduler moved shard %d to node %d after the health check reported latency above budget.\n", i, i%13)
		}
	}
	return b.String()
}

func benchmarkCompress(b *testing.B, tokens int) {
	ctx := benchContext(tokens)
	Compress(ctx, logQuery, Options{})
	b.ResetTimer()
	for range b.N {
		Compress(ctx, logQuery, Options{})
	}
}

func BenchmarkCompress2k(b *testing.B)  { benchmarkCompress(b, 2000) }
func BenchmarkCompress50k(b *testing.B) { benchmarkCompress(b, 50000) }
