package verify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
)

func eaFixture() string {
	return strings.Join([]string{
		"#property copyright \"me\"",
		"#property strict",
		"int OnInit() { return INIT_SUCCEEDED; }",
		"void OnTick() { Print(\"tick\"); }",
		"",
	}, "\n")
}

func TestRunCleanFile(t *testing.T) {
	rep := Run(map[string]string{"MyEA.mq5": eaFixture()})
	if rep.Confidence != 1.0 {
		t.Errorf("confidence = %f, want 1.0 (findings: %+v)", rep.Confidence, rep.Findings)
	}
	if rep.Files[0].ProgramType != "ea" {
		t.Errorf("program type = %q", rep.Files[0].ProgramType)
	}
	if rep.Files[0].Metadata["copyright"] != "me" {
		t.Errorf("metadata = %+v", rep.Files[0].Metadata)
	}
}

func TestDetectProgramType(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"void OnCalculate(...) {}", "indicator"},
		{"int OnInit() {} void OnTick() {}", "ea"},
		{"void OnStart() { Print(1); }", "script"},
		{"just talk", "unknown"},
	}
	for _, tt := range tests {
		if got := detectProgramType(tt.src); got != tt.want {
			t.Errorf("detectProgramType(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestRunMissingEntryPoint(t *testing.T) {
	rep := Run(map[string]string{"A.mq5": "#property strict\nvoid OnTick() {}"})
	found := false
	for _, f := range rep.Findings {
		if f.Check == "entry_points" && f.Severity == SevError && strings.Contains(f.Detail, "OnInit") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %+v, want missing OnInit error", rep.Findings)
	}
	if rep.Confidence > 0.8 {
		t.Errorf("confidence = %f, want reduced", rep.Confidence)
	}
}

func TestRunUnbalancedBraces(t *testing.T) {
	tests := []string{
		"void OnTick() { Print(1); ",
		"void OnTick() ) Print(1); }",
		`void OnTick() { string s = "}"; Print(s); ` + "}", // balanced via string
	}
	for i, src := range tests[:2] {
		rep := Run(map[string]string{"B.mq5": src})
		bad := false
		for _, f := range rep.Findings {
			if f.Check == "balance" {
				bad = true
			}
		}
		if !bad {
			t.Errorf("case %d: no balance finding for %q", i, src)
		}
	}
	okRep := Run(map[string]string{"C.mq5": tests[2]})
	for _, f := range okRep.Findings {
		if f.Check == "balance" {
			t.Errorf("braces-in-string flagged: %+v", f)
		}
	}
}

func TestRunCommentedOutCodeNotFlagged(t *testing.T) {
	src := "#property strict\n// void OnTick() { broken { {\nint OnInit() { return 0; }\nvoid OnStart() {}"
	rep := Run(map[string]string{"A.mq5": src})
	for _, f := range rep.Findings {
		if f.Check == "balance" {
			t.Errorf("commented braces flagged: %+v", f)
		}
	}
}

func TestPropertyStrictWarning(t *testing.T) {
	rep := Run(map[string]string{"A.mq5": "int OnInit() {return 0;} void OnTick(){}"})
	found := false
	for _, f := range rep.Findings {
		if f.Check == "property_strict" && f.Severity == SevWarning {
			found = true
		}
	}
	if !found {
		t.Error("no property_strict warning")
	}
}

func TestArtifacts(t *testing.T) {
	src := "#property strict\nint OnInit() { return 0; } // TODO finish\nvoid OnTick() { int placeholder; }"
	rep := Run(map[string]string{"A.mq5": src})
	kinds := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Check == "artifacts" {
			kinds[f.Detail] = true
		}
	}
	if len(kinds) < 2 {
		t.Errorf("artifact findings = %v, want TODO + placeholder", kinds)
	}
}

func TestTruncation(t *testing.T) {
	rep := Run(map[string]string{"A.mq5": "#property strict\nint OnInit()\n{ return INIT_SUCCEEDED; }\nvoid OnTick() { Print(1)"})
	found := false
	for _, f := range rep.Findings {
		if f.Check == "truncation" {
			found = true
		}
	}
	if !found {
		t.Error("no truncation finding")
	}
	complete := Run(map[string]string{"B.mq5": "#property strict\nvoid OnTick() { Print(1); }"})
	for _, f := range complete.Findings {
		if f.Check == "truncation" {
			t.Errorf("complete file flagged as truncated: %+v", f)
		}
	}
}

func TestExtractMetadata(t *testing.T) {
	src := "// #property copyright \"ignored\"\n#property version \"1.5\"\n#property description \"An EA\"\n/* #property copyright \"block\" */"
	meta := ExtractMetadata(src)
	if meta["copyright"] != "" || meta["version"] != "1.5" || meta["description"] != "An EA" {
		t.Errorf("metadata = %+v", meta)
	}
}

func TestStripCode(t *testing.T) {
	in := "// brace {\n/* } */\nstring s = \"}{\";\nchar c = '}';\nint x = 1;"
	got := stripCode(in)
	if strings.ContainsAny(got, "{}") {
		t.Errorf("stripCode left braces: %q", got)
	}
	if !strings.Contains(got, "int x = 1;") {
		t.Errorf("stripCode dropped code: %q", got)
	}
}

func TestEmptyFilesZeroConfidence(t *testing.T) {
	rep := Run(map[string]string{})
	if rep.Confidence != 0 || len(rep.Files) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestMultipleFilesAverage(t *testing.T) {
	files := map[string]string{
		"A.mq5": eaFixture(),
		"B.mq5": "int OnInit() {return 0;} void OnTick(){}", // missing strict -> warning
	}
	rep := Run(files)
	want := (1.0 + 0.95) / 2
	if diff := rep.Confidence - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("confidence = %f, want %f", rep.Confidence, want)
	}
}

func TestRunLLMCheckMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := `{"match":false,"missing":["int gone;"],"extra":[],"notes":"diff"}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": content},
			"done":    true,
		})
	}))
	defer srv.Close()

	files := map[string]string{"MyEA.mq5": eaFixture()}
	rep := Run(files)
	err := RunLLM(context.Background(), rep, files,
		[]events.Event{{File: "MyEA.mq5", Code: "int gone;"}},
		"m", srv.URL, "30m", 4096)
	if err != nil {
		t.Fatalf("RunLLM() error = %v", err)
	}
	if rep.Confidence >= 1.0 {
		t.Errorf("confidence = %f, want reduced after mismatch", rep.Confidence)
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Check == "llm_check" {
			found = true
		}
	}
	if !found {
		t.Error("no llm_check finding")
	}
}

func TestMarshalValidJSON(t *testing.T) {
	data, err := Marshal(Run(map[string]string{"A.mq5": eaFixture()}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("report not valid JSON: %v", err)
	}
}
