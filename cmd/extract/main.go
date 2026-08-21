// Command extract runs triage + deep extraction over a run's chunks and
// writes triage.jsonl / events.jsonl, skipping work when inputs are unchanged.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/extract"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ollama"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
)

const usage = `extract — triage chunks and extract code events via Ollama

Usage:
  extract --run DIR [--config FILE] [--fast] [--workers N]
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")
	configPath := fs.String("config", "pipeline.yaml", "config file path")
	fast := fs.Bool("fast", false, "use the fast model")
	workers := fs.Int("workers", 0, "parallel Ollama calls")

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
		_, _ = fmt.Fprintf(stderr, "extract: %v\n", err)
		return 2
	}
	if *workers > 0 {
		conf.Apply(cfg.Overrides{Workers: workers})
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	chunkData, err := r.ReadFileCapped(runstore.ChunksJSON, 64<<20)
	if err != nil {
		return fail(stderr, err)
	}
	model := conf.Models.Primary
	if *fast {
		model = conf.Models.Fast
	}
	inputHash, err := runstore.HashValue(struct {
		Chunks string `json:"chunks"`
		Model  string `json:"model"`
	}{string(chunkData), model})
	if err != nil {
		return fail(stderr, err)
	}
	if r.UpToDate(runstore.StageExtract, inputHash) {
		_, _ = fmt.Fprintf(stdout, "extract: up to date (%s)\n", r.Path(runstore.EventsJSONL))
		return 0
	}

	var chunks []segment.Chunk
	if err := json.Unmarshal(chunkData, &chunks); err != nil {
		return fail(stderr, fmt.Errorf("parse %s: %w", r.Path(runstore.ChunksJSON), err))
	}

	res, err := extract.Run(ctx, chunks, extract.Config{
		Model:     model,
		Workers:   conf.Extract.Workers,
		Retries:   conf.Extract.Retries,
		NumCtx:    conf.Ollama.NumCtx,
		KeepAlive: conf.Ollama.KeepAlive.String(),
	}, ollama.New(conf.Ollama.URL))
	soft := errors.Is(err, extract.ErrAllFailed)
	if err != nil && !soft {
		return fail(stderr, err)
	}

	var triageBuf, eventsBuf bytes.Buffer
	for _, rec := range res.Triage {
		if err := events.AppendJSONL(&triageBuf, rec); err != nil {
			return fail(stderr, err)
		}
	}
	for _, ev := range res.Events {
		if err := events.AppendJSONL(&eventsBuf, ev); err != nil {
			return fail(stderr, err)
		}
	}
	for _, f := range res.Failed {
		if err := events.AppendJSONL(&eventsBuf, f); err != nil {
			return fail(stderr, err)
		}
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.TriageJSONL), triageBuf.Bytes()); err != nil {
		return fail(stderr, err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.EventsJSONL), eventsBuf.Bytes()); err != nil {
		return fail(stderr, err)
	}
	if err := r.MarkDone(runstore.StageExtract, inputHash); err != nil {
		return fail(stderr, err)
	}

	positive := 0
	for _, rec := range res.Triage {
		if rec.HasCodeAction {
			positive++
		}
	}
	_, _ = fmt.Fprintf(stdout, "extract: %d events, %d/%d chunks positive, %d failed -> %s\n",
		len(res.Events), positive, len(chunks), len(res.Failed), r.Path(runstore.EventsJSONL))
	if soft {
		_, _ = fmt.Fprintln(stderr, "extract: every chunk failed extraction")
		return 1
	}
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
	_, _ = fmt.Fprintf(stderr, "extract: %v\n", err)
	return 1
}
