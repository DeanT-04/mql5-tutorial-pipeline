// Package transcript parses and normalizes caption/ASR output into a common
// timed-line representation (spec.md §4.1). It is pure: no processes, no network.
package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MaxFileBytes caps any single transcript file read from disk.
const MaxFileBytes = 64 << 20

// Line is one normalized caption line with second-precision timing.
type Line struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// json3 is YouTube's json3 subtitle container.
type json3 struct {
	Events []struct {
		TStartMs    float64 `json:"tStartMs"`
		DDurationMs float64 `json:"dDurationMs"`
		Segs        []struct {
			UTF8 string `json:"utf8"`
		} `json:"segs"`
	} `json:"events"`
}

type whisperLine struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// FromJSON3 decodes YouTube json3 subtitle data into lines.
func FromJSON3(data []byte) ([]Line, error) {
	var doc json3
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("transcript: parse json3: %w", err)
	}
	lines := make([]Line, 0, len(doc.Events))
	for _, ev := range doc.Events {
		if len(ev.Segs) == 0 {
			continue
		}
		var b strings.Builder
		for _, seg := range ev.Segs {
			b.WriteString(seg.UTF8)
		}
		start := ev.TStartMs / 1000
		end := start + ev.DDurationMs/1000
		lines = append(lines, Line{Start: start, End: end, Text: b.String()})
	}
	return lines, nil
}

// FromWhisperJSON decodes faster-whisper JSON output ([{start,end,text}, ...]).
func FromWhisperJSON(data []byte) ([]Line, error) {
	var raw []whisperLine
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("transcript: parse whisper json: %w", err)
	}
	lines := make([]Line, 0, len(raw))
	for _, w := range raw {
		lines = append(lines, Line(w))
	}
	return lines, nil
}

// Normalize trims/collapses whitespace, drops empty lines, sorts by start time
// and clamps overlapping end times so lines never overlap.
func Normalize(lines []Line) []Line {
	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		text := strings.Join(strings.Fields(l.Text), " ")
		if text == "" {
			continue
		}
		out = append(out, Line{Start: l.Start, End: l.End, Text: text})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	for i := 0; i+1 < len(out); i++ {
		if out[i].End > out[i+1].Start {
			out[i].End = out[i+1].Start
		}
		if out[i].End < out[i].Start {
			out[i].End = out[i].Start
		}
	}
	return out
}

// Marshal encodes lines as transcript.json content (spec.md §4.1).
func Marshal(lines []Line) ([]byte, error) {
	data, err := json.MarshalIndent(lines, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("transcript: encode: %w", err)
	}
	return append(data, '\n'), nil
}

// Unmarshal decodes transcript.json content (as produced by Marshal) into
// lines. Callers are responsible for capping the input size before reading
// it from disk (see runstore.Run.ReadFileCapped).
func Unmarshal(data []byte) ([]Line, error) {
	var lines []Line
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil, fmt.Errorf("transcript: parse: %w", err)
	}
	return lines, nil
}
