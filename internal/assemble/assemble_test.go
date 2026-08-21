package assemble

import (
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
)

func ev(chunk string, seq int, op events.Op, file, code string) events.Event {
	return events.Event{ChunkID: chunk, Seq: seq, Op: op, File: file, Code: code}
}

func TestCreateAndAppend(t *testing.T) {
	res := Run([]events.Event{
		ev("c0002", 1, events.OpAppend, "MyEA.mq5", "int y;"),
		ev("c0001", 1, events.OpCreate, "MyEA.mq5", "#property strict\nint x;"),
	})
	if res.Skipped != 0 || res.Applied != 2 {
		t.Fatalf("applied=%d skipped=%d records=%+v", res.Applied, res.Skipped, res.Records)
	}
	got := res.Files["MyEA.mq5"]
	want := "#property strict\nint x;\nint y;\n"
	if got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestInputOrderIrrelevant(t *testing.T) {
	a := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "// one"),
		ev("c0002", 1, events.OpAppend, "A.mq5", "// two"),
	})
	b := Run([]events.Event{
		ev("c0002", 1, events.OpAppend, "A.mq5", "// two"),
		ev("c0001", 1, events.OpCreate, "A.mq5", "// one"),
	})
	if a.Files["A.mq5"] != b.Files["A.mq5"] {
		t.Errorf("replay order changed output: %q vs %q", a.Files["A.mq5"], b.Files["A.mq5"])
	}
}

func TestAppendWithoutCreateSkipped(t *testing.T) {
	res := Run([]events.Event{ev("c0001", 1, events.OpAppend, "Ghost.mq5", "int x;")})
	if res.Skipped != 1 || len(res.Files) != 0 {
		t.Fatalf("skipped=%d files=%v", res.Skipped, res.Files)
	}
	if !strings.Contains(res.Records[0].Detail, "does not exist") {
		t.Errorf("detail = %q", res.Records[0].Detail)
	}
}

func TestDuplicateCreateSkipped(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "int x;"),
		ev("c0002", 1, events.OpCreate, "A.mq5", "int x;"),
	})
	if res.Skipped != 1 || !strings.Contains(res.Records[1].Detail, "already exists") {
		t.Fatalf("records = %+v", res.Records)
	}
}

func TestNewlineDiscipline(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "\n\nint x;\n\n"), // trailing/leading blanks stripped
		ev("c0002", 1, events.OpAppend, "A.mq5", "int y;"),
		ev("c0003", 1, events.OpAppend, "A.mq5", "\nint z;"),
	})
	got := res.Files["A.mq5"]
	want := "int x;\nint y;\nint z;\n"
	if got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestReplaceUniqueAnchor(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "input double LotSize = 0.10;\nvoid OnTick() {}"),
		ev("c0002", 1, events.OpReplace, "A.mq5", "input double LotSize = 0.50;").WithAnchor("input double LotSize = 0.10;"),
	})
	got := res.Files["A.mq5"]
	if got != "input double LotSize = 0.50;\nvoid OnTick() {}\n" {
		t.Errorf("file = %q", got)
	}
	if res.Skipped != 0 {
		t.Errorf("skipped = %d", res.Skipped)
	}
}

func TestReplaceMissingAnchor(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "int x;"),
		ev("c0002", 1, events.OpReplace, "A.mq5", "int y;").WithAnchor("int gone;"),
	})
	if res.Skipped != 1 || !strings.Contains(res.Records[1].Detail, "not found") {
		t.Fatalf("records = %+v", res.Records)
	}
	if res.Files["A.mq5"] != "int x;\n" {
		t.Errorf("original must be untouched, got %q", res.Files["A.mq5"])
	}
}

func TestReplaceAmbiguousAnchor(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "Print(1);\nPrint(1);"),
		ev("c0002", 1, events.OpReplace, "A.mq5", "Print(2);").WithAnchor("Print(1);"),
	})
	if res.Skipped != 1 || !strings.Contains(res.Records[1].Detail, "occurs 2 times") {
		t.Fatalf("records = %+v", res.Records)
	}
	if strings.Contains(res.Files["A.mq5"], "Print(2)") {
		t.Error("ambiguous anchor must not be applied")
	}
}

func TestPropertyIncludeHeaderInsertion(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "#property strict"),
		ev("c0002", 1, events.OpProperty, "A.mq5", "#property version \"1.00\""),
		ev("c0003", 1, events.OpInclude, "A.mq5", "#include <Trade\\Trade.mqh>"),
		ev("c0004", 1, events.OpAppend, "A.mq5", "int x;"),
	})
	got := res.Files["A.mq5"]
	want := "#property strict\n#property version \"1.00\"\n#include <Trade\\Trade.mqh>\nint x;\n"
	if got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestDirectiveInsertionIntoBodyGoesToHeader(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "#property strict\nint x;\nint y;"),
		ev("c0002", 1, events.OpProperty, "A.mq5", "#property copyright \"me\""),
	})
	got := res.Files["A.mq5"]
	want := "#property strict\n#property copyright \"me\"\nint x;\nint y;\n"
	if got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestDuplicateDirectiveNoop(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "#property strict"),
		ev("c0002", 1, events.OpProperty, "A.mq5", "#property strict"),
	})
	got := res.Files["A.mq5"]
	if got != "#property strict\n" {
		t.Errorf("file = %q, want deduplicated directive", got)
	}
	if res.Applied != 2 {
		t.Errorf("applied = %d (dedupe counts as applied no-op)", res.Applied)
	}
}

func TestEmptyEvents(t *testing.T) {
	res := Run(nil)
	if res.Applied != 0 || res.Skipped != 0 || len(res.Files) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestMultiFileIsolation(t *testing.T) {
	res := Run([]events.Event{
		ev("c0001", 1, events.OpCreate, "A.mq5", "int a;"),
		ev("c0002", 1, events.OpCreate, "B.mq5", "int b;"),
	})
	if res.Files["A.mq5"] != "int a;\n" || res.Files["B.mq5"] != "int b;\n" {
		t.Errorf("files = %+v", res.Files)
	}
}
