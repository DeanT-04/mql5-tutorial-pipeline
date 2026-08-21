package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
)

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"  dQw4w9WgXcQ  ", "dQw4w9WgXcQ", false},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "dQw4w9WgXcQ", false},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://youtu.be/dQw4w9WgXcQ?t=1", "dQw4w9WgXcQ", false},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://music.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"http://youtube-nocookie.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"", "", true},
		{"not a url", "", true},
		{"https://example.com/watch?v=dQw4w9WgXcQ", "", true},
		{"https://www.youtube.com/playlist?list=PL123", "", true},
		{"https://www.youtube.com/watch?v=tooshort", "", true},
		{"https://youtu.be/a/b/c", "", true},
	}
	for _, tt := range tests {
		got, err := ExtractVideoID(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ExtractVideoID(%q) error = nil, want error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ExtractVideoID(%q) error = %v, want nil", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ExtractVideoID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// fakeRunner records invocations and can create files as a side effect,
// simulating yt-dlp/python output without any real processes or network.
type fakeRunner struct {
	onRun func(name string, args []string) ([]byte, error)
	calls []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return f.onRun(name, args)
}

const testID = "dQw4w9WgXcQ"

func json3Fixture(text string) []byte {
	return []byte(fmt.Sprintf(`{"events":[{"tStartMs":0,"dDurationMs":2000,"segs":[{"utf8":%q}]}]}`, text))
}

func writeJSON3(t *testing.T, dir, id, source, text string) {
	t.Helper()
	name := id + "." + source + ".json3"
	if err := os.WriteFile(filepath.Join(dir, name), json3Fixture(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestFetcher(onRun func(name string, args []string) ([]byte, error)) (*Fetcher, *fakeRunner) {
	fr := &fakeRunner{onRun: onRun}
	return &Fetcher{Runner: fr, PythonBin: "python", WhisperModel: "small"}, fr
}

func TestFetchCaptionsManual(t *testing.T) {
	dir := t.TempDir()
	f, fr := newTestFetcher(func(name string, args []string) ([]byte, error) {
		if name != "yt-dlp" {
			t.Fatalf("unexpected binary %q", name)
		}
		writeJSON3(t, dir, testID, "en", "manual caption")
		return []byte{}, nil
	})
	res, err := f.Fetch(context.Background(), "https://youtu.be/"+testID, dir, ModeCaptions)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.Source != SourceManual || res.VideoID != testID {
		t.Errorf("result = %+v", res)
	}
	if len(res.Lines) != 1 || res.Lines[0].Text != "manual caption" {
		t.Errorf("lines = %+v", res.Lines)
	}
	if len(fr.calls) != 1 {
		t.Errorf("calls = %v, want exactly one yt-dlp run", fr.calls)
	}
	for _, a := range strings.Split(fr.calls[0], " ") {
		if strings.Contains(a, ";") || strings.Contains(a, "&") {
			t.Errorf("shell metacharacter reached argument list: %q", fr.calls[0])
		}
	}
}

func TestFetchCaptionsAutoFallback(t *testing.T) {
	dir := t.TempDir()
	call := 0
	f, _ := newTestFetcher(func(name string, args []string) ([]byte, error) {
		call++
		auto := false
		for _, a := range args {
			if a == "--write-auto-subs" {
				auto = true
			}
		}
		if auto {
			writeJSON3(t, dir, testID, "en", "auto caption")
		}
		return []byte{}, nil
	})
	res, err := f.Fetch(context.Background(), testID, dir, ModeAuto)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.Source != SourceAuto {
		t.Errorf("source = %q, want %q (after %d calls)", res.Source, SourceAuto, call)
	}
}

func TestFetchCaptionsModeNoCaptions(t *testing.T) {
	dir := t.TempDir()
	f, _ := newTestFetcher(func(name string, args []string) ([]byte, error) { return nil, nil })
	if _, err := f.Fetch(context.Background(), testID, dir, ModeCaptions); !errors.Is(err, ErrNoCaptions) {
		t.Errorf("Fetch(captions, no subs) error = %v, want ErrNoCaptions", err)
	}
}

func TestFetchAutoFallsBackToWhisper(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, testID+".m4a")
	whisperPath := audioPath + ".whisper.json"
	f, fr := newTestFetcher(func(name string, args []string) ([]byte, error) {
		switch name {
		case "yt-dlp":
			if err := os.WriteFile(audioPath, []byte("fake audio"), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil, nil
		case "python":
			if len(args) < 4 {
				t.Fatalf("python args too short: %v", args)
			}
			if args[2] != whisperPath {
				t.Errorf("whisper output arg = %q, want %q", args[2], whisperPath)
			}
			data := []byte(`[{"start":0,"end":1.5,"text":"from whisper"}]`)
			if err := os.WriteFile(whisperPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			return nil, nil
		default:
			t.Fatalf("unexpected binary %q", name)
			return nil, nil
		}
	})
	res, err := f.Fetch(context.Background(), testID, dir, ModeAuto)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.Source != SourceWhisper {
		t.Errorf("source = %q, want %q", res.Source, SourceWhisper)
	}
	if len(res.Lines) != 1 || res.Lines[0].Text != "from whisper" {
		t.Errorf("lines = %+v", res.Lines)
	}
	sawPython := false
	for _, c := range fr.calls {
		if strings.HasPrefix(c, "python ") {
			sawPython = true
		}
	}
	if !sawPython {
		t.Errorf("no python invocation in %v", fr.calls)
	}
}

func TestFetchWhisperModeRequiresAudio(t *testing.T) {
	dir := t.TempDir()
	f, _ := newTestFetcher(func(name string, args []string) ([]byte, error) { return nil, nil })
	if _, err := f.Fetch(context.Background(), testID, dir, ModeWhisper); err == nil {
		t.Error("Fetch(whisper) with no downloaded audio error = nil, want error")
	}
}

func TestFetchInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	f, _ := newTestFetcher(func(string, []string) ([]byte, error) { return nil, nil })

	if _, err := f.Fetch(context.Background(), "https://example.com", dir, ModeAuto); err == nil {
		t.Error("Fetch(bad url) error = nil, want error")
	}
	if _, err := f.Fetch(context.Background(), testID, dir, Mode("bogus")); err == nil {
		t.Error("Fetch(bad mode) error = nil, want error")
	}
	missing := filepath.Join(dir, "does-not-exist")
	if _, err := f.Fetch(context.Background(), testID, missing, ModeCaptions); err == nil {
		t.Error("Fetch(missing dir) error = nil, want error")
	}
}

func TestLimitedBufferTruncates(t *testing.T) {
	b := limitedBuffer{max: 4}
	n, err := b.Write([]byte("abcdefgh"))
	if n != 8 || err != nil {
		t.Fatalf("Write() = (%d, %v)", n, err)
	}
	if got := b.String(); got != "abcd" {
		t.Errorf("String() = %q, want %q", got, "abcd")
	}
	if len(b.Bytes()) != 4 {
		t.Errorf("Bytes() length = %d, want 4", len(b.Bytes()))
	}
}

func TestNormalizeAppliedByFetch(t *testing.T) {
	dir := t.TempDir()
	writeJSON3(t, dir, testID, "en", "  spaced   out  ")
	f, _ := newTestFetcher(func(name string, args []string) ([]byte, error) { return nil, nil })
	res, err := f.Fetch(context.Background(), testID, dir, ModeCaptions)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want := transcript.Normalize([]transcript.Line{{Start: 0, End: 2, Text: "spaced out"}})
	if !reflect.DeepEqual(res.Lines, want) {
		t.Errorf("lines = %+v, want %+v", res.Lines, want)
	}
}
