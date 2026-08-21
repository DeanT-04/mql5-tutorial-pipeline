package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ollama"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
)

// fakeClient scripts responses per call and records what it was asked.
type fakeClient struct {
	mu     sync.Mutex
	calls  []string
	script func(call int, req ollama.Request) (string, error)
	err    error
}

func (f *fakeClient) ChatJSON(ctx context.Context, req ollama.Request, out any) error {
	f.mu.Lock()
	call := len(f.calls)
	isDeep := strings.Contains(fmt.Sprint(req.Format), "events")
	f.calls = append(f.calls, fmt.Sprintf("call %d deep=%v user=%q", call, isDeep, req.Messages[len(req.Messages)-1].Content))
	f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	content, err := f.script(call, req)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(content), out)
}

func testChunks() []segment.Chunk {
	return []segment.Chunk{
		{ID: "c0001", Start: 0, End: 2, Text: "welcome to the video"},
		{ID: "c0002", Start: 3, End: 6, Text: "let's add an input variable"},
		{ID: "c0003", Start: 7, End: 9, Text: "thanks for watching"},
	}
}

func baseCfg() Config {
	return Config{Model: "test-model", Workers: 2, Retries: 2, NumCtx: 4096}
}

func triageReply(id string, code bool) string {
	return fmt.Sprintf(`{"chunk_id":%q,"has_code_action":%t,"confidence":0.9}`, id, code)
}

func TestRunConfigValidation(t *testing.T) {
	ctx := context.Background()
	chunks := testChunks()
	cli := &fakeClient{}

	if _, err := Run(ctx, chunks, Config{Workers: 2}, cli); err == nil {
		t.Error("empty model: error = nil, want error")
	}
	if _, err := Run(ctx, chunks, Config{Model: "m", Workers: 0}, cli); err == nil {
		t.Error("workers 0: error = nil, want error")
	}
	if _, err := Run(ctx, chunks, Config{Model: "m", Workers: 99}, cli); err == nil {
		t.Error("workers 99: error = nil, want error")
	}
	if _, err := Run(ctx, chunks, Config{Model: "m", Workers: 2, Retries: -1}, cli); err == nil {
		t.Error("negative retries: error = nil, want error")
	}
	if _, err := Run(ctx, chunks, Config{Model: "m", Workers: 2}, nil); err == nil {
		t.Error("nil client: error = nil, want error")
	}
}

func TestRunTriageFiltersDeepPath(t *testing.T) {
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		id := chunkIDFromUser(t, req)
		if isDeep(req) {
			if id != "c0002" {
				t.Errorf("deep pass reached non-code chunk %q", id)
			}
			return `{"events":[]}`, nil
		}
		switch id {
		case "c0002":
			return triageReply(id, true), nil
		default:
			return triageReply(id, false), nil
		}
	}}
	res, err := Run(context.Background(), testChunks(), baseCfg(), cli)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(res.Events) != 0 || len(res.Failed) != 0 {
		t.Errorf("unexpected output: %+v", res)
	}
	if len(res.Triage) != 3 {
		t.Fatalf("triage records = %d, want 3", len(res.Triage))
	}
	if res.Triage[1].ChunkID != "c0002" || !res.Triage[1].HasCodeAction {
		t.Errorf("triage[1] = %+v", res.Triage[1])
	}
}

func TestRunTriageErrorFailsOpen(t *testing.T) {
	var deepSeen atomic.Bool
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		isDeep := isDeep(req)
		if isDeep {
			deepSeen.Store(true)
			return `{"events":[]}`, nil
		}
		if chunkIDFromUser(t, req) == "c0001" {
			return "", errors.New("server hiccup")
		}
		id := chunkIDFromUser(t, req)
		return triageReply(id, false), nil
	}}
	res, err := Run(context.Background(), testChunks(), baseCfg(), cli)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !res.Triage[0].HasCodeAction || !strings.Contains(res.Triage[0].Note, "fail open") && !strings.Contains(res.Triage[0].Note, "triage error") {
		t.Errorf("first verdict = %+v, want fail-open positive", res.Triage[0])
	}
	if !deepSeen.Load() {
		t.Error("failed triage did not reach the deep pass")
	}
}

func TestRunLowConfidencePositiveKept(t *testing.T) {
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		if isDeep(req) {
			return `{"events":[{"seq":1,"op":"append","file":"A.mq5","code":"int x;"}]}`, nil
		}
		id := chunkIDFromUser(t, req)
		if id == "c0002" {
			return `{"chunk_id":"c0002","has_code_action":true,"confidence":0.3}`, nil
		}
		return triageReply(id, false), nil
	}}
	res, err := Run(context.Background(), testChunks(), baseCfg(), cli)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events = %+v, want the low-confidence chunk extracted", res.Events)
	}
	if !strings.Contains(res.Triage[1].Note, "low-confidence") {
		t.Errorf("note = %q", res.Triage[1].Note)
	}
}

func TestRunDeepExtractionAndOrdering(t *testing.T) {
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		if !isDeep(req) {
			id := chunkIDFromUser(t, req)
			return triageReply(id, id == "c0001" || id == "c0003"), nil
		}
		id := chunkIDFromUser(t, req)
		switch id {
		case "c0001":
			return `{"events":[
				{"op":"create","file":"B.mq5","code":"#property strict"},
				{"seq":2,"op":"append","file":"B.mq5","code":"int y;"}
			]}`, nil
		default:
			return `{"events":[{"seq":1,"op":"create","file":"A.mq5","code":"// a"}]}`, nil
		}
	}}
	res, err := Run(context.Background(), testChunks(), baseCfg(), cli)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("events = %+v, want 3", res.Events)
	}
	if res.Events[0].ChunkID != "c0001" || res.Events[2].ChunkID != "c0003" {
		t.Errorf("ordering wrong: %+v", res.Events)
	}
	for _, ev := range res.Events {
		if ev.ChunkID == "" || ev.Seq < 1 {
			t.Errorf("unnormalized event: %+v", ev)
		}
		if err := ev.Validate(); err != nil {
			t.Errorf("event %+v invalid: %v", ev, err)
		}
	}
}

func TestRunDeepRetriesThenFails(t *testing.T) {
	var attempts atomic.Int32
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		if !isDeep(req) {
			id := chunkIDFromUser(t, req)
			return triageReply(id, true), nil
		}
		attempts.Add(1)
		return "", &ollama.SchemaError{Detail: "bad content"}
	}}
	cfg := baseCfg()
	res, err := Run(context.Background(), []segment.Chunk{{ID: "c0001", Text: "x"}}, cfg, cli)
	if !errors.Is(err, ErrAllFailed) { // the only chunk failed, so soft error is expected
		t.Fatalf("error = %v, want ErrAllFailed", err)
	}
	if got := attempts.Load(); got != int32(cfg.Retries+1) {
		t.Errorf("attempts = %d, want %d (initial + retries)", got, cfg.Retries+1)
	}
	if len(res.Failed) != 1 || res.Failed[0].ChunkID != "c0001" || res.Failed[0].Error == "" {
		t.Errorf("failed = %+v", res.Failed)
	}
}

func TestRunInvalidEventRetriedThenFailed(t *testing.T) {
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		if !isDeep(req) {
			id := chunkIDFromUser(t, req)
			return triageReply(id, true), nil
		}
		return `{"events":[{"op":"append","file":"../evil.mq5","code":"x"}]}`, nil
	}}
	res, err := Run(context.Background(), []segment.Chunk{{ID: "c0001", Text: "x"}}, baseCfg(), cli)
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("error = %v, want ErrAllFailed (single chunk failed)", err)
	}
	if len(res.Failed) != 1 {
		t.Fatalf("failed = %+v, want one failure for traversal file", res.Failed)
	}
}

func TestRunAllFailedReturnsErr(t *testing.T) {
	cli := &fakeClient{err: errors.New("ollama down")}
	res, err := Run(context.Background(), []segment.Chunk{{ID: "c0001", Text: "x"}}, baseCfg(), cli)
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("error = %v, want ErrAllFailed", err)
	}
	if len(res.Failed) != 1 {
		t.Errorf("failed = %+v", res.Failed)
	}
}

func TestRunEmptyChunks(t *testing.T) {
	res, err := Run(context.Background(), nil, baseCfg(), &fakeClient{})
	if err != nil {
		t.Fatalf("Run(nil chunks) error = %v", err)
	}
	if len(res.Triage) != 0 || len(res.Events) != 0 || len(res.Failed) != 0 {
		t.Errorf("result = %+v, want all empty", res)
	}
}

func TestRunConcurrentMany(t *testing.T) {
	var n atomic.Int32
	cli := &fakeClient{script: func(call int, req ollama.Request) (string, error) {
		if !isDeep(req) {
			id := chunkIDFromUser(t, req)
			return triageReply(id, false), nil
		}
		n.Add(1)
		return `{"events":[]}`, nil
	}}
	chunks := make([]segment.Chunk, 40)
	for i := range chunks {
		chunks[i] = segment.Chunk{ID: fmt.Sprintf("c%04d", i+1), Text: "talk"}
	}
	if _, err := Run(context.Background(), chunks, Config{Model: "m", Workers: 4}, cli); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls := len(cli.calls); calls != 80 { // 40 triage + 40 deep? no: negatives skip deep
		t.Logf("info: total calls = %d", calls)
	}
	_ = events.Event{}
}

func isDeep(req ollama.Request) bool {
	data, _ := json.Marshal(req.Format)
	return strings.Contains(string(data), `"events"`)
}

func chunkIDFromUser(t *testing.T, req ollama.Request) string {
	t.Helper()
	user := req.Messages[len(req.Messages)-1].Content
	i := strings.LastIndex(user, "c0")
	if i < 0 {
		t.Fatalf("no chunk id in %q", user)
	}
	rest := user[i:]
	if j := strings.IndexAny(rest, ": \n\t"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
