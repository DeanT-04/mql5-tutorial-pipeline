// Package extract runs the two-pass extraction (triage, then deep event
// extraction) over transcript chunks (spec.md §4.3). The Ollama client is
// injected, so tests script a fake server-side and never need a running model.
package extract

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ollama"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/prompts"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
)

// Client is the subset of *ollama.Client this package depends on.
type Client interface {
	ChatJSON(ctx context.Context, req ollama.Request, out any) error
}

// Config controls the extraction passes.
type Config struct {
	Model     string
	Workers   int
	Retries   int
	NumCtx    int
	KeepAlive string
}

// TriageRecord is one Pass-A verdict.
type TriageRecord struct {
	ChunkID       string  `json:"chunk_id"`
	HasCodeAction bool    `json:"has_code_action"`
	Confidence    float64 `json:"confidence"`
	Note          string  `json:"note,omitempty"`
}

// Result collects all pass outputs in deterministic (chunk, seq) order.
type Result struct {
	Triage []TriageRecord
	Events []events.Event
	Failed []events.Failed
}

// ErrAllFailed is returned when every triaged chunk failed permanently.
var ErrAllFailed = errors.New("extract: every chunk failed extraction")

// Run executes both passes. Triage failures fail open (chunk stays in the
// deep path); deep failures retry up to cfg.Retries and then become Failed
// markers instead of aborting the run.
func Run(ctx context.Context, chunks []segment.Chunk, cfg Config, cli Client) (*Result, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("extract: empty model")
	}
	if cfg.Workers < 1 || cfg.Workers > 8 {
		return nil, fmt.Errorf("extract: workers must be in [1, 8], got %d", cfg.Workers)
	}
	if cfg.Retries < 0 {
		return nil, fmt.Errorf("extract: retries must be >= 0")
	}
	if cli == nil {
		return nil, fmt.Errorf("extract: nil client")
	}

	verdicts, passed := triage(ctx, chunks, cfg, cli)
	evts, fails := deepExtract(ctx, passed, chunks, cfg, cli)
	res := &Result{Events: evts, Failed: fails}
	for _, c := range chunks {
		if rec, ok := verdicts[c.ID]; ok {
			res.Triage = append(res.Triage, rec)
		}
	}
	sort.SliceStable(res.Events, func(i, j int) bool {
		a, b := res.Events[i], res.Events[j]
		if a.ChunkID != b.ChunkID {
			return a.ChunkID < b.ChunkID
		}
		return a.Seq < b.Seq
	})
	sort.SliceStable(res.Failed, func(i, j int) bool { return res.Failed[i].ChunkID < res.Failed[j].ChunkID })
	if len(passed) > 0 && len(fails) == len(passed) {
		return res, ErrAllFailed
	}
	return res, nil
}

type triReply struct {
	ChunkID       string  `json:"chunk_id"`
	HasCodeAction bool    `json:"has_code_action"`
	Confidence    float64 `json:"confidence"`
}

// deepReply is the schema-forced Pass-B reply envelope.
type deepReply struct {
	Events []events.Event `json:"events"`
}

// triage runs Pass A over all chunks concurrently and returns the verdicts
// plus the ordered list of chunk IDs that continue to Pass B.
func triage(ctx context.Context, chunks []segment.Chunk, cfg Config, cli Client) (verdicts map[string]TriageRecord, passed []string) {
	var mu sync.Mutex
	verdicts = map[string]TriageRecord{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Workers)

	for _, c := range chunks {
		wg.Add(1)
		go func(c segment.Chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req := ollama.Request{
				Model: cfg.Model,
				Messages: []ollama.Message{
					{Role: "system", Content: prompts.System},
					{Role: "user", Content: prompts.TriageUser(c.ID, c.Text)},
				},
				Format:    prompts.TriageSchema(),
				KeepAlive: cfg.KeepAlive,
				Options:   ollama.Options{Temperature: 0, NumCtx: cfg.NumCtx, NumPredict: 64},
			}
			var reply triReply
			rec := TriageRecord{ChunkID: c.ID, HasCodeAction: true}
			if err := cli.ChatJSON(ctx, req, &reply); err != nil {
				// Fail open: keep the chunk in the expensive path rather
				// than risk silently dropping code actions.
				rec.Note = "triage error: " + err.Error()
			} else {
				rec.HasCodeAction = reply.HasCodeAction
				rec.Confidence = reply.Confidence
				if reply.HasCodeAction && reply.Confidence > 0 && reply.Confidence < 0.5 {
					rec.Note = "low-confidence positive kept (fail open)"
				}
			}
			mu.Lock()
			defer mu.Unlock()
			verdicts[c.ID] = rec
			if rec.HasCodeAction {
				passed = append(passed, c.ID)
			}
		}(c)
	}
	wg.Wait()
	return verdicts, passed
}

func deepExtract(ctx context.Context, passed []string, chunks []segment.Chunk, cfg Config, cli Client) ([]events.Event, []events.Failed) {
	text := make(map[string]string, len(chunks))
	for _, c := range chunks {
		text[c.ID] = c.Text
	}

	type outcome struct {
		id    string
		evts  []events.Event
		fails *events.Failed
	}
	out := make([]outcome, len(passed))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Workers)

	for i, id := range passed {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req := ollama.Request{
				Model: cfg.Model,
				Messages: []ollama.Message{
					{Role: "system", Content: prompts.DeepSystem},
					{Role: "user", Content: prompts.DeepUser(id, text[id])},
				},
				Format:    prompts.DeepSchema(),
				KeepAlive: cfg.KeepAlive,
				Options:   ollama.Options{Temperature: 0, NumCtx: cfg.NumCtx, NumPredict: 512},
			}

			o := outcome{id: id}
			content := ""
			for attempt := 0; attempt <= cfg.Retries; attempt++ {
				var reply deepReply
				err := cli.ChatJSON(ctx, req, &reply)
				if err != nil {
					content = err.Error()
					continue // retry on any client/schema error
				}
				evts, verr := events.Normalize(id, reply.Events)
				if verr != nil {
					content = verr.Error()
					continue
				}
				o.evts = evts
				break
			}
			if o.evts == nil {
				o.fails = &events.Failed{ChunkID: id, Error: content}
			}
			out[i] = o
		}(i, id)
	}
	wg.Wait()

	var evts []events.Event
	var fails []events.Failed
	for _, o := range out {
		if o.fails != nil {
			fails = append(fails, *o.fails)
			continue
		}
		evts = append(evts, o.evts...)
	}
	return evts, fails
}
