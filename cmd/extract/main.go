// Command extract runs triage + deep extraction over a run's chunks and
// writes triage.jsonl / events.jsonl, skipping work when inputs are unchanged.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/extract"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/stages"
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
		return fail(stderr, err)
	}
	if *workers > 0 {
		conf.Apply(cfg.Overrides{Workers: workers})
	}
	model := conf.Models.Primary
	if *fast {
		model = conf.Models.Fast
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	res, runErr := stages.Extract(ctx, r, extract.Config{
		Model:     model,
		Workers:   conf.Extract.Workers,
		Retries:   conf.Extract.Retries,
		NumCtx:    conf.Ollama.NumCtx,
		KeepAlive: conf.Ollama.KeepAlive.String(),
	}, conf.Ollama.URL)
	if res == nil && runErr != nil {
		return fail(stderr, runErr)
	}
	if res == nil {
		_, _ = fmt.Fprintf(stdout, "extract: up to date (%s)\n", r.Path(runstore.EventsJSONL))
		return 0
	}
	positive := 0
	for _, rec := range res.Triage {
		if rec.HasCodeAction {
			positive++
		}
	}
	_, _ = fmt.Fprintf(stdout, "extract: %d events, %d/%d chunks positive, %d failed -> %s\n",
		len(res.Events), positive, len(res.Triage), len(res.Failed), r.Path(runstore.EventsJSONL))
	if errors.Is(runErr, extract.ErrAllFailed) {
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
