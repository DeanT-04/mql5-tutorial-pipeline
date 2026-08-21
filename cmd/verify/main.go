package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `verify — run static (and optional LLM) checks on assembled files

Usage:
  verify --run DIR [--llm-check]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")
	llmCheck := fs.Bool("llm-check", false, "additionally compare files against code events via the model")

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

	_ = *llmCheck

	_, _ = fmt.Fprintf(stderr, "verify: not implemented yet (run=%s)\n", *runDir)
	return 1
}
