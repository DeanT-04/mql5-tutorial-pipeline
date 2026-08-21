// Command verify runs static (and optional LLM) checks on a run's assembled
// files and writes report.json. Exits 1 when confidence is below threshold.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/verify"
)

const usage = `verify — run static (and optional LLM) checks on assembled files

Usage:
  verify --run DIR [--config FILE] [--llm-check]
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")
	configPath := fs.String("config", "pipeline.yaml", "config file path")
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
	conf, err := loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "verify: %v\n", err)
		return 2
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	outDir := filepath.Join(r.Dir(), "out")
	files, err := readOutDir(outDir)
	if err != nil {
		return fail(stderr, err)
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(stderr, "verify: no .mq5 files in out/ — run assemble first")
		return 1
	}

	inputHash, err := runstore.HashValue(struct {
		Files map[string]string `json:"files"`
		LLM   bool              `json:"llm"`
	}{files, *llmCheck})
	if err != nil {
		return fail(stderr, err)
	}
	if r.UpToDate(runstore.StageVerify, inputHash) {
		_, _ = fmt.Fprintf(stdout, "verify: up to date (%s)\n", r.Path(runstore.ReportJSON))
		return 0
	}

	rep := verify.Run(files)
	if *llmCheck {
		data, err := r.ReadFileCapped(runstore.EventsJSONL, 256<<20)
		if err != nil {
			return fail(stderr, fmt.Errorf("load events for --llm-check: %w", err))
		}
		var evts []events.Event
		if err := events.Reader(bytes.NewReader(data), func(rec any) error {
			if e, ok := rec.(events.Event); ok {
				evts = append(evts, e)
			}
			return nil
		}); err != nil {
			return fail(stderr, err)
		}
		model := conf.Models.Primary
		if err := verify.RunLLM(ctx, rep, files, evts, model, conf.Ollama.URL,
			conf.Ollama.KeepAlive.String(), conf.Ollama.NumCtx); err != nil {
			_, _ = fmt.Fprintf(stderr, "verify: warning: %v\n", err)
		}
	}

	repData, err := verify.Marshal(rep)
	if err != nil {
		return fail(stderr, err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.ReportJSON), repData); err != nil {
		return fail(stderr, err)
	}
	if err := r.MarkDone(runstore.StageVerify, inputHash); err != nil {
		return fail(stderr, err)
	}

	for _, f := range rep.Findings {
		_, _ = fmt.Fprintf(stderr, "verify: [%s] %s %s: %s\n", f.Severity, f.File, f.Check, f.Detail)
	}
	_, _ = fmt.Fprintf(stdout, "verify: confidence %.2f (%d findings) -> %s\n",
		rep.Confidence, len(rep.Findings), r.Path(runstore.ReportJSON))
	if rep.Confidence < conf.Verify.MinConfidence {
		_, _ = fmt.Fprintf(stderr, "verify: confidence %.2f below minimum %.2f\n",
			rep.Confidence, conf.Verify.MinConfidence)
		return 1
	}
	return 0
}

// readOutDir loads every *.mq5 file under dir keyed by bare name.
func readOutDir(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".mq5" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- fixed join of controlled dir
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		files[e.Name()] = string(data)
	}
	return files, nil
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
	_, _ = fmt.Fprintf(stderr, "verify: %v\n", err)
	return 1
}
