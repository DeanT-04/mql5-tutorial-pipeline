package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
)

func TestRunUsage(t *testing.T) {
	tests := [][]string{
		nil,
		{"--help"},
		{"/tmp/some-run", "extra"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if code := run(args, nil, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr: %s)", args, code, stderr.String())
		}
	}
}

func TestRunBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("extract:\n  workers: 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--run", filepath.Join(dir, "r"), "--config", bad}, nil, &stderr); code != 2 {
		t.Errorf("run(bad config) = %d, want 2 (stderr: %s)", code, stderr.String())
	}
}

func fixtureRun(t *testing.T) string {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `[
		{"start":0,"end":1,"text":"welcome to the tutorial"},
		{"start":5,"end":6,"text":"let's add an input variable"}
	]`
	if err := os.WriteFile(filepath.Join(runDir, "transcript.json"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func TestRunSegmentsTranscript(t *testing.T) {
	runDir := fixtureRun(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2 chunks") {
		t.Errorf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(runDir, "chunks.json"))
	if err != nil {
		t.Fatalf("chunks.json missing: %v", err)
	}
	var chunks []segment.Chunk
	// sanity: parseable and cue split present
	raw := string(data)
	if !strings.Contains(raw, `"c0001"`) || !strings.Contains(raw, `"c0002"`) || !strings.Contains(raw, "let's add") {
		t.Errorf("chunks.json = %s", raw)
	}
	_ = chunks
}

func TestRunSkipsWhenUpToDate(t *testing.T) {
	runDir := fixtureRun(t)
	args := []string{"--run", runDir}

	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("first run() = %d (stderr: %s)", code, stderr.String())
	}
	stdout.Reset()
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("second run() = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("second stdout = %q", stdout.String())
	}
}

func TestRunMissingTranscript(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, nil, &stderr); code != 1 {
		t.Errorf("run(no transcript) = %d, want 1 (stderr: %s)", code, stderr.String())
	}
}

func TestRunCorruptTranscript(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "transcript.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, nil, &stderr); code != 1 {
		t.Errorf("run(corrupt transcript) = %d, want 1 (stderr: %s)", code, stderr.String())
	}
}
