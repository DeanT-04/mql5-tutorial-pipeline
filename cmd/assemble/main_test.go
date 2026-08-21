package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/assemble"
)

func fixtureEvents(t *testing.T) string {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"chunk_id":"c0001","seq":1,"op":"create","file":"MyEA.mq5","code":"#property strict"}`,
		`{"chunk_id":"c0002","seq":1,"op":"append","file":"MyEA.mq5","code":"input double LotSize = 0.10;"}`,
		`{"chunk_id":"c0003","seq":1,"op":"replace","file":"MyEA.mq5","anchor":"input double LotSize = 0.10;","code":"input double LotSize = 0.50;"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func TestRunUsage(t *testing.T) {
	tests := [][]string{
		nil,
		{"--help"},
		{"/tmp/run", "extra"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if code := run(args, nil, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr: %s)", args, code, stderr.String())
		}
	}
}

func TestRunAssemblesFiles(t *testing.T) {
	runDir := fixtureEvents(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d (stderr: %s)", code, stderr.String())
	}
	out := filepath.Join(runDir, "out", "MyEA.mq5")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	want := "#property strict\ninput double LotSize = 0.50;\n"
	if string(data) != want {
		t.Errorf("out = %q, want %q", data, want)
	}
	report, err := os.ReadFile(filepath.Join(runDir, "assembly-report.json"))
	if err != nil {
		t.Fatalf("report missing: %v", err)
	}
	var rep struct {
		Applied int `json:"applied"`
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal(report, &rep); err != nil {
		t.Fatalf("report invalid: %v", err)
	}
	if rep.Applied != 3 || rep.Skipped != 0 {
		t.Errorf("report = %+v", rep)
	}
	_ = assemble.Result{}
}

func TestRunSkipsWhenUpToDate(t *testing.T) {
	runDir := fixtureEvents(t)
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

func TestRunConflictsReported(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"chunk_id":"c0001","seq":1,"op":"append","file":"Ghost.mq5","code":"int x;"}`,
		`{"chunk_id":"c0002","seq":1,"op":"create","file":"A.mq5","code":"int x;"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 skipped") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "conflict c0001") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunMissingEvents(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--run", runDir}, nil, &stderr); code != 1 {
		t.Errorf("run(no events) = %d, want 1 (stderr: %s)", code, stderr.String())
	}
}
