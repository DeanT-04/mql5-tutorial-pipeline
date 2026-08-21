package fidelity

import "testing"

const golden = `#property strict
// a comment
#include <Trade\Trade.mqh>
void OnTick() { Print("hi"); }`

func TestStripCommentsAndBlanks(t *testing.T) {
	got := StripCommentsAndBlanks(golden)
	want := []string{`#property strict`, `#include <Trade\Trade.mqh>`, `void OnTick() { Print("hi"); }`}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTokens(t *testing.T) {
	got := Tokens(`if (RSIValue > 70) signal="sell";`)
	want := []string{"if", "(", "RSIValue", ">", "70", ")", "signal", "=", `"sell"`, ";"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestComparePerfect(t *testing.T) {
	r := Compare(golden, golden)
	if r.F1 != 1.0 {
		t.Errorf("F1 = %f, want 1.0", r.F1)
	}
}

func TestCompareMissingLines(t *testing.T) {
	r := Compare(golden, "#property strict\n")
	if r.Recall >= 0.5 || r.Precision != 1.0 {
		t.Errorf("precision=%f recall=%f; want precision 1.0, low recall", r.Precision, r.Recall)
	}
}

func TestCompareHallucination(t *testing.T) {
	r := Compare("#property strict\nint x;", "#property strict\nint x;\nint totallyInvented;")
	if r.Precision >= 0.9 {
		t.Errorf("precision = %f, want penalized for invented code", r.Precision)
	}
}

func TestCompareCaseSensitive(t *testing.T) {
	r := Compare("int RSIValue;", "int rsivalue;")
	if r.F1 >= 1.0 {
		t.Error("case differences must not score as perfect")
	}
}
