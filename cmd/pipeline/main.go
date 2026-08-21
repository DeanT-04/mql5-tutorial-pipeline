// Command pipeline turns one YouTube MQL5 tutorial video into .mq5 code by
// running fetch → segment → extract → assemble → verify with content-hash
// resume at every stage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/extract"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/stages"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

const usage = `pipeline — YouTube MQL5 tutorial URL to .mq5 code, end to end

Usage:
  pipeline <youtube-url> [flags]

Flags:
  --config FILE          config file path (default: pipeline.yaml; missing file = defaults)
  --transcript-mode MODE auto | captions | whisper (default: auto)
  --fast                 use the fast model
  --force                re-run all stages, ignoring cached outputs
  --workers N            parallel Ollama calls
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, ytdlp.New))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, newFetcher func() *ytdlp.Fetcher) int {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", cfg.DefaultPath, "config file path")
	transcriptMode := fs.String("transcript-mode", "auto", "auto | captions | whisper")
	fast := fs.Bool("fast", false, "use the fast model")
	force := fs.Bool("force", false, "ignore cached stage outputs")
	workers := fs.Int("workers", 0, "parallel Ollama calls")

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
	switch *transcriptMode {
	case "auto", "captions", "whisper":
	default:
		_, _ = fmt.Fprintf(stderr, "pipeline: invalid --transcript-mode %q\n", *transcriptMode)
		return 2
	}
	conf, err := cfg.LoadOrDefault(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pipeline: %v\n", err)
		return 2
	}
	if *workers > 0 {
		conf.Apply(cfg.Overrides{Workers: workers})
	}
	rawURL := fs.Arg(0)
	videoID, err := ytdlp.ExtractVideoID(rawURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pipeline: %v\n", err)
		return 2
	}
	model := conf.Models.Primary
	if *fast {
		model = conf.Models.Fast
	}
	mode := ytdlp.Mode(*transcriptMode)

	r, err := runstore.New(conf.Paths.RunsDir, videoID)
	if err != nil {
		return fail(stderr, err)
	}
	if *force {
		if err := r.ResetStages(); err != nil {
			return fail(stderr, err)
		}
	}

	step := func(name string, fn func() error) error {
		_, _ = fmt.Fprintf(stderr, "==> %s\n", name)
		return fn()
	}

	if err := step("fetch", func() error {
		n, err := stages.Fetch(ctx, r, rawURL, mode, newFetcher)
		if n == -1 {
			logf(stderr, "fetch: skipped (cached)")
			return nil
		}
		if err == nil {
			logf(stderr, "fetch: %d lines", n)
		}
		return err
	}); err != nil {
		if errors.Is(err, ytdlp.ErrNoCaptions) {
			_, _ = fmt.Fprintln(stderr, "pipeline: no english captions; retry with --transcript-mode whisper (slow)")
			return 1
		}
		return fail(stderr, err)
	}

	if err := step("segment", func() error {
		n, err := stages.Segment(r, segment.Config{
			MaxTokens:  conf.Segment.MaxTokens,
			MaxSeconds: conf.Segment.MaxSeconds,
			PauseGap:   conf.Segment.PauseGap,
			Cues:       segment.DefaultCues,
		})
		if n == -1 {
			logf(stderr, "segment: skipped (cached)")
			return nil
		}
		if err == nil {
			logf(stderr, "segment: %d chunks", n)
		}
		return err
	}); err != nil {
		return fail(stderr, err)
	}

	var extractRes *extract.Result
	if err := step("extract", func() error {
		res, runErr := stages.Extract(ctx, r, extract.Config{
			Model:     model,
			Workers:   conf.Extract.Workers,
			Retries:   conf.Extract.Retries,
			NumCtx:    conf.Ollama.NumCtx,
			KeepAlive: conf.Ollama.KeepAlive.String(),
		}, conf.Ollama.URL)
		extractRes = res
		if res == nil {
			logf(stderr, "extract: skipped (cached)")
			return nil
		}
		logf(stderr, "extract: %d events (%d failed chunks)", len(res.Events), len(res.Failed))
		return runErr
	}); err != nil && !errors.Is(err, extract.ErrAllFailed) {
		return fail(stderr, err)
	}

	var outDir string
	if err := step("assemble", func() error {
		res, dir, err := stages.Assemble(r)
		outDir = dir
		if res == nil {
			logf(stderr, "assemble: skipped (cached)")
			return nil
		}
		for _, rec := range res.Records {
			if rec.Status == "skipped" {
				logf(stderr, "assemble: conflict %s %s seq %d on %s: %s",
					rec.ChunkID, rec.Op, rec.Seq, rec.File, rec.Detail)
			}
		}
		logf(stderr, "assemble: %d ops applied, %d skipped", res.Applied, res.Skipped)
		return err
	}); err != nil {
		return fail(stderr, err)
	}

	report, err := stages.Verify(ctx, r, stages.VerifyOptions{
		LLM:       false,
		Model:     conf.Models.Primary,
		BaseURL:   conf.Ollama.URL,
		KeepAlive: conf.Ollama.KeepAlive.String(),
		NumCtx:    conf.Ollama.NumCtx,
	})
	if report == nil {
		if report, err = stages.LoadReport(r); err != nil {
			return fail(stderr, err)
		}
		logf(stderr, "verify: skipped (cached)")
	} else if err != nil {
		logf(stderr, "verify: warning: %v", err)
	}
	for _, f := range report.Findings {
		logf(stderr, "verify: [%s] %s %s: %s", f.Severity, f.File, f.Check, f.Detail)
	}

	failed := 0
	if extractRes != nil {
		failed = len(extractRes.Failed)
	}

	_, _ = fmt.Fprintf(stdout, "\nvideo:   %s\noutput:  %s\nreport:  %s\nconfidence: %.2f\nfailed chunks: %d\n",
		videoID, outDir, r.Path(runstore.ReportJSON), report.Confidence, failed)

	if report.Confidence < conf.Verify.MinConfidence {
		_, _ = fmt.Fprintf(stderr, "pipeline: confidence %.2f below minimum %.2f\n",
			report.Confidence, conf.Verify.MinConfidence)
		return 1
	}
	return 0
}

func logf(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", args...)
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "pipeline: %v\n", err)
	return 1
}
