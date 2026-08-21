package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

const testURL = "https://youtu.be/dQw4w9WgXcQ"

// fakeFetcher returns a Fetcher whose Runner simulates yt-dlp writing captions.
func fakeFetcher(runDir func() string) func() *ytdlp.Fetcher {
	return func() *ytdlp.Fetcher {
		return &ytdlp.Fetcher{Runner: &fakeRunner{dir: runDir}, PythonBin: "python", WhisperModel: "small"}
	}
}

type fakeRunner struct {
	dir func() string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "yt-dlp" {
		for _, a := range args {
			if a == "--print" {
				return []byte("Fake Title\n"), nil
			}
		}
		body := `{"events":[
			{"tStartMs":0,"dDurationMs":2000,"segs":[{"utf8":"welcome to the tutorial"}]},
			{"tStartMs":5000,"dDurationMs":2000,"segs":[{"utf8":"let's add an input variable"}]}
		]}`
		path := filepath.Join(f.dir(), "dQw4w9WgXcQ.en.json3")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return nil, err
		}
	}
	return []byte{}, nil
}

func fakeOllama() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
			Format   json.RawMessage            `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := `{"chunk_id":"c0001","has_code_action":false,"confidence":0.9}`
		if strings.Contains(string(req.Format), `"events"`) {
			content = `{"events":[{"seq":1,"op":"create","file":"MyEA.mq5","code":"#property strict"}]}`
		} else if strings.Contains(req.Messages[len(req.Messages)-1].Content, "let's add") {
			content = `{"chunk_id":"c0002","has_code_action":true,"confidence":0.95}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": content},
			"done":    true,
		})
	}))
}

type env struct {
	root     string
	confPath string
	runDir   string
}

func newEnv(t *testing.T) (*env, func() *ytdlp.Fetcher) {
	t.Helper()
	root := t.TempDir()
	srv := fakeOllama()
	t.Cleanup(srv.Close)

	confPath := filepath.Join(root, "pipeline.yaml")
	conf := "ollama:\n  url: " + srv.URL + "\npaths:\n  runs_dir: " + filepath.Join(root, "runs") + "\n"
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &env{
		root:     root,
		confPath: confPath,
		runDir:   filepath.Join(root, "runs", "dQw4w9WgXcQ"),
	}
	return e, fakeFetcher(func() string { return e.runDir })
}

func TestPipelineEndToEnd(t *testing.T) {
	e, ff := newEnv(t)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--config", e.confPath, testURL}, &stdout, &stderr, ff)
	if code != 0 {
		t.Fatalf("run() = %d (stderr: %s)", code, stderr.String())
	}
	out, err := os.ReadFile(filepath.Join(e.runDir, "out", "MyEA.mq5"))
	if err != nil {
		t.Fatalf("assembled file missing: %v", err)
	}
	if string(out) != "#property strict\n" {
		t.Errorf("out = %q", out)
	}
	for _, artifact := range []string{"transcript.json", "chunks.json", "triage.jsonl", "events.jsonl", "assembly-report.json", "report.json", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(e.runDir, artifact)); err != nil {
			t.Errorf("artifact %s missing", artifact)
		}
	}
	if !strings.Contains(stdout.String(), "confidence") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestPipelineResumeRedoesOnlyDeletedStage(t *testing.T) {
	e, ff := newEnv(t)
	args := []string{"--config", e.confPath, testURL}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr, ff); code != 0 {
		t.Fatalf("first run() = %d (stderr: %s)", code, stderr.String())
	}

	eventsPath := filepath.Join(e.runDir, "events.jsonl")
	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, &stdout, &stderr, ff); code != 0 {
		t.Fatalf("second run() = %d (stderr: %s)", code, stderr.String())
	}
	s := stderr.String()
	if !strings.Contains(s, "fetch: skipped") || !strings.Contains(s, "segment: skipped") {
		t.Errorf("fetch/segment should be cached, stderr = %s", s)
	}
	if !strings.Contains(s, "==> extract") && !strings.Contains(s, "extract:") {
		t.Errorf("extract should re-run, stderr = %s", s)
	}
	if _, err := os.Stat(eventsPath); err != nil {
		t.Error("events.jsonl not regenerated")
	}
}

func TestPipelineUsage(t *testing.T) {
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
