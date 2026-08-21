package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/cfg"
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
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "pipeline.yaml", "config file path")
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

	conf, err := loadConfig(*configPath, *workers)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pipeline: %v\n", err)
		return 2
	}

	_ = conf
	_ = *fast
	_ = *force

	_, _ = fmt.Fprintf(stderr, "pipeline: not implemented yet (url=%s)\n", fs.Arg(0))
	return 1
}

// loadConfig loads the config file; a missing default pipeline.yaml falls back
// to built-in defaults, but an explicitly named missing file is a usage error.
func loadConfig(path string, workers int) (*cfg.Config, error) {
	_, statErr := os.Stat(path)
	if statErr != nil && path != "pipeline.yaml" {
		return nil, fmt.Errorf("open %s: %w", path, statErr)
	}
	if statErr != nil {
		conf := cfg.Default()
		if workers > 0 {
			conf.Apply(cfg.Overrides{Workers: &workers})
		}
		return conf, nil
	}
	conf, err := cfg.Load(path)
	if err != nil {
		return nil, err
	}
	if workers > 0 {
		conf.Apply(cfg.Overrides{Workers: &workers})
	}
	return conf, nil
}
