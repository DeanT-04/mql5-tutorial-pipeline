package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureOut(t *testing.T, src string) string {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	outDir := filepath.Join(runDir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "MyEA.mq5"), []byte(src), 0o600); err != nil {
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
		if code := run(context.Background(), args, nil, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr: %s)", args, code, stderr.String())
		}
	}
}

const goodEA = `#property strict
int OnInit() { return INIT_SUCCEEDED; }
void OnTick() { Print("tick"); }
`

func TestRunVerifyGoodFile(t *testing.T) {
	runDir := fixtureOut(t, goodEA)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--run", runDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "confidence 1.00") {
		t.Errorf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(runDir, "report.json"))
	if err != nil {
		t.Fatalf("report missing: %v", err)
	}
	var rep struct {
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(data, &rep); err != nil || rep.Confidence != 1.0 {
		t.Errorf("report confidence = %f (err: %v)", rep.Confidence, err)
	}
}

func TestRunVerifyBadFileFailsThreshold(t *testing.T) {
	runDir := fixtureOut(t, "void OnTick() { Print(1); ") // unbalanced + no strict + no OnInit
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--run", runDir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "below minimum") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunVerifyUpToDate(t *testing.T) {
	runDir := fixtureOut(t, goodEA)
	args := []string{"--run", runDir}

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

func TestRunNoOutputFiles(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"--run", runDir}, nil, &stderr); code != 1 {
		t.Errorf("run(no out files) = %d, want 1 (stderr: %s)", code, stderr.String())
	}
}
