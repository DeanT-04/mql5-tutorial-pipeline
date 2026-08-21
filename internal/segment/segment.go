// Package segment merges caption lines into code-step chunks using pause gaps,
// code-action cue phrases, and token/time caps (spec.md §4.2). The function is
// pure and deterministic: identical input yields byte-identical output.
package segment

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
)

// Chunk is one code-step unit handed to the extraction stage.
type Chunk struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// Config controls the chunking heuristics.
type Config struct {
	// MaxTokens is the per-chunk token budget (estimated as len(text)/4).
	MaxTokens int
	// MaxSeconds is the maximum speech duration of one chunk.
	MaxSeconds int
	// PauseGap is the silence length that starts a new chunk.
	PauseGap time.Duration
	// Cues are phrases that start a new chunk when they open a caption
	// line (case-insensitive).
	Cues []string
}

// DefaultCues are the built-in code-action cues from design.md §4.2.
var DefaultCues = []string{
	"let's add",
	"let's write",
	"let's create",
	"let's type",
	"next line",
	"so now we",
	"type this",
	"we're going to add",
	"now we add",
}

// DefaultConfig mirrors the pipeline.yaml defaults.
func DefaultConfig() Config {
	return Config{
		MaxTokens:  350,
		MaxSeconds: 45,
		PauseGap:   1500 * time.Millisecond,
		Cues:       DefaultCues,
	}
}

// Run segments lines into deterministically-ID'd chunks (c0001, c0002, ...).
func Run(lines []transcript.Line, c Config) []Chunk {
	if len(lines) == 0 {
		return nil
	}
	cues := normalizedCues(c.Cues)
	maxGap := c.PauseGap.Seconds()

	var chunks []Chunk
	var group []transcript.Line
	var groupTokens int
	var prevEnd float64

	flush := func() {
		if len(group) == 0 {
			return
		}
		var b strings.Builder
		for i, l := range group {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(l.Text)
		}
		chunks = append(chunks, Chunk{
			Start: group[0].Start,
			End:   group[len(group)-1].End,
			Text:  b.String(),
		})
		group = nil
		groupTokens = 0
	}

	for i, l := range lines {
		gap := 0.0
		if i > 0 {
			gap = l.Start - prevEnd
		}
		startNew := len(group) > 0 && (gap > maxGap ||
			startsWithCue(l.Text, cues) ||
			groupTokens+estimateTokens(l.Text) > c.MaxTokens ||
			l.End-group[0].Start > float64(c.MaxSeconds))
		if startNew {
			flush()
		}
		group = append(group, l)
		prevEnd = l.End
		groupTokens += estimateTokens(l.Text)
	}
	flush()

	for i := range chunks {
		chunks[i].ID = fmt.Sprintf("c%04d", i+1)
	}
	return chunks
}

func normalizedCues(cues []string) []string {
	out := make([]string, 0, len(cues))
	for _, c := range cues {
		if s := strings.ToLower(strings.TrimSpace(c)); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// startsWithCue reports whether text begins with any cue, ignoring case and
// leading punctuation/whitespace. A cue in the middle of a line never matches.
func startsWithCue(text string, cues []string) bool {
	t := strings.ToLower(strings.TrimLeft(strings.TrimSpace(text), "\"'-—([ "))
	for _, cue := range cues {
		if strings.HasPrefix(t, cue) {
			return true
		}
	}
	return false
}

// estimateTokens approximates the token count of text at ~4 chars/token.
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// Marshal encodes chunks as chunks.json content.
func Marshal(chunks []Chunk) ([]byte, error) {
	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("segment: encode: %w", err)
	}
	return append(data, '\n'), nil
}

// Load reads and parses a chunks.json file.
func Load(path string) ([]Chunk, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from the run directory by design
	if err != nil {
		return nil, fmt.Errorf("segment: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var chunks []Chunk
	if err := json.NewDecoder(f).Decode(&chunks); err != nil {
		return nil, fmt.Errorf("segment: parse %s: %w", path, err)
	}
	return chunks, nil
}
