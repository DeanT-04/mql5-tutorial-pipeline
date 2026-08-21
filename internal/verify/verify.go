// Package verify runs static (and optional LLM) checks over assembled .mq5
// files and produces a weighted-confidence report (spec.md §4.5).
package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/ollama"
)

// Severities.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
)

// Confidence penalties per finding severity.
const (
	errorPenalty   = 0.25
	warningPenalty = 0.05
)

type checkFunc func(file, src string) []Finding

// Finding is one itemized verification result.
type Finding struct {
	File     string `json:"file"`
	Severity string `json:"severity"`
	Check    string `json:"check"`
	Detail   string `json:"detail"`
}

// FileReport summarizes one file's verdict.
type FileReport struct {
	File        string            `json:"file"`
	ProgramType string            `json:"program_type"`
	Metadata    map[string]string `json:"metadata"`
	Confidence  float64           `json:"confidence"`
}

// Report is the full verify output written to report.json.
type Report struct {
	Files      []FileReport `json:"files"`
	Findings   []Finding    `json:"findings"`
	Confidence float64      `json:"confidence"`
}

var staticChecks = []struct {
	name string
	fn   checkFunc
}{
	{"balance", checkBalance},
	{"entry_points", checkEntryPoints},
	{"property_strict", checkPropertyStrict},
	{"artifacts", checkArtifacts},
	{"truncation", checkTruncation},
}

var propertyPattern = regexp.MustCompile(`(?m)^\s*#property\s+([A-Za-z_]+)\s+"?([^"\r\n]*)"?"?`)

// Run performs all static checks over the assembled files. files maps bare
// file names to source text.
func Run(files map[string]string) *Report {
	rep := &Report{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		src := files[name]
		fr := FileReport{
			File:        name,
			ProgramType: detectProgramType(src),
			Metadata:    ExtractMetadata(src),
			Confidence:  1.0,
		}
		for _, c := range staticChecks {
			for _, f := range c.fn(name, src) {
				rep.Findings = append(rep.Findings, f)
				switch f.Severity {
				case SevError:
					fr.Confidence -= errorPenalty
				case SevWarning:
					fr.Confidence -= warningPenalty
				}
			}
		}
		if fr.Confidence < 0 {
			fr.Confidence = 0
		}
		rep.Files = append(rep.Files, fr)
	}
	rep.Confidence = overall(rep.Files)
	return rep
}

func overall(files []FileReport) float64 {
	if len(files) == 0 {
		return 0
	}
	sum := 0.0
	for _, f := range files {
		sum += f.Confidence
	}
	return sum / float64(len(files))
}

// detectProgramType classifies an MQL5 program by its entry points.
func detectProgramType(src string) string {
	switch {
	case strings.Contains(src, "OnCalculate"):
		return "indicator"
	case strings.Contains(src, "OnStart"):
		return "script"
	case strings.Contains(src, "OnInit") || strings.Contains(src, "OnTick"):
		return "ea"
	default:
		return "unknown"
	}
}

var entryPoints = map[string][]string{
	"ea":        {"OnInit", "OnTick"},
	"indicator": {"OnCalculate"},
	"script":    {"OnStart"},
}

func checkEntryPoints(file, src string) []Finding {
	pt := detectProgramType(src)
	if pt == "unknown" {
		return nil // balance/artifact checks cover broken files; a talk-only file is not an error
	}
	var out []Finding
	for _, fn := range entryPoints[pt] {
		if !strings.Contains(src, fn) {
			out = append(out, Finding{File: file, Severity: SevError, Check: "entry_points",
				Detail: fmt.Sprintf("%s missing required entry point %s()", pt, fn)})
		}
	}
	return out
}

// stripCode removes comments and string literals so balance counting is not
// confused by braces in strings or commented-out code.
func stripCode(src string) string {
	var b strings.Builder
	inLineComment, inBlockComment, inString, inChar := false, false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				b.WriteByte(c)
			}
		case inBlockComment:
			if c == '*' && next == '/' {
				inBlockComment = false
				i++
			}
		case inString:
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
		case inChar:
			switch c {
			case '\\':
				i++
			case '\'':
				inChar = false
			}
		default:
			switch {
			case c == '/' && next == '/':
				inLineComment = true
				i++
			case c == '/' && next == '*':
				inBlockComment = true
				i++
			case c == '"':
				inString = true
			case c == '\'':
				inChar = true
			default:
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

func countBalanced(src string, open, close rune) bool {
	depth := 0
	for _, r := range src {
		switch r {
		case open:
			depth++
		case close:
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func checkBalance(file, src string) []Finding {
	code := stripCode(src)
	var out []Finding
	if !countBalanced(code, '{', '}') {
		out = append(out, Finding{File: file, Severity: SevError, Check: "balance", Detail: "curly braces are unbalanced"})
	}
	if !countBalanced(code, '(', ')') {
		out = append(out, Finding{File: file, Severity: SevError, Check: "balance", Detail: "parentheses are unbalanced"})
	}
	return out
}

func checkPropertyStrict(file, src string) []Finding {
	if !strings.Contains(src, "#property strict") {
		return []Finding{{File: file, Severity: SevWarning, Check: "property_strict",
			Detail: "#property strict is missing"}}
	}
	return nil
}

var artifactPatterns = []struct {
	pattern *regexp.Regexp
	detail  string
}{
	{regexp.MustCompile(`\bTODO\b`), "TODO marker left in code"},
	{regexp.MustCompile(`\bFIXME\b`), "FIXME marker left in code"},
	{regexp.MustCompile(`(?i)\bplaceholder\b`), "placeholder text"},
	{regexp.MustCompile(`<[^>]*your[^>]*>`), "template placeholder like <your...>"},
	{regexp.MustCompile(`\.\.\.\s*$`), "ellipsis at end of line (possible truncation)"},
}

func checkArtifacts(file, src string) []Finding {
	var out []Finding
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		for _, a := range artifactPatterns {
			if a.pattern.MatchString(t) {
				out = append(out, Finding{File: file, Severity: SevWarning, Check: "artifacts",
					Detail: a.detail + ": " + truncate(t)})
			}
		}
	}
	return out
}

// checkTruncation flags a last meaningful line that does not look finished.
func checkTruncation(file, src string) []Finding {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if !strings.HasSuffix(t, ";") && !strings.HasSuffix(t, "{") &&
			!strings.HasSuffix(t, "}") && !strings.HasSuffix(t, "*/") {
			return []Finding{{File: file, Severity: SevWarning, Check: "truncation",
				Detail: "last line looks unfinished: " + truncate(t)}}
		}
		return nil
	}
	return nil
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	const n = 80
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ExtractMetadata mirrors the sibling extractor's semantics: #property
// key/value pairs from non-comment lines.
func ExtractMetadata(src string) map[string]string {
	meta := map[string]string{}
	code := stripCommentsOnly(src)
	for _, m := range propertyPattern.FindAllStringSubmatch(code, -1) {
		key := strings.ToLower(m[1])
		if _, dup := meta[key]; !dup {
			meta[key] = strings.TrimSpace(m[2])
		}
	}
	return meta
}

// stripCommentsOnly removes comments but keeps strings intact (metadata may
// contain quoted values).
func stripCommentsOnly(src string) string {
	var b strings.Builder
	inLineComment, inBlockComment := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				b.WriteByte(c)
			}
		case inBlockComment:
			if c == '*' && next == '/' {
				inBlockComment = false
				i++
			}
		default:
			switch {
			case c == '/' && next == '/':
				inLineComment = true
				i++
			case c == '/' && next == '*':
				inBlockComment = true
				i++
			default:
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// LLM verdict schema for --llm-check.
type llmVerdict struct {
	Match   bool     `json:"match"`
	Missing []string `json:"missing,omitempty"`
	Extra   []string `json:"extra,omitempty"`
	Notes   string   `json:"notes,omitempty"`
}

const llmSystem = `You verify that assembled MQL5 source code matches the code events that were extracted from a tutorial transcript.

You receive the final file content and the list of code events that produced it. Decide whether the content faithfully reflects exactly those events.

Answer with exactly one JSON object:
{"match": true|false, "missing": ["code present in events but absent from the file"], "extra": ["code in the file with no event"], "notes": ""}`

// RunLLM adds an LLM diff-check to a static report: one model call comparing
// each assembled file against the code of its events.
func RunLLM(ctx context.Context, rep *Report, files map[string]string, evts []events.Event,
	model, baseURL, keepAlive string, numCtx int) error {

	byFile := map[string]*strings.Builder{}
	for _, e := range evts {
		b, ok := byFile[e.File]
		if !ok {
			b = &strings.Builder{}
			byFile[e.File] = b
		}
		b.WriteString(e.Code)
		b.WriteString("\n")
	}

	cli := ollama.New(baseURL)
	for i := range rep.Files {
		fr := &rep.Files[i]
		eventsCode, ok := byFile[fr.File]
		if !ok {
			continue
		}
		user := "Final file:\n```\n" + files[fr.File] + "\n```\n\nEvents code:\n```\n" + eventsCode.String() + "\n```"
		req := ollama.Request{
			Model: model,
			Messages: []ollama.Message{
				{Role: "system", Content: llmSystem},
				{Role: "user", Content: user},
			},
			Format: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"match":   map[string]any{"type": "boolean"},
					"missing": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"extra":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"notes":   map[string]any{"type": "string"},
				},
				"required": []string{"match", "missing", "extra", "notes"},
			},
			KeepAlive: keepAlive,
			Options:   ollama.Options{Temperature: 0, NumCtx: numCtx, NumPredict: 512},
		}
		var v llmVerdict
		if err := cli.ChatJSON(ctx, req, &v); err != nil {
			return fmt.Errorf("verify: llm-check %s: %w", fr.File, err)
		}
		if !v.Match {
			detail := "LLM diff-check reports mismatch"
			if len(v.Missing) > 0 {
				detail += "; missing: " + truncate(strings.Join(v.Missing, " | "))
			}
			if len(v.Extra) > 0 {
				detail += "; extra: " + truncate(strings.Join(v.Extra, " | "))
			}
			if v.Notes != "" {
				detail += "; notes: " + truncate(v.Notes)
			}
			rep.Findings = append(rep.Findings, Finding{File: fr.File, Severity: SevError, Check: "llm_check", Detail: detail})
			fr.Confidence -= errorPenalty
			if fr.Confidence < 0 {
				fr.Confidence = 0
			}
		}
	}
	rep.Confidence = overall(rep.Files)
	return nil
}

// Marshal encodes the report as report.json content.
func Marshal(rep *Report) ([]byte, error) {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("verify: encode: %w", err)
	}
	return append(data, '\n'), nil
}
