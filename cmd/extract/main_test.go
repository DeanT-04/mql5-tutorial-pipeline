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
)

func TestRunUsage(t *testing.T) {
	tests := [][]string{
		nil,
		{"--help"},
		{"/tmp/run", "extra"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, nil, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr: %s)", args, code, stderr.String())
		}
	}
}

func fixtureChunks(t *testing.T) string {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chunks := `[
		{"id":"c0001","start":0,"end":2,"text":"welcome"},
		{"id":"c0002","start":3,"end":6,"text":"let's add code"}
	]`
	if err := os.WriteFile(filepath.Join(runDir, "chunks.json"), []byte(chunks), 0o600); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func fakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
			Format   json.RawMessage            `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		isDeep := strings.Contains(string(req.Format), `"events"`)
		content := ""
		switch {
		case isDeep:
			content = `{"events":[{"seq":1,"op":"append","file":"MyEA.mq5","code":"int x = 1;"}]}`
		default:
			content = `{"chunk_id":"c0001","has_code_action":false,"confidence":0.9}`
			if strings.Contains(req.Messages[len(req.Messages)-1].Content, "c0002") {
				content = `{"chunk_id":"c0002","has_code_action":true,"confidence":0.95}`
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": content},
			"done":    true,
		})
	}))
}

func TestRunExtractEndToEnd(t *testing.T) {
	runDir := fixtureChunks(t)
	srv := fakeOllama(t)
	defer srv.Close()

	conf := filepath.Join(runDir, "..", "..", "pipeline.yaml")
	if err := os.WriteFile(conf, []byte("ollama:\n  url: "+srv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--run", runDir, "--config", conf}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 events") || !strings.Contains(stdout.String(), "1/2 chunks positive") {
		t.Errorf("stdout = %q", stdout.String())
	}
	evData, err := os.ReadFile(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl missing: %v", err)
	}
	if !strings.Contains(string(evData), `"file":"MyEA.mq5"`) {
		t.Errorf("events.jsonl = %s", evData)
	}
	if _, err := os.Stat(filepath.Join(runDir, "triage.jsonl")); err != nil {
		t.Errorf("triage.jsonl missing: %v", err)
	}
}

func TestRunExtractUpToDate(t *testing.T) {
	runDir := fixtureChunks(t)
	srv := fakeOllama(t)
	defer srv.Close()

	conf := filepath.Join(runDir, "..", "..", "pipeline.yaml")
	if err := os.WriteFile(conf, []byte("ollama:\n  url: "+srv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--run", runDir, "--config", conf}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("first run() = %d (stderr: %s)", code, stderr.String())
	}
	stdout.Reset()
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("second run() = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("second stdout = %q", stdout.String())
	}
}

func TestRunExtractDeadOllama(t *testing.T) {
	runDir := fixtureChunks(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close()

	conf := filepath.Join(runDir, "..", "..", "pipeline.yaml")
	if err := os.WriteFile(conf, []byte("ollama:\n  url: "+base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--run", runDir, "--config", conf}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "every chunk failed") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
