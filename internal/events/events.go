// Package events defines the extraction event schema and its validation
// (spec.md §4.3): ops create|append|replace|property|include, JSONL storage,
// and failed-chunk records.
package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	maxLineBytes = 1 << 20
	// MaxFileBytes caps any single events/triage JSONL file read into memory.
	MaxFileBytes = 256 << 20
)

// Op is the kind of edit an event describes.
type Op string

// Valid ops (spec.md §4.3).
const (
	OpCreate   Op = "create"
	OpAppend   Op = "append"
	OpReplace  Op = "replace"
	OpProperty Op = "property"
	OpInclude  Op = "include"
)

func (o Op) valid() bool {
	switch o {
	case OpCreate, OpAppend, OpReplace, OpProperty, OpInclude:
		return true
	}
	return false
}

// Event is one deterministic code-edit instruction.
type Event struct {
	ChunkID string `json:"chunk_id"`
	Seq     int    `json:"seq"`
	Op      Op     `json:"op"`
	File    string `json:"file"`
	Anchor  string `json:"anchor,omitempty"`
	Code    string `json:"code"`
}

// Failed marks a chunk whose extraction permanently failed after retries.
type Failed struct {
	ChunkID string `json:"chunk_id"`
	Error   string `json:"error"`
}

// WithAnchor returns a copy of e with the replace anchor set.
func (e Event) WithAnchor(anchor string) Event {
	e.Anchor = anchor
	return e
}

// Validate checks an event against the spec rules. replace requires a
// non-empty anchor; file must be a bare .mq5 name; chunk_id and code are
// mandatory; seq >= 1.
func (e Event) Validate() error {
	if e.ChunkID == "" {
		return fmt.Errorf("events: empty chunk_id")
	}
	if e.Seq < 1 {
		return fmt.Errorf("events: %s: seq %d < 1", e.ChunkID, e.Seq)
	}
	if !e.Op.valid() {
		return fmt.Errorf("events: %s: invalid op %q", e.ChunkID, e.Op)
	}
	if e.File == "" {
		return fmt.Errorf("events: %s: empty file", e.ChunkID)
	}
	if dir, base := filepath.Split(e.File); dir != "" || base != filepath.Base(e.File) ||
		strings.ContainsAny(e.File, `/\`) || strings.HasPrefix(e.File, ".") {
		return fmt.Errorf("events: %s: file must be a bare file name, got %q", e.ChunkID, e.File)
	}
	if !strings.HasSuffix(e.File, ".mq5") {
		return fmt.Errorf("events: %s: file %q must end in .mq5", e.ChunkID, e.File)
	}
	if e.Op == OpReplace && strings.TrimSpace(e.Anchor) == "" {
		return fmt.Errorf("events: %s: replace requires a non-empty anchor", e.ChunkID)
	}
	if e.Code == "" && e.Op != OpReplace {
		return fmt.Errorf("events: %s: empty code", e.ChunkID)
	}
	return nil
}

// Normalize re-anchors each event to chunkID (the authoritative id),
// renumbers seq in list order when absent (< 1), and validates every event.
// It is the single entry point for normalizing model-emitted event lists.
func Normalize(chunkID string, in []Event) ([]Event, error) {
	out := make([]Event, 0, len(in))
	for i, ev := range in {
		ev.ChunkID = chunkID
		if ev.Seq < 1 {
			ev.Seq = i + 1
		}
		if err := ev.Validate(); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// Reader reads JSONL records from r. Records may be Event objects or
// {"chunk_id","error"} failure markers. Use type switches on *Event / *Failed.
func Reader(r io.Reader, fn func(rec any) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			return fmt.Errorf("events: line %d: %w", lineNo, err)
		}
		if _, hasErr := probe["error"]; hasErr {
			var f Failed
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				return fmt.Errorf("events: line %d: %w", lineNo, err)
			}
			if err := fn(f); err != nil {
				return err
			}
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return fmt.Errorf("events: line %d: %w", lineNo, err)
		}
		if err := e.Validate(); err != nil {
			return fmt.Errorf("events: line %d: %w", lineNo, err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("events: read: %w", err)
	}
	return nil
}

// AppendJSONL marshals v and appends it as one JSONL line to w.
func AppendJSONL(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("events: encode: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("events: write: %w", err)
	}
	return nil
}
