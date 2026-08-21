package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

const testURL = "https://youtu.be/dQw4w9WgXcQ"

// fakeRunner stands in for all external binaries; no processes, no network.
type fakeRunner struct {
	onRun func(name string, args []string) ([]byte, error)
	calls []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return f.onRun(name, args)
}

func stubFetcher(onRun func(name string, args []string) ([]byte, error)) func() *ytdlp.Fetcher {
	return func() *ytdlp.Fetcher {
		return &ytdlp.Fetcher{Runner: &fakeRunner{onRun: onRun}, PythonBin: "python", WhisperModel: "small"}
	}
}

// writeJSON3 simulates yt-dlp having written a subtitle file into the run dir.
// The run directory already exists by the time Fetch runs (runstore.New).
func writeJSON3(t *testing.T, runDir string) {
	t.Helper()
	body := `{"events":[{"tStartMs":0,"dDurationMs":2000,"segs":[{"utf8":"hello from fake"}]}]}`
	path := filepath.Join(runDir, "dQw4w9WgXcQ.en.json3")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunUsage(t *testing.T) {
	tests := [][]string{
		nil,
		{"--help"},
		{testURL, "extra"},
		{"--transcript-mode", "bogus", testURL},
		{"https://example.com/x"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, nil, &stderr, nil); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr: %s)", args, code, stderr.String())
		}
	}
}

func TestRunFetchEndToEnd(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	onRun := func(name string, args []string) ([]byte, error) {
		switch name {
		case "yt-dlp":
			for _, a := range args {
				if a == "--print" {
					return []byte("Fake Tutorial Title\n"), nil
				}
			}
			writeJSON3(t, filepath.Join(runsDir, "dQw4w9WgXcQ"))
			return []byte{}, nil
		default:
			t.Fatalf("unexpected binary %q (%v)", name, args)
			return nil, nil
		}
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--runs-dir", runsDir, testURL}, &stdout, &stderr, stubFetcher(onRun))
	if code != 0 {
		t.Fatalf("run() = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 lines") {
		t.Errorf("stdout = %q", stdout.String())
	}

	r, err := runstore.New(runsDir, "dQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.ReadFileCapped(runstore.TranscriptJSON, 1<<20)
	if err != nil {
		t.Fatalf("transcript.json missing: %v", err)
	}
	if !strings.Contains(string(data), "hello from fake") {
		t.Errorf("transcript content = %q", data)
	}
}

func TestRunSkipsWhenUpToDate(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	calls := 0
	onRun := func(name string, args []string) ([]byte, error) {
		if name == "yt-dlp" && calls%2 == 0 {
			writeJSON3(t, filepath.Join(runsDir, "dQw4w9WgXcQ"))
		}
		calls++
		return []byte{}, nil
	}
	args := []string{"--runs-dir", runsDir, "--transcript-mode", "captions", testURL}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr, stubFetcher(onRun)); code != 0 {
		t.Fatalf("first run() = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, &stdout, &stderr, stubFetcher(onRun)); code != 0 {
		t.Fatalf("second run() = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("second stdout = %q, want up-to-date notice", stdout.String())
	}
	if calls%2 != 0 {
		t.Errorf("call count %d suggests second run re-fetched", calls)
	}
}

func TestRunNoCaptionsHint(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	onRun := func(name string, args []string) ([]byte, error) { return []byte{}, nil }
	var stdout, stderr bytes.Buffer
	code := run(context.Background(),
		[]string{"--runs-dir", runsDir, "--transcript-mode", "captions", testURL},
		&stdout, &stderr, stubFetcher(onRun))
	if code != 1 {
		t.Fatalf("run() = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no english captions") ||
		!strings.Contains(stderr.String(), "--transcript-mode whisper") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunRuntimeError(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	onRun := func(name string, args []string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(),
		[]string{"--runs-dir", runsDir, "--transcript-mode", "whisper", testURL},
		&stdout, &stderr, stubFetcher(onRun))
	if code != 1 {
		t.Fatalf("run() = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
