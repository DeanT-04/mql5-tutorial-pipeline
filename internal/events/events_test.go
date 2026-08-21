package events

import (
	"bytes"
	"strings"
	"testing"
)

func validEvent() Event {
	return Event{ChunkID: "c0017", Seq: 1, Op: OpAppend, File: "MyEA.mq5", Code: "input double LotSize = 0.10;"}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{"valid append", func(*Event) {}, false},
		{"valid create", func(e *Event) { e.Op = OpCreate }, false},
		{"valid replace", func(e *Event) { e.Op = OpReplace; e.Anchor = "old line" }, false},
		{"replace without anchor", func(e *Event) { e.Op = OpReplace }, true},
		{"replace blank anchor", func(e *Event) { e.Op = OpReplace; e.Anchor = "  " }, true},
		{"invalid op", func(e *Event) { e.Op = "delete" }, true},
		{"empty chunk id", func(e *Event) { e.ChunkID = "" }, true},
		{"zero seq", func(e *Event) { e.Seq = 0 }, true},
		{"negative seq", func(e *Event) { e.Seq = -2 }, true},
		{"empty file", func(e *Event) { e.File = "" }, true},
		{"file with directory", func(e *Event) { e.File = `dir\MyEA.mq5` }, true},
		{"file with slash", func(e *Event) { e.File = "sub/MyEA.mq5" }, true},
		{"file traversal", func(e *Event) { e.File = "../MyEA.mq5" }, true},
		{"wrong extension", func(e *Event) { e.File = "notes.txt" }, true},
		{"bare dotfile", func(e *Event) { e.File = ".mq5" }, true},
		{"empty code", func(e *Event) { e.Code = "" }, true},
	}
	for _, tt := range tests {
		e := validEvent()
		tt.mutate(&e)
		err := e.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: Validate() error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestValidateIncludeProperty(t *testing.T) {
	for _, op := range []Op{OpProperty, OpInclude} {
		e := Event{ChunkID: "c0001", Seq: 1, Op: op, File: "MyEA.mq5", Code: "#property strict"}
		if err := e.Validate(); err != nil {
			t.Errorf("op %s: Validate() error = %v", op, err)
		}
	}
}

func TestParseDeep(t *testing.T) {
	content := `{"events":[
		{"chunk_id":"WRONG","seq":0,"op":"create","file":"A.mq5","code":"// x"},
		{"chunk_id":"WRONG","seq":9,"op":"append","file":"A.mq5","code":"int y;"}
	]}`
	got, err := ParseDeep("c0003", content)
	if err != nil {
		t.Fatalf("ParseDeep() error = %v", err)
	}
	if got[0].ChunkID != "c0003" || got[0].Seq != 1 {
		t.Errorf("first event = %+v, want re-anchored chunk c0003 seq 1", got[0])
	}
	if got[1].ChunkID != "c0003" || got[1].Seq != 9 {
		t.Errorf("second event = %+v, want kept seq 9", got[1])
	}
}

func TestParseDeepErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"not json", "{oops"},
		{"trailing data", `{"events":[]} extra`},
		{"invalid event", `{"events":[{"chunk_id":"x","seq":1,"op":"bogus","file":"A.mq5","code":"c"}]}`},
		{"missing code", `{"events":[{"chunk_id":"x","seq":1,"op":"append","file":"A.mq5"}]}`},
	}
	for _, tt := range tests {
		if _, err := ParseDeep("c0001", tt.content); err == nil {
			t.Errorf("%s: ParseDeep() error = nil, want error", tt.name)
		}
	}
}

func TestParseDeepEmptyEvents(t *testing.T) {
	got, err := ParseDeep("c0001", `{"events":[]}`)
	if err != nil {
		t.Fatalf("ParseDeep() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("events = %+v, want empty", got)
	}
}

func TestJSONLRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	recs := []any{
		validEvent(),
		Event{ChunkID: "c0018", Seq: 2, Op: OpReplace, File: "MyEA.mq5", Anchor: "int x;", Code: "int x = 5;"},
		Failed{ChunkID: "c0019", Error: "schema mismatch after 2 retries"},
	}
	for _, r := range recs {
		if err := AppendJSONL(&buf, r); err != nil {
			t.Fatalf("AppendJSONL(%T) error = %v", r, err)
		}
	}

	var got []any
	err := Reader(bytes.NewReader(buf.Bytes()), func(rec any) error {
		switch rec.(type) {
		case Event, Failed:
			got = append(got, rec)
		default:
			t.Fatalf("unexpected record type %T", rec)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Reader() error = %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("got %d records, want %d (%+v)", len(got), len(recs), got)
	}
	if f, ok := got[2].(Failed); !ok || f.ChunkID != "c0019" || f.Error == "" {
		t.Errorf("record 3 = %+v, want Failed marker", got[2])
	}
}

func TestReaderSkipsBlanksAndComments(t *testing.T) {
	var buf bytes.Buffer
	if err := AppendJSONL(&buf, validEvent()); err != nil {
		t.Fatal(err)
	}
	input := "\n# a comment\n" + buf.String() + "\n"
	count := 0
	if err := Reader(strings.NewReader(input), func(rec any) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("Reader() error = %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestReaderRejectsInvalidRecords(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{"bad json", []string{`{not json`}},
		{"event failing validation", []string{`{"chunk_id":"","seq":1,"op":"append","file":"A.mq5","code":"x"}`}},
		{"unknown record", []string{`{"something":"else"}`}},
	}
	for _, tt := range tests {
		err := Reader(strings.NewReader(strings.Join(tt.lines, "\n")), func(any) error { return nil })
		if err == nil {
			t.Errorf("%s: Reader() error = nil, want error", tt.name)
		}
	}
}

func TestAppendJSONLError(t *testing.T) {
	if err := AppendJSONL(&bytes.Buffer{}, make(chan int)); err == nil {
		t.Error("AppendJSONL(unmarshalable) error = nil, want error")
	}
}
