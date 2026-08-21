// Command segment merges a run's transcript into code-step chunks
// (chunks.json), skipping work when inputs are unchanged.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
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
	data, err := r.ReadFileCapped(runstore.TranscriptJSON, transcript.MaxFileBytes)
	if err != nil {
		return fail(stderr, err)
	}
	inputHash := runstore.HashBytes(data)
	if r.UpToDate(runstore.StageSegment, inputHash) {
		_, _ = fmt.Fprintf(stdout, "segment: up to date (%s)\n", r.Path(runstore.ChunksJSON))
		return 0
	}

	var lines []transcript.Line
	if err := json.Unmarshal(data, &lines); err != nil {
		return fail(stderr, fmt.Errorf("parse %s: %w", r.Path(runstore.TranscriptJSON), err))
	}
	chunks := segment.Run(lines, segment.Config{
		MaxTokens:  conf.Segment.MaxTokens,
		MaxSeconds: conf.Segment.MaxSeconds,
		PauseGap:   conf.Segment.PauseGap,
		Cues:       segment.DefaultCues,
	})
	out, err := segment.Marshal(chunks)
	if err != nil {
		return fail(stderr, err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.ChunksJSON), out); err != nil {
		return fail(stderr, err)
	}
	if err := r.MarkDone(runstore.StageSegment, inputHash); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "segment: %d chunks -> %s\n", len(chunks), r.Path(runstore.ChunksJSON))
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
