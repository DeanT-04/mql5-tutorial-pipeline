// Package fidelity scores an assembled .mq5 file against a ground-truth file
// for one tutorial video. The metric is deterministic and transparent:
//
//  1. Both files are stripped of comments and blank lines.
//  2. Remaining lines are tokenized on non-alphanumeric characters
//     (identifiers, keywords, numbers, operators become separate tokens).
//  3. Score = multiset token-level Precision / Recall / F1.
//
// A score of 1.0 means every ground-truth token is present exactly as often
// as expected and nothing extra was invented.
package fidelity

import (
	"fmt"
	"strings"
	"unicode"
)

// Result holds one comparison.
type Result struct {
	GoldenTokens int     `json:"golden_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Precision    float64 `json:"precision"`
	Recall       float64 `json:"recall"`
	F1           float64 `json:"f1"`
}

// StripCommentsAndBlanks removes // and /* */ comments, blank lines, and
// leading/trailing whitespace, keeping string literals intact.
func StripCommentsAndBlanks(src string) []string {
	var out []string
	inBlock := false
	for _, raw := range strings.Split(src, "\n") {
		line := raw
		if inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				line = line[i+2:]
				inBlock = false
			} else {
				continue
			}
		}
		if i := strings.Index(line, "//"); i >= 0 && !inString(line[:i]) {
			line = line[:i]
		}
		if strings.Contains(line, "/*") {
			if i := strings.Index(line, "/*"); i >= 0 {
				if j := strings.Index(line[i+2:], "*/"); j >= 0 {
					line = line[:i] + line[i+2+j+2:]
				} else {
					line = line[:i]
					inBlock = true
				}
			}
		}
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// inString reports whether position i sits inside a double-quoted literal.
func inString(prefix string) bool {
	n := 0
	for _, r := range prefix {
		if r == '"' {
			n++
		}
	}
	return n%2 == 1
}

// Tokens splits source text into lowercase-insensitive-free alphanumeric runs.
// Case is preserved: MQL5 is case-sensitive and casing errors are real errors.
func Tokens(src string) []string {
	var toks []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range src {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
			if !unicode.IsSpace(r) {
				toks = append(toks, string(r))
			}
		}
	}
	flush()
	return toks
}

// Compare scores output against golden.
func Compare(golden, output string) Result {
	g := count(Tokens(strings.Join(StripCommentsAndBlanks(golden), "\n")))
	o := count(Tokens(strings.Join(StripCommentsAndBlanks(output), "\n")))

	matched := 0
	total := 0
	for tok, n := range g {
		total += n
		matched += min(n, o[tok])
	}
	outTotal := 0
	for _, n := range o {
		outTotal += n
	}

	res := Result{GoldenTokens: total, OutputTokens: outTotal}
	if total > 0 {
		res.Recall = float64(matched) / float64(total)
	}
	if outTotal > 0 {
		res.Precision = float64(matched) / float64(outTotal)
	}
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	return res
}

func count(toks []string) map[string]int {
	m := make(map[string]int, len(toks))
	for _, t := range toks {
		m[t]++
	}
	return m
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// String renders a compact human-readable summary.
func (r Result) String() string {
	return fmt.Sprintf("precision=%.3f recall=%.3f f1=%.3f (golden %d tokens, output %d)",
		r.Precision, r.Recall, r.F1, r.GoldenTokens, r.OutputTokens)
}
