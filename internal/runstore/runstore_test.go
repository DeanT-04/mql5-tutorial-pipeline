package runstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewCreatesDirAndEmptyManifest(t *testing.T) {
	root := t.TempDir()
	r, err := New(root, "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if want := filepath.Join(root, "abc123"); r.Dir() != want {
		t.Errorf("Dir() = %q, want %q", r.Dir(), want)
	}
	if _, err := os.Stat(r.Dir()); err != nil {
		t.Errorf("run dir not created: %v", err)
	}
	for _, stage := range []string{StageFetch, StageSegment, StageExtract, StageAssemble, StageVerify} {
		if r.UpToDate(stage, "deadbeef") {
			t.Errorf("UpToDate(%q) = true on fresh run", stage)
		}
	}
}

func TestNewExistingRunLoadsManifest(t *testing.T) {
	root := t.TempDir()
	r, err := New(root, "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	r.SetMeta("https://youtu.be/abc123", "My Tutorial")
	if err := r.MarkDone(StageFetch, HashBytes([]byte("t"))); err != nil {
		t.Fatalf("MarkDone() error = %v", err)
	}

	r2, err := New(root, "abc123")
	if err != nil {
		t.Fatalf("New() again error = %v", err)
	}
	if !r2.UpToDate(StageFetch, HashBytes([]byte("t"))) {
		t.Error("UpToDate(fetch, same hash) = false, want true")
	}
	if r2.UpToDate(StageFetch, HashBytes([]byte("other"))) {
		t.Error("UpToDate(fetch, different hash) = true, want false")
	}
	if r2.m.URL != "https://youtu.be/abc123" || r2.m.Title != "My Tutorial" {
		t.Errorf("meta not persisted: %+v", r2.m)
	}
}

func TestManifestVideoIDMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{VideoID: "other", Stages: map[string]StageRecord{}}
	data, _ := json.Marshal(&m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "abc123"); err == nil {
		t.Fatal("New() with mismatched video_id error = nil, want error")
	}
}

func TestCorruptManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "abc123"); err == nil {
		t.Fatal("New() with corrupt manifest error = nil, want error")
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"abc123", false},
		{"dQw4w9WgXcQ", false},
		{"my-video_1", false},
		{"", true},
		{"has space", true},
		{"slash/attack", true},
		{"dot.", true},
		{"../escape", true},
		{strings.Repeat("a", 129), true},
		{strings.Repeat("a", 128), false},
	}
	for _, tt := range tests {
		err := validateID(tt.id)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
		}
	}
}

func TestMarkDoneEmptyHash(t *testing.T) {
	r, err := New(t.TempDir(), "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := r.MarkDone(StageFetch, ""); err == nil {
		t.Error("MarkDone(empty hash) error = nil, want error")
	}
}

func TestPathsAndOutDir(t *testing.T) {
	r, err := New(t.TempDir(), "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got, want := r.Path(TranscriptJSON), filepath.Join(r.Dir(), "transcript.json"); got != want {
		t.Errorf("Path(transcript.json) = %q, want %q", got, want)
	}
	if got := r.Path("../escape.json"); strings.Contains(got, "..") {
		t.Errorf("Path(../escape.json) = %q, want traversal neutralized", got)
	}
	out, err := r.OutDir()
	if err != nil {
		t.Fatalf("OutDir() error = %v", err)
	}
	if want := filepath.Join(r.Dir(), "out"); out != want {
		t.Errorf("OutDir() = %q, want %q", out, want)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("out dir not created: %v", err)
	}
}

func TestHashBytesDeterministic(t *testing.T) {
	a := HashBytes([]byte("hello"))
	b := HashBytes([]byte("hello"))
	c := HashBytes([]byte("hellp"))
	if a != b {
		t.Error("HashBytes not deterministic")
	}
	if a == c {
		t.Error("HashBytes collision for adjacent inputs")
	}
	if len(a) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(a))
	}
}

func TestHashFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	data := []byte("file content")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile() error = %v", err)
	}
	if want := HashBytes(data); got != want {
		t.Errorf("HashFile() = %q, want %q", got, want)
	}
	if _, err := HashFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("HashFile(missing) error = nil, want error")
	}
}

type binaryValue struct{}

func (binaryValue) MarshalBinary() ([]byte, error) { return []byte("binary"), nil }

func TestHashValue(t *testing.T) {
	got, err := HashValue(map[string]int{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("HashValue() error = %v", err)
	}
	again, _ := HashValue(map[string]int{"a": 1, "b": 2})
	if got != again {
		t.Error("HashValue(map) not deterministic across key orders")
	}
	bin, err := HashValue(binaryValue{})
	if err != nil {
		t.Fatalf("HashValue(BinaryMarshaler) error = %v", err)
	}
	if want := HashBytes([]byte("binary")); bin != want {
		t.Errorf("HashValue(BinaryMarshaler) = %q, want %q", bin, want)
	}
	if _, err := HashValue(func() {}); err == nil {
		t.Error("HashValue(unmarshalable) error = nil, want error")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	if err := WriteFileAtomic(path, []byte("v1")); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	if err := WriteFileAtomic(path, []byte("v2")); err != nil {
		t.Fatalf("WriteFileAtomic() overwrite error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Errorf("content = %q, want v2", data)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestReadFileCapped(t *testing.T) {
	r, err := New(t.TempDir(), "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := os.WriteFile(r.Path(ChunksJSON), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := r.ReadFileCapped(ChunksJSON, 10)
	if err != nil {
		t.Fatalf("ReadFileCapped() error = %v", err)
	}
	if string(data) != "12345" {
		t.Errorf("data = %q", data)
	}
	if _, err := r.ReadFileCapped(ChunksJSON, 3); err == nil {
		t.Error("ReadFileCapped(size 3) error = nil, want error")
	}
	if _, err := r.ReadFileCapped("missing.json", 10); err == nil {
		t.Error("ReadFileCapped(missing) error = nil, want error")
	}
}

func TestManifestRoundTripFields(t *testing.T) {
	root := t.TempDir()
	r, err := New(root, "abc123")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	r.SetMeta("url", "title")
	if err := r.MarkDone(StageSegment, "cafe"); err != nil {
		t.Fatalf("MarkDone() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "abc123", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v", err)
	}
	rec, ok := m.Stages[StageSegment]
	if !ok {
		t.Fatal("segment stage missing from manifest")
	}
	if rec.InputHash != "cafe" || rec.Status != "done" {
		t.Errorf("stage record = %+v", rec)
	}
	if rec.Finished.IsZero() || time.Until(rec.Finished) > time.Minute {
		t.Errorf("finished timestamp unreasonable: %v", rec.Finished)
	}
}
