// Command segment merges a run's transcript into code-step chunks
// (chunks.json), skipping work when inputs are unchanged.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/stages"
)

const usage = `segment — merge a run's transcript into code-step chunks

Usage:
  segment --run DIR [--config FILE]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("segment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")
	configPath := fs.String("config", "pipeline.yaml", "config file path")

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
	conf, err := loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "segment: %v\n", err)
		return 2
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	n, err := stages.Segment(r, segment.Config{
		MaxTokens:  conf.Segment.MaxTokens,
		MaxSeconds: conf.Segment.MaxSeconds,
		PauseGap:   conf.Segment.PauseGap,
		Cues:       segment.DefaultCues,
	})
	if err != nil {
		return fail(stderr, err)
	}
	if n == -1 {
		_, _ = fmt.Fprintf(stdout, "segment: up to date (%s)\n", r.Path(runstore.ChunksJSON))
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "segment: %d chunks -> %s\n", n, r.Path(runstore.ChunksJSON))
	return 0
}

// loadConfig loads the config; a missing default pipeline.yaml falls back to
// built-in defaults, an explicitly named missing file is an error.
func loadConfig(path string) (*cfg.Config, error) {
	if _, err := os.Stat(path); err != nil {
		if path != "pipeline.yaml" {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		return cfg.Default(), nil
	}
	return cfg.Load(path)
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "segment: %v\n", err)
	return 1
}
