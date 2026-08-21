package prompts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemPromptsByteStable(t *testing.T) {
	if System == "" || DeepSystem == "" {
		t.Fatal("system prompts must not be empty")
	}
	a, b := TriageUser("c0001", "text one"), TriageUser("c0002", "very different text")
	if !strings.HasPrefix(a, "Classify this chunk.") || !strings.HasPrefix(b, "Classify this chunk.") {
		t.Error("triage user messages lost their shared prefix")
	}
	c, d := DeepUser("c0001", "x", ""), DeepUser("c0009", "totally different", "")
	if !strings.HasPrefix(c, "Extract code events from this chunk.") ||
		!strings.HasPrefix(d, "Extract code events from this chunk.") {
		t.Error("deep user messages lost their shared prefix")
	}
	if strings.Contains(System, "%") && strings.Contains(System, "s") {
		t.Log("note: system prompt contains format-like sequences; ensure no interpolation")
	}
}

func TestSchemasAreValidJSON(t *testing.T) {
	for name, schema := range map[string]any{
		"triage": TriageSchema(),
		"deep":   DeepSchema(),
	} {
		data, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s schema: %v", name, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s schema not a JSON object: %v", name, err)
		}
		if _, ok := m["properties"]; !ok {
			t.Errorf("%s schema missing properties", name)
		}
	}
}

func TestDeepSchemaEnum(t *testing.T) {
	data, _ := json.Marshal(DeepSchema())
	s := string(data)
	for _, op := range []string{"create", "append", "replace", "property", "include"} {
		if !strings.Contains(s, op) {
			t.Errorf("deep schema missing op %q", op)
		}
	}
}

func TestUserMessagesContainChunk(t *testing.T) {
	got := TriageUser("c0017", `the "chunk" text`)
	if !strings.Contains(got, "c0017") || !strings.Contains(got, `the "chunk" text`) {
		t.Errorf("TriageUser() = %q", got)
	}
	got = DeepUser("c0042", "body", "")
	if !strings.Contains(got, "c0042") || !strings.Contains(got, "body") {
		t.Errorf("DeepUser() = %q", got)
	}
	if strings.Contains(got, "Previous chunk") {
		t.Error("DeepUser with empty context must not mention context")
	}
	withCtx := DeepUser("c0042", "body", "earlier words")
	if !strings.Contains(withCtx, "earlier words") || !strings.Contains(withCtx, "context only") {
		t.Errorf("DeepUser with context = %q", withCtx)
	}
	if !strings.HasPrefix(withCtx, "Extract code events from this chunk.\n\n") {
		t.Error("DeepUser lost its stable head prefix")
	}
}
