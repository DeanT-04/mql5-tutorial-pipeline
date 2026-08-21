// Command fetch downloads the transcript for one YouTube video into its run
// directory (transcript.json), skipping work when inputs are unchanged.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

const usage = `fetch — download transcript for one YouTube video

Usage:
  fetch <youtube-url> [flags]

Flags:
  --runs-dir DIR         parent directory for runs (default: runs)
  --transcript-mode MODE auto | captions | whisper (default: auto)
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, ytdlp.New))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, newFetcher func() *ytdlp.Fetcher) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runsDir := fs.String("runs-dir", "runs", "parent directory for runs")
	modeStr := fs.String("transcript-mode", "auto", "auto | captions | whisper")

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
	mode := ytdlp.Mode(*modeStr)
	switch mode {
	case ytdlp.ModeAuto, ytdlp.ModeCaptions, ytdlp.ModeWhisper:
	default:
		_, _ = fmt.Fprintf(stderr, "fetch: invalid --transcript-mode %q\n", *modeStr)
		return 2
	}
	rawURL := fs.Arg(0)
	videoID, err := ytdlp.ExtractVideoID(rawURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fetch: %v\n", err)
		return 2
	}

	r, err := runstore.New(*runsDir, videoID)
	if err != nil {
		return fail(stderr, err)
	}
	inputHash, err := runstore.HashValue(struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}{rawURL, string(mode)})
	if err != nil {
		return fail(stderr, err)
	}
	if r.UpToDate(runstore.StageFetch, inputHash) {
		_, _ = fmt.Fprintf(stdout, "fetch: up to date (%s)\n", r.Path(runstore.TranscriptJSON))
		return 0
	}

	fetcher := newFetcher()
	res, err := fetcher.Fetch(ctx, rawURL, r.Dir(), mode)
	if err != nil {
		if errors.Is(err, ytdlp.ErrNoCaptions) {
			_, _ = fmt.Fprintln(stderr, "fetch: no english captions; retry with --transcript-mode whisper (slow)")
			return 1
		}
		return fail(stderr, err)
	}
	data, err := transcript.Marshal(res.Lines)
	if err != nil {
		return fail(stderr, err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.TranscriptJSON), data); err != nil {
		return fail(stderr, err)
	}
	title, _ := fetcher.Title(ctx, rawURL)
	r.SetMeta(rawURL, title)
	if err := r.MarkDone(runstore.StageFetch, inputHash); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "fetch: %d lines via %s -> %s\n", len(res.Lines), res.Source, r.Path(runstore.TranscriptJSON))
	return 0
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "fetch: %v\n", err)
	return 1
}
