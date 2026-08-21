package cfg

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v, want nil", err)
	}
	want := Config{
		Models:  Models{Primary: "qwen2.5-coder:3b-instruct", Fast: "qwen2.5-coder:1.5b"},
		Ollama:  Ollama{URL: "http://localhost:11434", NumCtx: 4096, KeepAlive: 30 * time.Minute},
		Segment: Segment{MaxTokens: 350, MaxSeconds: 45, PauseGap: 1500 * time.Millisecond},
		Extract: Extract{Workers: 2, Retries: 2},
		Verify:  Verify{MinConfidence: 0.6},
		Paths:   Paths{RunsDir: "runs"},
	}
	if c == nil {
		t.Fatal("Load(\"\") = nil, want non-nil")
	}
	if *c != want {
		t.Errorf("defaults mismatch:\n got %+v\nwant %+v", *c, want)
	}
}

func TestLoadFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, *Config)
		wantErr bool
	}{
		{
			name: "full override",
			content: `models:
  primary: other-model:7b
  fast: tiny-model:0.5b
ollama:
  url: http://127.0.0.1:11434
  num_ctx: 2048
  keep_alive: 5m
segment:
  max_tokens: 200
  max_seconds: 30
  pause_gap: 2s
extract:
  workers: 4
  retries: 1
verify:
  min_confidence: 0.75
paths:
  runs_dir: data/runs
`,
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.Models.Primary != "other-model:7b" || c.Models.Fast != "tiny-model:0.5b" {
					t.Errorf("models = %+v", c.Models)
				}
				if c.Ollama.URL != "http://127.0.0.1:11434" || c.Ollama.NumCtx != 2048 || c.Ollama.KeepAlive != 5*time.Minute {
					t.Errorf("ollama = %+v", c.Ollama)
				}
				if c.Segment.MaxTokens != 200 || c.Segment.MaxSeconds != 30 || c.Segment.PauseGap != 2*time.Second {
					t.Errorf("segment = %+v", c.Segment)
				}
				if c.Extract.Workers != 4 || c.Extract.Retries != 1 {
					t.Errorf("extract = %+v", c.Extract)
				}
				if c.Verify.MinConfidence != 0.75 {
					t.Errorf("min_confidence = %f", c.Verify.MinConfidence)
				}
				if c.Paths.RunsDir != "data/runs" {
					t.Errorf("runs_dir = %q", c.Paths.RunsDir)
				}
			},
		},
		{
			name: "partial override keeps defaults",
			content: `extract:
  workers: 3
`,
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.Extract.Workers != 3 {
					t.Errorf("workers = %d, want 3", c.Extract.Workers)
				}
				if c.Extract.Retries != 2 {
					t.Errorf("retries = %d, want default 2", c.Extract.Retries)
				}
				if c.Models.Primary != "qwen2.5-coder:3b-instruct" {
					t.Errorf("primary = %q, want default", c.Models.Primary)
				}
			},
		},
		{
			name: "comments and blank lines ignored",
			content: `# leading comment

extract:
  workers: 3 # inline comment
`,
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.Extract.Workers != 3 {
					t.Errorf("workers = %d, want 3", c.Extract.Workers)
				}
			},
		},
		{
			name:    "unknown section",
			content: "bogus:\n  x: 1\n",
			wantErr: true,
		},
		{
			name:    "unknown key",
			content: "extract:\n  workerz: 3\n",
			wantErr: true,
		},
		{
			name:    "duplicate key",
			content: "extract:\n  workers: 2\n  workers: 3\n",
			wantErr: true,
		},
		{
			name:    "duplicate section",
			content: "extract:\n  workers: 2\nextract:\n  workers: 3\n",
			wantErr: true,
		},
		{
			name:    "key before section",
			content: "workers: 3\n",
			wantErr: true,
		},
		{
			name:    "line without colon",
			content: "extract\n",
			wantErr: true,
		},
		{
			name:    "bad integer",
			content: "extract:\n  workers: two\n",
			wantErr: true,
		},
		{
			name:    "bad duration",
			content: "ollama:\n  keep_alive: soon\n",
			wantErr: true,
		},
		{
			name:    "out of range workers",
			content: "extract:\n  workers: 99\n",
			wantErr: true,
		},
		{
			name:    "out of range min_confidence",
			content: "verify:\n  min_confidence: 1.5\n",
			wantErr: true,
		},
		{
			name:    "bad url scheme",
			content: "ollama:\n  url: ftp://localhost\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pipeline.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			c, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			tt.check(t, c)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load(missing) error = nil, want error")
	}
}

func TestApplyOverrides(t *testing.T) {
	c := Default()
	workers := 5
	conf := 0.9
	dir := "elsewhere"
	c.Apply(Overrides{})
	if c.Extract.Workers != 2 || c.Verify.MinConfidence != 0.6 || c.Paths.RunsDir != "runs" {
		t.Fatalf("empty overrides mutated config: %+v", c)
	}
	c.Apply(Overrides{Workers: &workers, MinConfidence: &conf, RunsDir: &dir})
	if c.Extract.Workers != 5 {
		t.Errorf("workers = %d, want 5", c.Extract.Workers)
	}
	if c.Verify.MinConfidence != 0.9 {
		t.Errorf("min_confidence = %f, want 0.9", c.Verify.MinConfidence)
	}
	if c.Paths.RunsDir != "elsewhere" {
		t.Errorf("runs_dir = %q, want elsewhere", c.Paths.RunsDir)
	}
}
