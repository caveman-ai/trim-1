// Command trim keeps the lines of a context that a query needs.
//
//	trim --query "why does the build fail" build.log
//	cat build.log | trim --query "why does the build fail" --point balanced
//
// The trimmed context goes to stdout and a one-line summary to stderr.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	trim "github.com/caveman-ai/trim-1"
)

func main() {
	query := flag.String("query", "", "the current ask (required)")
	point := flag.String("point", trim.DefaultPoint, "operating point: conservative, balanced or aggressive")
	maxBytes := flag.Int("max-bytes", 0, "cap the output at this many bytes (0: no cap)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: trim --query QUERY [--point POINT] [--max-bytes N] [FILE]   (reads stdin without FILE)")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *query == "" || flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}
	if _, ok := trim.Points[*point]; !ok {
		fmt.Fprintf(os.Stderr, "trim: unknown point %q\n", *point)
		os.Exit(2)
	}
	if *maxBytes < 0 {
		fmt.Fprintf(os.Stderr, "trim: negative --max-bytes %d\n", *maxBytes)
		os.Exit(2)
	}

	src := io.Reader(os.Stdin)
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "trim:", err)
			os.Exit(1)
		}
		src = f
	}
	context, err := io.ReadAll(io.LimitReader(src, trim.MaxContextBytes+1))
	if err == nil && len(context) > trim.MaxContextBytes {
		err = errors.New("context over 8 MiB")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "trim:", err)
		os.Exit(1)
	}

	res := trim.Compress(string(context), *query, trim.Options{Point: *point, MaxBytes: *maxBytes})
	if _, err := os.Stdout.WriteString(res.Output); err != nil {
		fmt.Fprintln(os.Stderr, "trim:", err)
		os.Exit(1)
	}
	if res.Unchanged {
		fmt.Fprintf(os.Stderr, "trim: unchanged (%s)\n", res.Reason)
		return
	}
	fmt.Fprintf(os.Stderr, "trim: kept %d lines, removed %d, %d -> %d bytes\n",
		res.KeptLines, res.RemovedLines, len(context), len(res.Output))
}
