// Package stages holds the reusable implementation of each pipeline stage so
// that the standalone CLIs and the pipeline orchestrator share one code path.
// Every stage follows the same contract: content-hash resume check → work →
// atomic artifact write → manifest update.
package stages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/assemble"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/extract"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ollama"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/segment"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/verify"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ytdlp"
)

// cached reports whether a stage can be skipped: its recorded input hash
// matches AND its output artifact still exists on disk.
func cached(r *runstore.Run, stage, inputHash, artifact string) bool {
	if !r.UpToDate(stage, inputHash) {
		return false
	}
	if _, err := os.Stat(r.Path(artifact)); err != nil {
		return false
	}
	return true
}

// decodeEvents parses JSONL data into events, ignoring failed-chunk markers
// (they carry no replayable edit).
func decodeEvents(data []byte) ([]events.Event, error) {
	var evts []events.Event
	err := events.Reader(bytes.NewReader(data), func(rec any) error {
		if e, ok := rec.(events.Event); ok {
			evts = append(evts, e)
		}
		return nil
	})
	return evts, err
}

// writeJSONL encodes every item as one JSONL line into w.
func writeJSONL[T any](w io.Writer, items []T) error {
	for _, item := range items {
		if err := events.AppendJSONL(w, item); err != nil {
			return err
		}
	}
	return nil
}

// Fetch downloads the transcript for one video into its run directory.
// Returns ytdlp.ErrNoCaptions (wrapped) when captions are unavailable.
func Fetch(ctx context.Context, r *runstore.Run, rawURL string, mode ytdlp.Mode,
	fetcherFactory func() *ytdlp.Fetcher) (int, error) {

	inputHash, err := runstore.HashValue(struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}{rawURL, string(mode)})
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	if cached(r, runstore.StageFetch, inputHash, runstore.TranscriptJSON) {
		return -1, nil // -1 signals "skipped"
	}
	fetcher := fetcherFactory()
	res, err := fetcher.Fetch(ctx, rawURL, r.Dir(), mode)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	data, err := transcript.Marshal(res.Lines)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.TranscriptJSON), data); err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	title, _ := fetcher.Title(ctx, rawURL)
	r.SetMeta(rawURL, title)
	if err := r.MarkDone(runstore.StageFetch, inputHash); err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	return len(res.Lines), nil
}

// Segment merges the transcript into chunks; returns the chunk count (-1 skipped).
func Segment(r *runstore.Run, cfg segment.Config) (int, error) {
	data, err := r.ReadFileCapped(runstore.TranscriptJSON, transcript.MaxFileBytes)
	if err != nil {
		return 0, fmt.Errorf("segment: %w", err)
	}
	inputHash := runstore.HashBytes(data)
	if cached(r, runstore.StageSegment, inputHash, runstore.ChunksJSON) {
		return -1, nil
	}
	lines, err := transcript.Unmarshal(data)
	if err != nil {
		return 0, fmt.Errorf("segment: parse %s: %w", r.Path(runstore.TranscriptJSON), err)
	}
	chunks := segment.Run(lines, cfg)
	out, err := segment.Marshal(chunks)
	if err != nil {
		return 0, fmt.Errorf("segment: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.ChunksJSON), out); err != nil {
		return 0, fmt.Errorf("segment: %w", err)
	}
	if err := r.MarkDone(runstore.StageSegment, inputHash); err != nil {
		return 0, fmt.Errorf("segment: %w", err)
	}
	return len(chunks), nil
}

// Extract runs both passes over chunks.json; returns the extraction result
// (-1 signals skipped). A non-nil extract.ErrAllFailed accompanies a written
// result so callers can decide the exit code.
func Extract(ctx context.Context, r *runstore.Run, cfg extract.Config,
	ollamaURL string) (*extract.Result, error) {

	chunkData, err := r.ReadFileCapped(runstore.ChunksJSON, transcript.MaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	inputHash, err := runstore.HashValue(struct {
		Chunks string `json:"chunks"`
		Model  string `json:"model"`
	}{string(chunkData), cfg.Model})
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if cached(r, runstore.StageExtract, inputHash, runstore.EventsJSONL) {
		return nil, nil
	}
	chunks, err := segment.Unmarshal(chunkData)
	if err != nil {
		return nil, fmt.Errorf("extract: parse %s: %w", r.Path(runstore.ChunksJSON), err)
	}
	res, runErr := extract.Run(ctx, chunks, cfg, ollama.New(ollamaURL))
	soft := errors.Is(runErr, extract.ErrAllFailed)
	if runErr != nil && !soft {
		return nil, fmt.Errorf("extract: %w", runErr)
	}

	var triageBuf, eventsBuf bytes.Buffer
	if err := writeJSONL(&triageBuf, res.Triage); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := writeJSONL(&eventsBuf, res.Events); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := writeJSONL(&eventsBuf, res.Failed); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.TriageJSONL), triageBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.EventsJSONL), eventsBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := r.MarkDone(runstore.StageExtract, inputHash); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	return res, runErr
}

// Assemble replays events into out/*.mq5 plus assembly-report.json.
func Assemble(r *runstore.Run) (*assemble.Result, string, error) {
	data, err := r.ReadFileCapped(runstore.EventsJSONL, events.MaxFileBytes)
	if err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	inputHash := runstore.HashBytes(data)
	if cached(r, runstore.StageAssemble, inputHash, runstore.AssemblyReportJSON) {
		return nil, "", nil
	}
	evts, err := decodeEvents(data)
	if err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	res := assemble.Run(evts)

	outDir, err := r.OutDir()
	if err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	names := make([]string, 0, len(res.Files))
	for name := range res.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := runstore.WriteFileAtomic(filepath.Join(outDir, name), []byte(res.Files[name])); err != nil {
			return nil, "", fmt.Errorf("assemble: %w", err)
		}
	}
	report := struct {
		Applied int               `json:"applied"`
		Skipped int               `json:"skipped"`
		Files   []string          `json:"files"`
		Ops     []assemble.Record `json:"ops"`
	}{Applied: res.Applied, Skipped: res.Skipped, Files: names, Ops: res.Records}
	reportData, err := json.MarshalIndent(&report, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.AssemblyReportJSON), append(reportData, '\n')); err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	if err := r.MarkDone(runstore.StageAssemble, inputHash); err != nil {
		return nil, "", fmt.Errorf("assemble: %w", err)
	}
	return res, outDir, nil
}

// VerifyOptions parameterizes the verify stage.
type VerifyOptions struct {
	LLM       bool
	Model     string
	BaseURL   string
	KeepAlive string
	NumCtx    int
}

// Verify runs static (and optionally LLM) checks and writes report.json.
func Verify(ctx context.Context, r *runstore.Run, opts VerifyOptions) (*verify.Report, error) {
	outDir := filepath.Join(r.Dir(), "out")
	files, err := ReadOutDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	inputHash, err := runstore.HashValue(struct {
		Files map[string]string `json:"files"`
		LLM   bool              `json:"llm"`
	}{files, opts.LLM})
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if cached(r, runstore.StageVerify, inputHash, runstore.ReportJSON) {
		return nil, nil
	}
	rep := verify.Run(files)
	if opts.LLM {
		data, err := r.ReadFileCapped(runstore.EventsJSONL, events.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("verify: load events: %w", err)
		}
		evts, err := decodeEvents(data)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}
		if err := verify.RunLLM(ctx, rep, files, evts, opts.Model, opts.BaseURL,
			opts.KeepAlive, opts.NumCtx); err != nil {
			return rep, fmt.Errorf("verify: %w", err)
		}
	}
	repData, err := verify.Marshal(rep)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.ReportJSON), repData); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if err := r.MarkDone(runstore.StageVerify, inputHash); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	return rep, nil
}

// LoadReport reads a previously written report.json.
func LoadReport(r *runstore.Run) (*verify.Report, error) {
	data, err := r.ReadFileCapped(runstore.ReportJSON, 16<<20)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	var rep verify.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("verify: parse %s: %w", r.Path(runstore.ReportJSON), err)
	}
	return &rep, nil
}

// ReadOutDir loads every *.mq5 file under dir keyed by bare name.
func ReadOutDir(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".mq5" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- fixed join of controlled dir
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		files[e.Name()] = string(data)
	}
	return files, nil
}
