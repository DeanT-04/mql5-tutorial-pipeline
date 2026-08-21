// Package ollama is a minimal net/http client for the Ollama chat API,
// supporting schema-constrained ("structured output") requests. All
// model-dependent behavior of the pipeline lives behind this package; tests
// run against httptest servers and never require a running Ollama.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout   = 10 * time.Minute
	maxResponseBytes = 64 << 20
)

// Typed sentinel errors; wrap with %w when produced.
var (
	ErrConnection = errors.New("ollama: connection failed")
	ErrTimeout    = errors.New("ollama: request timed out")
)

// SchemaError reports a 200 response whose payload was not usable
// (malformed envelope or content that violates the requested JSON contract).
type SchemaError struct {
	Detail string
	Body   string
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("ollama: unusable response: %s", e.Detail)
}

// Options are generation parameters (design.md §5: temperature 0 always).
type Options struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
	NumPredict  int     `json:"num_predict"`
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is a chat completion request. Format carries the JSON-schema value
// passed to Ollama's structured-output mode (nil disables it).
type Request struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	Format    any       `json:"format,omitempty"`
	KeepAlive string    `json:"keep_alive,omitempty"`
	Options   Options   `json:"options"`
}

type chatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// Client talks to one Ollama server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for baseURL (e.g. http://localhost:11434).
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: defaultTimeout},
	}
}

// Validate checks a request before it hits the wire.
func (r *Request) Validate() error {
	if r.Model == "" {
		return errors.New("ollama: empty model")
	}
	if len(r.Messages) == 0 {
		return errors.New("ollama: no messages")
	}
	if !r.Stream {
		return nil
	}
	return errors.New("ollama: streaming is not supported by this client")
}

// Chat sends the request and returns the assistant message content.
func (c *Client) Chat(ctx context.Context, r Request) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	body, err := json.Marshal(&r)
	if err != nil {
		return "", fmt.Errorf("ollama: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return "", fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return "", fmt.Errorf("%w: %v", ErrConnection, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("ollama: read response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return "", &SchemaError{Detail: fmt.Sprintf("response exceeds %d bytes", maxResponseBytes)}
	}
	if resp.StatusCode != http.StatusOK {
		snippet := snippet(string(data))
		return "", fmt.Errorf("ollama: http %d: %s", resp.StatusCode, snippet)
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", &SchemaError{Detail: fmt.Sprintf("malformed envelope: %v", err), Body: snippet(string(data))}
	}
	if cr.Message.Content == "" {
		return "", &SchemaError{Detail: "empty message content", Body: snippet(string(data))}
	}
	return cr.Message.Content, nil
}

// ChatJSON is Chat plus strict JSON decoding of the content into out. The
// content must be exactly one JSON value — this is how schema-forced replies
// are consumed.
func (c *Client) ChatJSON(ctx context.Context, r Request, out any) error {
	content, err := c.Chat(ctx, r)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(content))
	if err := dec.Decode(out); err != nil {
		return &SchemaError{Detail: fmt.Sprintf("content is not valid JSON: %v", err), Body: snippet(content)}
	}
	if dec.More() {
		return &SchemaError{Detail: "content contains trailing data after JSON value", Body: snippet(content)}
	}
	return nil
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// snippet shortens a body for error messages.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	const n = 256
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
