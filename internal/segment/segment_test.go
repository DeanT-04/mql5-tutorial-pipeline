package segment

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
)

func line(start, end float64, text string) transcript.Line {
	return transcript.Line{Start: start, End: end, Text: text}
}

func ids(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.ID
	}
	return out
}

func TestRunEmpty(t *testing.T) {
	if got := Run(nil, DefaultConfig()); got != nil {
		t.Errorf("Run(nil) = %+v, want nil", got)
	}
	if got := Run([]transcript.Line{}, DefaultConfig()); got != nil {
		t.Errorf("Run(empty) = %+v, want nil", got)
	}
}

func TestRunSingleLine(t *testing.T) {
	got := Run([]transcript.Line{line(0, 2, "one line only")}, DefaultConfig())
	want := []Chunk{{ID: "c0001", Start: 0, End: 2, Text: "one line only"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
}

func TestRunPauseGapSplits(t *testing.T) {
	cfg := DefaultConfig() // 1.5s gap
	lines := []transcript.Line{
		line(0, 1, "before the pause"),
		line(3, 4, "after a long silence"), // 2s gap
		line(4.5, 5, "short gap here"),     // 0.5s gap
	}
	got := Run(lines, cfg)
	wantIDs := []string{"c0001", "c0002"}
	if !reflect.DeepEqual(ids(got), wantIDs) {
		t.Errorf("ids = %v, want %v (chunks: %+v)", ids(got), wantIDs, got)
	}
	if got[1].Start != 3 {
		t.Errorf("second chunk start = %f, want 3", got[1].Start)
	}
}

func TestRunCueSplitsOnlyAtLineStart(t *testing.T) {
	cfg := DefaultConfig()
	lines := []transcript.Line{
		line(0, 1, "and in the middle we say next line should not split"),
		line(2, 3, "Next line starts with the cue"),
	}
	got := Run(lines, cfg)
	if len(got) != 2 {
		t.Fatalf("chunks = %d (%+v), want 2: cue must split at line start only", len(got), got)
	}
	if got[1].Text != "Next line starts with the cue" {
		t.Errorf("second chunk text = %q", got[1].Text)
	}
}

func TestRunCueCaseInsensitiveAndPunctuationTolerant(t *testing.T) {
	cfg := DefaultConfig()
	lines := []transcript.Line{
		line(0, 1, "first chunk content"),
		line(2, 3, "\"Let's add an input variable\""),
	}
	got := Run(lines, cfg)
	if len(got) != 2 {
		t.Fatalf("chunks = %d (%+v), want 2", len(got), got)
	}
}

func TestRunTokenCapSplits(t *testing.T) {
	cfg := Config{MaxTokens: 10, MaxSeconds: 100, PauseGap: time.Hour, Cues: nil}
	long := strings.Repeat("word ", 6) // ~30 chars => 8 tokens
	lines := []transcript.Line{
		line(0, 1, long),
		line(1.5, 2.5, long), // 8+8 > 10 => split despite no gap/cue
	}
	got := Run(lines, cfg)
	if len(got) != 2 {
		t.Errorf("chunks = %d (%+v), want 2 via token cap", len(got), got)
	}
}

func TestRunCapSplitPrefersSentenceBoundary(t *testing.T) {
	cfg := Config{MaxTokens: 15, MaxSeconds: 1000, PauseGap: time.Hour, Cues: nil}
	lines := []transcript.Line{
		line(0, 1, "words without any stop"),        // 6 tokens
		line(1.1, 2, "more words ending right."),    // 7 tokens -> group would exceed on next
		line(2.1, 3, "tail content after boundary"), // 8 tokens -> forces cap split
	}
	got := Run(lines, cfg)
	if len(got) != 2 {
		t.Fatalf("chunks = %d (%+v), want 2", len(got), got)
	}
	if !strings.HasSuffix(got[0].Text, ".") {
		t.Errorf("first chunk should end at the sentence boundary, got %q", got[0].Text)
	}
	if got[1].Text != "tail content after boundary" {
		t.Errorf("second chunk should carry only the overflow line, got %q", got[1].Text)
	}
}

func TestRunTimeCapSplits(t *testing.T) {
	cfg := Config{MaxTokens: 10000, MaxSeconds: 10, PauseGap: time.Hour, Cues: nil}
	lines := []transcript.Line{
		line(0, 5, "early speech"),
		line(6, 12, "late speech"), // span would be 12s > 10s
	}
	got := Run(lines, cfg)
	if len(got) != 2 {
		t.Errorf("chunks = %d (%+v), want 2 via time cap", len(got), got)
	}
}

func TestRunDeterministic(t *testing.T) {
	lines := []transcript.Line{
		line(0, 1, "let's add a variable"),
		line(5, 6, "so now we initialize it"),
		line(20, 21, "type this exactly"),
	}
	a := Run(lines, DefaultConfig())
	b := Run(lines, DefaultConfig())
	if !reflect.DeepEqual(a, b) {
		t.Error("Run() not deterministic")
	}
	dataA, err := Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	dataB, _ := Marshal(b)
	if string(dataA) != string(dataB) {
		t.Error("Marshal(Run(...)) not byte-stable across runs")
	}
	if !reflect.DeepEqual(ids(a), []string{"c0001", "c0002", "c0003"}) {
		t.Errorf("ids = %v", ids(a))
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	chunks := []Chunk{{ID: "c0001", Start: 1, End: 2, Text: "hello"}}
	data, err := Marshal(chunks)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, chunks) {
		t.Errorf("round trip = %+v, want %+v", got, chunks)
	}
}

func TestUnmarshalError(t *testing.T) {
	if _, err := Unmarshal([]byte("{")); err == nil {
		t.Error("Unmarshal(corrupt) error = nil, want error")
	}
}

func TestMarshalValidJSON(t *testing.T) {
	var parsed []Chunk
	data, err := Marshal([]Chunk{{ID: "c0001"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Marshal output is not valid JSON: %v", err)
	}
}
