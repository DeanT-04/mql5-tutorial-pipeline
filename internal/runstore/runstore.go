// Package runstore manages the per-video run directory layout and the
// content-hash resume logic described in spec.md §3: every stage records the
// SHA-256 of its inputs in manifest.json; a stage whose recorded input hash
// matches the current one is skipped on re-run.
package runstore

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxManifestBytes = 4 << 20
	maxArtifactBytes = 512 << 20
	dirPerm          = 0o755
	filePerm         = 0o600
)

// Stage names used by the pipeline.
const (
	StageFetch    = "fetch"
	StageSegment  = "segment"
	StageExtract  = "extract"
	StageAssemble = "assemble"
	StageVerify   = "verify"
)

// Artifact file names within a run directory.
const (
	TranscriptJSON     = "transcript.json"
	ChunksJSON         = "chunks.json"
	TriageJSONL        = "triage.jsonl"
	EventsJSONL        = "events.jsonl"
	AssemblyReportJSON = "assembly-report.json"
	ReportJSON         = "report.json"
)

type StageRecord struct {
	InputHash string    `json:"input_hash"`
	Status    string    `json:"status"`
	Finished  time.Time `json:"finished"`
}

type Manifest struct {
	VideoID string                 `json:"video_id"`
	URL     string                 `json:"url,omitempty"`
	Title   string                 `json:"title,omitempty"`
	Stages  map[string]StageRecord `json:"stages"`
}

// Run is one video's directory under runsDir.
type Run struct {
	dir string
	m   Manifest
}

// New creates (if needed) runs/<videoID>/ and loads its manifest if present.
func New(runsDir, videoID string) (*Run, error) {
	if videoID == "" {
		return nil, fmt.Errorf("runstore: empty video id")
	}
	if err := validateID(videoID); err != nil {
		return nil, fmt.Errorf("runstore: %w", err)
	}
	r := &Run{dir: filepath.Join(runsDir, videoID)}
	if err := os.MkdirAll(r.dir, dirPerm); err != nil {
		return nil, fmt.Errorf("runstore: create %s: %w", r.dir, err)
	}
	r.m = Manifest{VideoID: videoID, Stages: map[string]StageRecord{}}
	path := r.manifestPath()
	data, err := readFileCapped(path, maxManifestBytes)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runstore: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &r.m); err != nil {
		return nil, fmt.Errorf("runstore: parse %s: %w", path, err)
	}
	if r.m.VideoID != videoID {
		return nil, fmt.Errorf("runstore: %s: manifest video_id %q does not match directory %q", path, r.m.VideoID, videoID)
	}
	if r.m.Stages == nil {
		r.m.Stages = map[string]StageRecord{}
	}
	return r, nil
}

func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("empty video id")
	}
	if len(id) > 128 {
		return fmt.Errorf("video id %q too long", id)
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return fmt.Errorf("video id %q contains invalid character %q", id, c)
		}
	}
	return nil
}

func (r *Run) Dir() string { return r.dir }

// Path returns the path of an artifact inside the run directory. The name must
// be a bare file name; subdirectories are addressed by joining Dir() manually.
func (r *Run) Path(name string) string {
	return filepath.Join(r.dir, filepath.Base(name))
}

// OutDir returns the assembled-output subdirectory, creating it if needed.
func (r *Run) OutDir() (string, error) {
	out := filepath.Join(r.dir, "out")
	if err := os.MkdirAll(out, dirPerm); err != nil {
		return "", fmt.Errorf("runstore: create %s: %w", out, err)
	}
	return out, nil
}

func (r *Run) manifestPath() string { return r.Path("manifest.json") }

// SetMeta records URL/title metadata in the in-memory manifest.
func (r *Run) SetMeta(url, title string) {
	r.m.URL = url
	r.m.Title = title
}

// UpToDate reports whether stage previously completed with exactly inputHash,
// meaning it can be skipped.
func (r *Run) UpToDate(stage, inputHash string) bool {
	rec, ok := r.m.Stages[stage]
	return ok && rec.Status == "done" && rec.InputHash == inputHash && inputHash != ""
}

// MarkDone records stage as completed for inputHash and persists the manifest.
func (r *Run) MarkDone(stage, inputHash string) error {
	if inputHash == "" {
		return fmt.Errorf("runstore: empty input hash for stage %q", stage)
	}
	if r.m.Stages == nil {
		r.m.Stages = map[string]StageRecord{}
	}
	r.m.Stages[stage] = StageRecord{
		InputHash: inputHash,
		Status:    "done",
		Finished:  time.Now().UTC(),
	}
	return r.save()
}

// save writes the manifest atomically: temp file in the same directory, then rename.
func (r *Run) save() error {
	data, err := json.MarshalIndent(&r.m, "", "  ")
	if err != nil {
		return fmt.Errorf("runstore: encode manifest: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(r.manifestPath(), data); err != nil {
		return fmt.Errorf("runstore: write manifest: %w", err)
	}
	return nil
}

// HashBytes returns the hex-encoded SHA-256 of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashFile returns the hex-encoded SHA-256 of the file at path.
func HashFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- paths come from the run directory by design
	if err != nil {
		return "", fmt.Errorf("runstore: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("runstore: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashValue returns the hex-encoded SHA-256 of the canonical JSON encoding of v.
// Values that do not implement encoding.BinaryMarshaler are encoded with
// encoding/json, which is deterministic for maps (sorted keys).
func HashValue(v any) (string, error) {
	var data []byte
	var err error
	if bm, ok := v.(encoding.BinaryMarshaler); ok {
		data, err = bm.MarshalBinary()
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return "", fmt.Errorf("runstore: hash value: %w", err)
	}
	return HashBytes(data), nil
}

// ReadFileCapped reads name from the run directory, refusing files larger than maxBytes.
func (r *Run) ReadFileCapped(name string, maxBytes int64) ([]byte, error) {
	return readFileCapped(r.Path(name), maxBytes)
}

func readFileCapped(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- paths come from the run directory by design
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return data, nil
}

// WriteFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so readers never observe partial content.
func WriteFileAtomic(path string, data []byte) error {
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}
