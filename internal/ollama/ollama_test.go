package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"ok", Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}, false},
		{"empty model", Request{Messages: []Message{{Role: "user", Content: "hi"}}}, true},
		{"no messages", Request{Model: "m"}, true},
		{"streaming", Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}, Stream: true}, true},
	}
	for _, tt := range tests {
		if err := tt.req.Validate(); (err != nil) != tt.wantErr {
			t.Errorf("%s: Validate() error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func okServer(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"message": map[string]string{"role": "assistant", "content": content},
			"done":    true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestChatSuccess(t *testing.T) {
	srv := okServer(`{"answer":42}`)
	defer srv.Close()
	c := New(srv.URL)

	var out struct{ Answer int }
	req := Request{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "?"}},
		Format:   map[string]any{"type": "object"},
		Options:  Options{Temperature: 0, NumCtx: 4096, NumPredict: 16},
	}
	if err := c.ChatJSON(context.Background(), req, &out); err != nil {
		t.Fatalf("ChatJSON() error = %v", err)
	}
	if out.Answer != 42 {
		t.Errorf("Answer = %d, want 42", out.Answer)
	}
}

func TestChatRequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		if got["stream"] != false {
			t.Errorf("stream = %v, want false", got["stream"])
		}
		if got["keep_alive"] != "30m" {
			t.Errorf("keep_alive = %v, want 30m", got["keep_alive"])
		}
		opts, _ := got["options"].(map[string]any)
		if opts["temperature"] != float64(0) || opts["num_ctx"] != float64(4096) {
			t.Errorf("options = %v", opts)
		}
		if _, ok := got["format"]; !ok {
			t.Error("format missing from request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": "{}"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	req := Request{
		Model:     "m",
		Messages:  []Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}},
		Format:    map[string]any{"type": "object"},
		KeepAlive: "30m",
		Options:   Options{NumCtx: 4096},
	}
	var out map[string]any
	if err := c.ChatJSON(context.Background(), req, &out); err != nil {
		t.Fatalf("ChatJSON() error = %v", err)
	}
}

func TestChatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Chat(context.Background(), Request{Model: "missing", Messages: []Message{{Role: "user", Content: "?"}}})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("Chat() error = %v, want http 404 with body snippet", err)
	}
}

func TestChatMalformedEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want *SchemaError", err)
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv := okServer("")
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want *SchemaError for empty content", err)
	}
}

func TestChatContentNotSingleJSONValue(t *testing.T) {
	srv := okServer(`{"a":1} trailing`)
	defer srv.Close()
	c := New(srv.URL)
	var out map[string]any
	err := c.ChatJSON(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}}, &out)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want *SchemaError for trailing data", err)
	}
}

func TestChatConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing listens anymore

	c := New(base)
	_, err := c.Chat(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}})
	if !errors.Is(err, ErrConnection) {
		t.Errorf("error = %v, want ErrConnection", err)
	}
}

func TestChatTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Chat(ctx, Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}})
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("error = %v, want ErrTimeout", err)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	srv := okServer("{}")
	defer srv.Close()
	c := New(srv.URL + "/")
	var out map[string]any
	if err := c.ChatJSON(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}}, &out); err != nil {
		t.Fatalf("ChatJSON() with trailing slash error = %v", err)
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := snippet(long)
	if len(got) != 256+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("snippet(long) length = %d, suffix ok = %v", len(got), strings.HasSuffix(got, "..."))
	}
	if got := snippet(" short "); got != "short" {
		t.Errorf("snippet(short) = %q", got)
	}
}
