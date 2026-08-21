// Package ytdlp wraps yt-dlp and faster-whisper to produce a transcript for
// one YouTube video (spec.md §4.1). External binaries are always invoked via
// exec.Command argument lists — URLs are untrusted input and never touch a
// shell. All process execution goes through the injectable Runner so tests run
// with no network and no binaries installed.
package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/transcript"
)

// Mode selects how a transcript is obtained.
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeCaptions Mode = "captions"
	ModeWhisper  Mode = "whisper"
)

// Transcript sources reported in Result.
const (
	SourceManual  = "captions-manual"
	SourceAuto    = "captions-auto"
	SourceWhisper = "whisper"
)

// ErrNoCaptions is returned when the video has no usable English captions.
var ErrNoCaptions = errors.New("ytdlp: no english captions available")

// maxStdoutBytes caps captured stdout (only small outputs such as
// `yt-dlp --print title` are consumed via the Runner).
const maxStdoutBytes = 1 << 20

// maxStderrBytes caps captured stderr used in error messages.
const maxStderrBytes = 8 << 10

// Runner executes an external binary with an argument list and returns stdout.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the production Runner using os/exec.
type ExecRunner struct{}

// Run implements Runner via exec.CommandContext.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 -- name and args are fixed binary names plus an argument
	// list built here; untrusted input (URLs) is never passed through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr limitedBuffer
	stdout.max = maxStdoutBytes
	stderr.max = maxStderrBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}

// limitedBuffer is an io.Writer that keeps at most max bytes.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }

// Fetcher fetches transcripts for YouTube videos.
type Fetcher struct {
	Runner       Runner
	PythonBin    string // default "python"
	WhisperModel string // default "small"
}

// New returns a Fetcher with production defaults.
func New() *Fetcher {
	return &Fetcher{Runner: ExecRunner{}, PythonBin: "python", WhisperModel: "small"}
}

// Result is a fetched transcript.
type Result struct {
	VideoID string
	Source  string
	Lines   []transcript.Line
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ExtractVideoID extracts the 11-character video ID from a YouTube URL
// (watch, youtu.be, shorts, embed, live) or accepts a bare ID.
func ExtractVideoID(rawURL string) (string, error) {
	s := strings.TrimSpace(rawURL)
	if idPattern.MatchString(s) {
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("ytdlp: parse url %q: %w", rawURL, err)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch host {
	case "youtu.be":
		id := strings.Trim(u.Path, "/")
		if i := strings.IndexByte(id, '/'); i >= 0 {
			id = id[:i]
		}
		return checkID(id)
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if v := u.Query().Get("v"); v != "" {
			return checkID(v)
		}
		parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		if len(parts) == 2 {
			switch parts[0] {
			case "shorts", "embed", "live", "v":
				return checkID(parts[1])
			}
		}
		return "", fmt.Errorf("ytdlp: no video id found in %q", rawURL)
	default:
		return "", fmt.Errorf("ytdlp: %q is not a youtube URL", rawURL)
	}
}

func checkID(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("ytdlp: %q is not a valid video id", id)
	}
	return id, nil
}

// Fetch obtains a transcript for url into dir according to mode:
//
//   - captions: manual en subs, else ErrNoCaptions
//   - whisper:  audio download + faster-whisper transcription
//   - auto:     manual en subs → auto-generated en subs → whisper
func (f *Fetcher) Fetch(ctx context.Context, rawURL, dir string, mode Mode) (Result, error) {
	id, err := ExtractVideoID(rawURL)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return Result{}, fmt.Errorf("ytdlp: output dir %s: %w", dir, err)
	}
	res := Result{VideoID: id}

	tryCaptions := func(auto bool) (bool, error) {
		args := []string{"--no-playlist", "--skip-download"}
		if auto {
			args = append(args, "--write-auto-subs")
		} else {
			args = append(args, "--write-subs")
		}
		args = append(args,
			"--sub-langs", "en",
			"--sub-format", "json3",
			"-o", filepath.Join(dir, id+".%(ext)s"),
			rawURL,
		)
		if _, err := f.Runner.Run(ctx, "yt-dlp", args...); err != nil {
			return false, fmt.Errorf("yt-dlp subtitles: %w", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, id+".en.json3")) // #nosec G304 -- fixed join of controlled dir/id
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("ytdlp: read subtitles: %w", err)
		}
		lines, err := transcript.FromJSON3(data)
		if err != nil {
			return false, err
		}
		res.Source = SourceManual
		if auto {
			res.Source = SourceAuto
		}
		res.Lines = lines
		return true, nil
	}

	downloadAudio := func() (string, error) {
		audioPath := filepath.Join(dir, id+".m4a")
		args := []string{
			"--no-playlist",
			"-x", "--audio-format", "m4a", "--audio-quality", "48K",
			"-o", filepath.Join(dir, id+".%(ext)s"),
			rawURL,
		}
		if _, err := f.Runner.Run(ctx, "yt-dlp", args...); err != nil {
			return "", fmt.Errorf("yt-dlp audio download: %w", err)
		}
		if _, err := os.Stat(audioPath); err != nil {
			return "", fmt.Errorf("ytdlp: expected audio at %s: %w", audioPath, err)
		}
		return audioPath, nil
	}

	transcribe := func(audioPath string) error {
		outPath := audioPath + ".whisper.json"
		script, err := writeWhisperScript(dir)
		if err != nil {
			return err
		}
		args := []string{script, audioPath, outPath, f.whisperModel()}
		if _, err := f.Runner.Run(ctx, f.pythonBin(), args...); err != nil {
			return fmt.Errorf("faster-whisper: %w", err)
		}
		data, err := os.ReadFile(outPath) // #nosec G304 -- fixed join of controlled paths
		if err != nil {
			return fmt.Errorf("ytdlp: read whisper output: %w", err)
		}
		lines, err := transcript.FromWhisperJSON(data)
		if err != nil {
			return err
		}
		res.Source = SourceWhisper
		res.Lines = lines
		return nil
	}

	switch mode {
	case ModeCaptions:
		found, err := tryCaptions(false)
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{}, ErrNoCaptions
		}
	case ModeWhisper:
		audioPath, err := downloadAudio()
		if err != nil {
			return Result{}, err
		}
		if err := transcribe(audioPath); err != nil {
			return Result{}, err
		}
	case ModeAuto:
		found, err := tryCaptions(false)
		if err != nil {
			return Result{}, err
		}
		if !found {
			if found, err = tryCaptions(true); err != nil {
				return Result{}, err
			}
		}
		if !found {
			audioPath, err := downloadAudio()
			if err != nil {
				return Result{}, err
			}
			if err := transcribe(audioPath); err != nil {
				return Result{}, err
			}
		}
	default:
		return Result{}, fmt.Errorf("ytdlp: invalid transcript mode %q", mode)
	}

	res.Lines = transcript.Normalize(res.Lines)
	return res, nil
}

// Title returns the video title via yt-dlp; callers may ignore its error.
func (f *Fetcher) Title(ctx context.Context, rawURL string) (string, error) {
	out, err := f.Runner.Run(ctx, "yt-dlp", "--no-playlist", "--print", "title", rawURL)
	if err != nil {
		return "", fmt.Errorf("yt-dlp title: %w", err)
	}
	title := strings.TrimSpace(string(out))
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return "", fmt.Errorf("yt-dlp title: unexpected output %q", title)
	}
	return title, nil
}

func (f *Fetcher) pythonBin() string {
	if f.PythonBin != "" {
		return f.PythonBin
	}
	return "python"
}

func (f *Fetcher) whisperModel() string {
	if f.WhisperModel != "" {
		return f.WhisperModel
	}
	return "small"
}

// whisperScript is the embedded faster-whisper driver. It reads the audio file
// and writes [{start,end,text}, ...] JSON next to it.
const whisperScript = `import json, sys
from faster_whisper import WhisperModel

audio, out = sys.argv[1], sys.argv[2]
model_name = sys.argv[3] if len(sys.argv) > 3 else "small"
model = WhisperModel(model_name, compute_type="int8", cpu_threads=8)
segments, _info = model.transcribe(audio, vad_filter=True)
result = [{"start": float(s.start), "end": float(s.end), "text": s.text} for s in segments]
with open(out, "w", encoding="utf-8") as fh:
    json.dump(result, fh, ensure_ascii=False)
`

func writeWhisperScript(dir string) (string, error) {
	path := filepath.Join(dir, ".whisper_transcribe.py")
	if err := os.WriteFile(path, []byte(whisperScript), 0o600); err != nil {
		return "", fmt.Errorf("ytdlp: write whisper script: %w", err)
	}
	return path, nil
}
