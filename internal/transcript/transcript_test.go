package transcript

import (
	"math"
	"reflect"
	"testing"
)

func TestFromJSON3(t *testing.T) {
	data := []byte(`{"events":[
		{"tStartMs":1000,"dDurationMs":2500,"segs":[{"utf8":"let's "},{"utf8":"add a variable"}]},
		{"tStartMs":4000,"dDurationMs":1500,"segs":[{"utf8":"next line"}]},
		{"tStartMs":5000,"aAppend":1},
		{"tStartMs":6000}
	]}`)
	got, err := FromJSON3(data)
	if err != nil {
		t.Fatalf("FromJSON3() error = %v", err)
	}
	want := []Line{
		{Start: 1, End: 3.5, Text: "let's add a variable"},
		{Start: 4, End: 5.5, Text: "next line"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromJSON3() = %+v, want %+v", got, want)
	}
}

func TestFromJSON3Invalid(t *testing.T) {
	if _, err := FromJSON3([]byte("{broken")); err == nil {
		t.Error("FromJSON3(invalid) error = nil, want error")
	}
}

func TestFromWhisperJSON(t *testing.T) {
	data := []byte(`[
		{"start":0.0,"end":2.5,"text":" OnInit "},
		{"start":2.5,"end":4.0,"text":"returns int"}
	]`)
	got, err := FromWhisperJSON(data)
	if err != nil {
		t.Fatalf("FromWhisperJSON() error = %v", err)
	}
	want := []Line{
		{Start: 0, End: 2.5, Text: " OnInit "},
		{Start: 2.5, End: 4, Text: "returns int"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromWhisperJSON() = %+v, want %+v", got, want)
	}
	if _, err := FromWhisperJSON([]byte("[not json")); err == nil {
		t.Error("FromWhisperJSON(invalid) error = nil, want error")
	}
}

func TestNormalize(t *testing.T) {
	in := []Line{
		{Start: 5, End: 8, Text: "  second\tline  "},
		{Start: 1, End: 6.5, Text: "first   overlaps\nsecond"},
		{Start: 9, End: 10, Text: "   \n\t  "},
		{Start: -1, End: 0.5, Text: "zeroth"},
	}
	got := Normalize(in)
	want := []Line{
		{Start: -1, End: 0.5, Text: "zeroth"},
		{Start: 1, End: 5, Text: "first overlaps second"},
		{Start: 5, End: 8, Text: "second line"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize() = %+v, want %+v", got, want)
	}
	for i, l := range got {
		if math.IsNaN(l.Start) || math.IsInf(l.Start, 0) {
			t.Errorf("line %d has non-finite start", i)
		}
	}
}

func TestNormalizeClampNegativeDuration(t *testing.T) {
	in := []Line{
		{Start: 3, End: 3, Text: "a"},
		{Start: 1, End: 9, Text: "b"},
	}
	got := Normalize(in)
	want := []Line{
		{Start: 1, End: 3, Text: "b"},
		{Start: 3, End: 3, Text: "a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize() = %+v, want %+v", got, want)
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	lines := []Line{{Start: 1.5, End: 2.5, Text: "hello world"}}
	data, err := Marshal(lines)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, lines) {
		t.Errorf("round trip = %+v, want %+v", got, lines)
	}
}

func TestUnmarshalError(t *testing.T) {
	if _, err := Unmarshal([]byte("{")); err == nil {
		t.Error("Unmarshal(corrupt) error = nil, want error")
	}
}
