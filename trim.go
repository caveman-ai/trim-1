// Package trim is query-aware context compression: given a context and the
// current ask it returns a verbatim subset of the context's lines, in their
// original order, with one short marker line where lines were removed. It
// never rewrites a line.
//
// Each line is scored by a small embedded gradient-boosted tree model over
// lexical, structural and positional features (LineFeatures); an operating
// point turns the scores into a selection. Pure Go, deterministic, no network
// and no files at runtime. It fails closed: an unknown operating point, an
// unusable model, a blank query, a small or oversized input, a line that reads
// as a marker or a cut too small to matter returns the context unchanged.
package trim

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// maxContextBytes: scoring costs time and memory in proportion to the input.
	maxContextBytes = 8 << 20
	// maxContextLines: every line costs a 67-float feature row, so short lines cost more than their bytes.
	maxContextLines = 200_000
	// maxQueryBytes: BM25 cost grows with query terms; the latest part of the ask is what the context answers.
	maxQueryBytes = 8 << 10
)

const MaxContextBytes = maxContextBytes

// Policy turns line scores into a selection.
type Policy struct {
	// Floor keeps every line scoring at least this share of the context's
	// best line. Relative, because calibrated probabilities shrink as a
	// context grows: one absolute threshold cuts long contexts far too hard.
	Floor float64
	// TopK always keeps the K best-scoring lines, so the output is never only
	// markers.
	TopK int
	// Context keeps this many lines on each side of a line kept by Floor or
	// TopK.
	Context int
	// Headers keeps up to this many enclosing block headers (the nearest
	// earlier lines with a smaller indent) of every such line and, when above
	// zero, the line opening its section (a Markdown heading, "[start of
	// path]", "==> path <==", "diff --git", "File: path", or any short line
	// in square brackets, such as a dated "[Session 8: ...]").
	Headers int
	// Gap fills a run of at most this many removed lines between kept lines;
	// a marker would cost about as much as the lines.
	Gap int
	// MinChars returns contexts shorter than this unchanged.
	MinChars int
	// MinCut returns the context unchanged when the selection would remove
	// less than this share of its bytes.
	MinCut float64
}

// Points are the named operating points, tuned on the benchmark dev splits.
var Points = map[string]Policy{
	"conservative": {Floor: 0.0035, TopK: 3, Context: 1, Headers: 1, Gap: 2, MinChars: 2000, MinCut: 0.1},
	"balanced":     {Floor: 0.008, TopK: 3, Context: 1, Headers: 1, Gap: 2, MinChars: 2000, MinCut: 0.1},
	"aggressive":   {Floor: 0.035, TopK: 3, Context: 1, Headers: 1, Gap: 2, MinChars: 2000, MinCut: 0.1},
}

// DefaultPoint is used when Options.Point is empty.
const DefaultPoint = "balanced"

// Options selects how much to keep.
type Options struct {
	// Point names an entry of Points; empty means DefaultPoint. An unknown
	// name returns the context unchanged.
	Point string
	// Policy, when set, replaces the named point (for tuning and frontiers).
	Policy *Policy
}

// Result is the outcome of one Compress call.
type Result struct {
	// Output is the compressed text, or the context itself when Unchanged.
	Output string
	// KeptLines and RemovedLines count the context's lines ("\n" separated;
	// the empty piece after a final "\n" is not a line).
	KeptLines, RemovedLines int
	// Point is the operating point used ("custom" for Options.Policy).
	Point  string
	Policy Policy
	// Unchanged is true when the context was returned as is; Reason says why.
	Unchanged bool
	Reason    string
}

// Compress keeps the lines of context that the query needs. A blank query is
// no query: the context comes back unchanged.
func Compress(context, query string, opts Options) Result {
	name, policy := opts.Point, Policy{}
	if opts.Policy != nil {
		name, policy = "custom", *opts.Policy
	} else {
		if name == "" {
			name = DefaultPoint
		}
		p, ok := Points[name]
		if !ok {
			return unchanged(context, name, policy, "unknown operating point")
		}
		policy = p
	}
	switch {
	case strings.TrimSpace(query) == "":
		return unchanged(context, name, policy, "no query")
	case len(context) < policy.MinChars:
		return unchanged(context, name, policy, "context below the minimum size")
	case len(context) > maxContextBytes:
		return unchanged(context, name, policy, "context over 8 MiB")
	case lineCount(context) > maxContextLines:
		return unchanged(context, name, policy, "context over 200,000 lines")
	case strings.Contains(context, "…") && markerLine.MatchString(context):
		return unchanged(context, name, policy, "context has a line that reads as an elision marker")
	}
	lines, scores, err := Score(context, query)
	if err != nil {
		return unchanged(context, name, policy, err.Error())
	}
	out, kept := policy.Apply(lines, scores)
	if float64(len(out)) > float64(len(context))*(1-policy.MinCut) {
		return unchanged(context, name, policy, "cut below the minimum")
	}
	return Result{Output: out, KeptLines: kept, RemovedLines: lineCount(context) - kept, Point: name, Policy: policy}
}

// Score splits context at "\n" and returns its lines with each line's keep
// probability for the query; blank lines score 0.
func Score(context, query string) ([]string, []float64, error) {
	m, err := model()
	if err != nil {
		return nil, nil, err
	}
	lines, rows := LineFeatures(context, query)
	scores := make([]float64, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			scores[i] = m.probability(rows[i])
		}
	}
	return lines, scores, nil
}

// Apply selects lines by score and renders them: kept lines verbatim, one
// marker line per removed run. It returns the output and the kept line count.
// The empty piece after a final "\n" is not a line: the output ends with
// "\n" as the context does.
func (p Policy) Apply(lines []string, scores []float64) (string, int) {
	final := len(lines) > 1 && lines[len(lines)-1] == ""
	if final {
		lines, scores = lines[:len(lines)-1], scores[:len(scores)-1]
	}
	blank := make([]bool, len(lines))
	indent := make([]int, len(lines))
	section := make([]int, len(lines))
	open := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		blank[i] = trimmed == ""
		indent[i] = indentOf(line)
		section[i] = open
		if hasAnyPrefix(trimmed, sectionPrefixes) || len(trimmed) < 120 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			open = i
		}
	}
	// A CRLF context keeps its "\r" on every kept line; markers match it.
	crlf := len(lines) > 0 && strings.HasSuffix(lines[0], "\r")
	out, kept := render(lines, selectLines(scores, blank, indent, section, p), crlf, final)
	if final {
		out += "\n"
	}
	return out, kept
}

func unchanged(context, name string, policy Policy, reason string) Result {
	return Result{Output: context, KeptLines: lineCount(context), Point: name, Policy: policy, Unchanged: true, Reason: reason}
}

// lineCount counts the lines of context; the empty piece after a final "\n"
// is not one.
func lineCount(context string) int {
	return strings.Count(strings.TrimSuffix(context, "\n"), "\n") + 1
}

// markerLine matches a line that reads as a marker: a context holding one
// would make the output ambiguous.
var markerLine = regexp.MustCompile(`(?m)^[ \t]*… \d+ lines? …[ \t\r]*$`)

func selectLines(score []float64, blank []bool, indent, section []int, p Policy) []bool {
	n := len(score)
	order := make([]int, 0, n)
	for i := range score {
		if !blank[i] {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case score[a] > score[b]:
			return -1
		case score[a] < score[b]:
			return 1
		}
		return 0
	})
	seed := make([]bool, n)
	for k, i := range order {
		seed[i] = k < p.TopK || score[i] >= p.Floor*score[order[0]]
	}

	keep := slices.Clone(seed)
	headers := blockHeaders(indent, blank)
	for i := range seed {
		if !seed[i] {
			continue
		}
		for j := max(0, i-p.Context); j <= min(n-1, i+p.Context); j++ {
			keep[j] = true
		}
		for h, k := headers[i], 0; h >= 0 && k < p.Headers; h, k = headers[h], k+1 {
			keep[h] = true
		}
		if p.Headers > 0 && section[i] >= 0 {
			keep[section[i]] = true
		}
	}
	// Fill short gaps and gaps of blank lines between kept lines.
	last := -1
	for i := range keep {
		if !keep[i] {
			continue
		}
		if gap := blank[last+1 : i]; last >= 0 && len(gap) > 0 && (len(gap) <= p.Gap || allBlank(gap)) {
			for j := last + 1; j < i; j++ {
				keep[j] = true
			}
		}
		last = i
	}
	return keep
}

var sectionPrefixes = []string{"# ", "## ", "### ", "#### ", "[start of ", "==> ", "diff --git ", "File: "}

func allBlank(b []bool) bool {
	for _, x := range b {
		if !x {
			return false
		}
	}
	return true
}

// render writes the kept lines verbatim and one marker line per removed run,
// ending each marker in "\r" when crlf is set and a "\n" follows it (always,
// when final: the caller adds the context's final newline).
func render(lines []string, keep []bool, crlf, final bool) (string, int) {
	var b strings.Builder
	kept := 0
	for i := 0; i < len(lines); {
		if i > 0 {
			b.WriteByte('\n')
		}
		if keep[i] {
			b.WriteString(lines[i])
			kept++
			i++
			continue
		}
		j := i
		for j < len(lines) && !keep[j] {
			j++
		}
		b.WriteString(marker(j - i))
		if crlf && (j < len(lines) || final) {
			b.WriteByte('\r')
		}
		i = j
	}
	return b.String(), kept
}

func marker(n int) string {
	if n == 1 {
		return "… 1 line …"
	}
	return fmt.Sprintf("… %d lines …", n)
}
