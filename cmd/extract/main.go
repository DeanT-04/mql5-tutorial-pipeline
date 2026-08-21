package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `extract — triage chunks and extract code events via Ollama

Usage:
  extract --run DIR [--fast]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")
	fast := fs.Bool("fast", false, "use the fast model")

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

	_ = *fast

	_, _ = fmt.Fprintf(stderr, "extract: not implemented yet (run=%s)\n", *runDir)
	return 1
}
