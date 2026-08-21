// Command verify runs static (and optional LLM) checks on a run's assembled
// files and writes report.json. Exits 1 when confidence is below threshold.
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
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/stages"
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
	configPath := fs.String("config", cfg.DefaultPath, "config file path")
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
	conf, err := cfg.LoadOrDefault(*configPath)
	if err != nil {
		return fail(stderr, err)
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	files, err := stages.ReadOutDir(filepath.Join(r.Dir(), "out"))
	if err != nil {
		return fail(stderr, err)
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(stderr, "verify: no .mq5 files in out/ — run assemble first")
		return 1
	}

	rep, err := stages.Verify(ctx, r, stages.VerifyOptions{
		LLM:       *llmCheck,
		Model:     conf.Models.Primary,
		BaseURL:   conf.Ollama.URL,
		KeepAlive: conf.Ollama.KeepAlive.String(),
		NumCtx:    conf.Ollama.NumCtx,
	})
	if err != nil && rep == nil {
		return fail(stderr, err)
	}
	if rep == nil {
		_, _ = fmt.Fprintf(stdout, "verify: up to date (%s)\n", r.Path(runstore.ReportJSON))
		return 0
	}
	for _, f := range rep.Findings {
		_, _ = fmt.Fprintf(stderr, "verify: [%s] %s %s: %s\n", f.Severity, f.File, f.Check, f.Detail)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "verify: warning: %v\n", err)
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

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "verify: %v\n", err)
	return 1
}
