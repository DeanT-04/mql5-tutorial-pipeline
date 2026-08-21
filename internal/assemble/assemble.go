// Package assemble deterministically replays extraction events into .mq5
// files (spec.md §4.4). Conflicts are recorded and skipped — never guessed.
package assemble

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
)

// Record is the per-op outcome noted in assembly-report.json.
type Record struct {
	ChunkID string    `json:"chunk_id"`
	Seq     int       `json:"seq"`
	Op      events.Op `json:"op"`
	File    string    `json:"file"`
	Status  string    `json:"status"` // "applied" | "skipped"
	Detail  string    `json:"detail,omitempty"`
}

// Result is the assembled output plus its report data.
type Result struct {
	Files   map[string]string `json:"files"`
	Applied int               `json:"applied"`
	Skipped int               `json:"skipped"`
	Records []Record          `json:"-"`
}

// Run replays evts in (chunk_id, seq) order and returns the final file map.
// The input slice is not modified.
func Run(evts []events.Event) *Result {
	sorted := make([]events.Event, len(evts))
	copy(sorted, evts)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.ChunkID != b.ChunkID {
			return a.ChunkID < b.ChunkID
		}
		return a.Seq < b.Seq
	})

	res := &Result{Files: map[string]string{}}
	for _, e := range sorted {
		rec := Record{ChunkID: e.ChunkID, Seq: e.Seq, Op: e.Op, File: e.File}
		if err := apply(res.Files, e); err != nil {
			rec.Status = "skipped"
			rec.Detail = err.Error()
			res.Skipped++
		} else {
			rec.Status = "applied"
			res.Applied++
		}
		res.Records = append(res.Records, rec)
	}
	return res
}

func apply(files map[string]string, e events.Event) error {
	switch e.Op {
	case events.OpCreate:
		if _, exists := files[e.File]; exists {
			return fmt.Errorf("create: %s already exists", e.File)
		}
		files[e.File] = terminate(e.Code)

	case events.OpAppend:
		if _, ok := files[e.File]; !ok {
			// Implicit create: extraction often misses the explicit
			// "create file" moment. An empty container is not invented code;
			// every subsequent line is still dictated.
			files[e.File] = ""
		}
		files[e.File] = join(files[e.File], e.Code)

	case events.OpReplace:
		cur, ok := files[e.File]
		if !ok {
			return fmt.Errorf("replace: %s does not exist", e.File)
		}
		count := strings.Count(cur, e.Anchor)
		if count == 0 {
			return fmt.Errorf("replace: anchor not found in %s: %q", e.File, snippet(e.Anchor))
		}
		if count > 1 {
			return fmt.Errorf("replace: anchor occurs %d times in %s: %q", count, e.File, snippet(e.Anchor))
		}
		files[e.File] = strings.Replace(cur, e.Anchor, e.Code, 1)

	case events.OpProperty, events.OpInclude:
		cur, ok := files[e.File]
		if !ok {
			cur = "" // implicit create, same rationale as append
		}
		line := strings.TrimRight(e.Code, "\n")
		if strings.Contains(cur, line+"\n") || strings.HasSuffix(cur, line) && !strings.Contains(line, "\n") {
			return nil // duplicate directive: no-op
		}
		files[e.File] = insertHeader(cur, line)

	default:
		return fmt.Errorf("unknown op %q", e.Op)
	}
	return nil
}

// insertHeader inserts a directive into the leading header block: after any
// run of comments / blank / #property / #include / #pragma lines at the top.
func insertHeader(content, directive string) string {
	lines := strings.SplitAfter(content, "\n")
	end := 0
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") ||
			strings.HasPrefix(t, "#property") || strings.HasPrefix(t, "#include") || strings.HasPrefix(t, "#pragma") {
			end = i + 1
			continue
		}
		break
	}
	head := strings.Join(lines[:end], "")
	tail := strings.Join(lines[end:], "")
	directive += "\n"
	if head == "" {
		return directive + tail
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return head + directive + tail
}

func join(cur, addition string) string {
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		cur += "\n"
	}
	return cur + terminate(addition)
}

func terminate(s string) string {
	return strings.Trim(s, "\n") + "\n"
}

func snippet(s string) string {
	const n = 64
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
