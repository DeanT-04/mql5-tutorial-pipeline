package stages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/extract"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

const testID = "dQw4w9WgXcQ"
const testURL = "https://youtu.be/" + testID

// captionRunner simulates yt-dlp: subtitle requests write a small json3 file
// into the run dir, `--print title` reports a fixed title.
type captionRunner struct {
	dir func() string
}

func (f *captionRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "yt-dlp" {
		return nil, errors.New("unexpected binary " + name)
	}
	for _, a := range args {
		if a == "--print" {
			return []byte("Fake Title\n"), nil
		}
	}
	body := `{"events":[
		{"tStartMs":0,"dDurationMs":2000,"segs":[{"utf8":"welcome to the tutorial"}]},
		{"tStartMs":5000,"dDurationMs":2000,"segs":[{"utf8":"let's add an input variable"}]}
	]}`
	path := filepath.Join(f.dir(), testID+".en.json3")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return nil, err
	}
	return []byte{}, nil
}

// silentRunner never produces captions.
type silentRunner struct{}

func (silentRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func newRun(t *testing.T) *runstore.Run {
	t.Helper()
	r, err := runstore.New(filepath.Join(t.TempDir(), "runs"), testID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// fakeOllama answers triage and deep requests against a scripted fixture.
func fakeOllama(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
			Format   json.RawMessage            `json:"format"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := `{"chunk_id":"c0002","has_code_action":true,"confidence":0.95}`
		if strings.Contains(string(body.Format), `"events"`) {
			content = `{"events":[{"seq":1,"op":"create","file":"MyEA.mq5","code":"#property strict"}]}`
		} else if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "let's add") {
			content = `{"chunk_id":"c0001","has_code_action":false,"confidence":0.9}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": content},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// seedTranscript writes a two-line transcript into the run dir.
func seedTranscript(t *testing.T, r *runstore.Run) {
	t.Helper()
	data, err := transcript.Marshal([]transcript.Line{
		{Start: 0, End: 2, Text: "welcome to the tutorial"},
		{Start: 5, End: 7, Text: "let's add an input variable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.TranscriptJSON), data); err != nil {
		t.Fatal(err)
	}
}

var segCfg = segment.Config{MaxTokens: 350, MaxSeconds: 45, PauseGap: 1500000000, Cues: segment.DefaultCues}

func extCfg() extract.Config {
	return extract.Config{Model: "test-model", Workers: 1, Retries: 0, NumCtx: 512, KeepAlive: "1m"}
}

func TestFetchRunsThenSkips(t *testing.T) {
	r := newRun(t)
	runDir := func() string { return r.Dir() }
	newFetcher := func() *ytdlp.Fetcher {
		return &ytdlp.Fetcher{Runner: &captionRunner{dir: runDir}, PythonBin: "python", WhisperModel: "small"}
	}
	n, err := Fetch(context.Background(), r, testURL, ytdlp.ModeCaptions, newFetcher)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if n != 2 {
		t.Errorf("Fetch() lines = %d, want 2", n)
	}
	if _, err := os.Stat(r.Path(runstore.TranscriptJSON)); err != nil {
		t.Errorf("transcript.json missing: %v", err)
	}
	n, err = Fetch(context.Background(), r, testURL, ytdlp.ModeCaptions, newFetcher)
	if err != nil || n != -1 {
		t.Errorf("cached Fetch() = %d, %v; want -1, nil", n, err)
	}
}

func TestFetchNoCaptions(t *testing.T) {
	r := newRun(t)
	newFetcher := func() *ytdlp.Fetcher {
		return &ytdlp.Fetcher{Runner: silentRunner{}, PythonBin: "python", WhisperModel: "small"}
	}
	_, err := Fetch(context.Background(), r, testURL, ytdlp.ModeCaptions, newFetcher)
	if !errors.Is(err, ytdlp.ErrNoCaptions) {
		t.Errorf("Fetch() error = %v, want ErrNoCaptions", err)
	}
}

func TestSegmentRunsThenSkips(t *testing.T) {
	r := newRun(t)
	if _, err := Segment(r, segCfg); err == nil {
		t.Error("Segment() without transcript error = nil, want error")
	}
	seedTranscript(t, r)
	n, err := Segment(r, segCfg)
	if err != nil {
		t.Fatalf("Segment() error = %v", err)
	}
	if n != 2 {
		t.Errorf("Segment() chunks = %d, want 2", n)
	}
	n, err = Segment(r, segCfg)
	if err != nil || n != -1 {
		t.Errorf("cached Segment() = %d, %v; want -1, nil", n, err)
	}
}

func TestExtractRunsThenSkips(t *testing.T) {
	r := newRun(t)
	seedTranscript(t, r)
	if _, err := Segment(r, segCfg); err != nil {
		t.Fatal(err)
	}
	url := fakeOllama(t)
	res, err := Extract(context.Background(), r, extCfg(), url)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Op != events.OpCreate {
		t.Errorf("events = %+v, want one create event", res.Events)
	}
	if _, err := os.Stat(r.Path(runstore.EventsJSONL)); err != nil {
		t.Errorf("events.jsonl missing: %v", err)
	}
	if _, err := os.Stat(r.Path(runstore.TriageJSONL)); err != nil {
		t.Errorf("triage.jsonl missing: %v", err)
	}
	res, err = Extract(context.Background(), r, extCfg(), url)
	if err != nil || res != nil {
		t.Errorf("cached Extract() = %+v, %v; want nil, nil", res, err)
	}
}

func TestAssembleRunsThenSkips(t *testing.T) {
	r := newRun(t)
	var buf strings.Builder
	if err := events.AppendJSONL(&buf, events.Event{
		ChunkID: "c0001", Seq: 1, Op: events.OpCreate, File: "MyEA.mq5", Code: "#property strict",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.EventsJSONL), []byte(buf.String())); err != nil {
		t.Fatal(err)
	}
	res, outDir, err := Assemble(r)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if res.Applied != 1 {
		t.Errorf("applied = %d, want 1", res.Applied)
	}
	out, err := os.ReadFile(filepath.Join(outDir, "MyEA.mq5"))
	if err != nil || string(out) != "#property strict\n" {
		t.Errorf("out = %q, %v; want %q", out, err, "#property strict\n")
	}
	if _, err := os.Stat(r.Path(runstore.AssemblyReportJSON)); err != nil {
		t.Errorf("assembly-report.json missing: %v", err)
	}
	res, _, err = Assemble(r)
	if err != nil || res != nil {
		t.Errorf("cached Assemble() = %+v, %v; want nil, nil", res, err)
	}
}

func TestVerifyRunsThenSkips(t *testing.T) {
	r := newRun(t)
	var buf strings.Builder
	if err := events.AppendJSONL(&buf, events.Event{
		ChunkID: "c0001", Seq: 1, Op: events.OpCreate, File: "MyEA.mq5", Code: "#property strict",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.EventsJSONL), []byte(buf.String())); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Assemble(r); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(context.Background(), r, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(rep.Files) != 1 || rep.Files[0].ProgramType != "unknown" {
		t.Errorf("report files = %+v", rep.Files)
	}
	cachedRep, err := Verify(context.Background(), r, VerifyOptions{})
	if err != nil || cachedRep != nil {
		t.Errorf("cached Verify() = %+v, %v; want nil, nil", cachedRep, err)
	}
	loaded, err := LoadReport(r)
	if err != nil {
		t.Fatalf("LoadReport() error = %v", err)
	}
	if loaded.Confidence != rep.Confidence {
		t.Errorf("loaded confidence = %f, want %f", loaded.Confidence, rep.Confidence)
	}
}

func TestReadOutDirMissing(t *testing.T) {
	files, err := ReadOutDir(filepath.Join(t.TempDir(), "out"))
	if err != nil || len(files) != 0 {
		t.Errorf("ReadOutDir(missing) = %v, %v; want empty, nil", files, err)
	}
}
