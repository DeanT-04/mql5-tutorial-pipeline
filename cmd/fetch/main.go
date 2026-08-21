package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `fetch — download transcript for one YouTube video

Usage:
  fetch <youtube-url> [flags]

Flags:
  --runs-dir DIR         parent directory for runs (default: runs)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runsDir := fs.String("runs-dir", "runs", "parent directory for runs")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stderr, usage)
			return 2
		}
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	_ = *runsDir

	_, _ = fmt.Fprintf(stderr, "fetch: not implemented yet (url=%s)\n", fs.Arg(0))
	return 1
}
