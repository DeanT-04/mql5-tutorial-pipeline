package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `segment — merge a run's transcript into code-step chunks

Usage:
  segment --run DIR
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("segment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stderr, usage)
			return 2
		}
		return 2
	}
	if *runDir == "" {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	_, _ = fmt.Fprintf(stderr, "segment: not implemented yet (run=%s)\n", *runDir)
	return 1
}
