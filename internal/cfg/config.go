// Package cfg loads pipeline configuration: built-in defaults, overridden by
// pipeline.yaml, overridden by CLI flags. The YAML subset parser is
// purpose-built (two-level "section: key: value" schema, spec.md §5) because
// this project admits zero third-party dependencies.
package cfg

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxLineBytes = 64 << 10
	maxFileBytes = 1 << 20
)

type Models struct {
	Primary string
	Fast    string
}

type Ollama struct {
	URL       string
	NumCtx    int
	KeepAlive time.Duration
}

type Segment struct {
	MaxTokens  int
	MaxSeconds int
	PauseGap   time.Duration
}

type Extract struct {
	Workers int
	Retries int
}

type Verify struct {
	MinConfidence float64
}

type Paths struct {
	RunsDir string
}

type Config struct {
	Models  Models
	Ollama  Ollama
	Segment Segment
	Extract Extract
	Verify  Verify
	Paths   Paths
}

// Overrides carries flag-provided values; nil fields leave the config untouched.
type Overrides struct {
	Workers       *int
	MinConfidence *float64
	RunsDir       *string
}

func Default() *Config {
	return &Config{
		Models: Models{
			Primary: "qwen2.5-coder:3b-instruct",
			Fast:    "qwen2.5-coder:1.5b",
		},
		Ollama: Ollama{
			URL:       "http://localhost:11434",
			NumCtx:    4096,
			KeepAlive: 30 * time.Minute,
		},
		Segment: Segment{
			MaxTokens:  350,
			MaxSeconds: 45,
			PauseGap:   1500 * time.Millisecond,
		},
		Extract: Extract{
			Workers: 2,
			Retries: 2,
		},
		Verify: Verify{
			MinConfidence: 0.6,
		},
		Paths: Paths{
			RunsDir: "runs",
		},
	}
}

// Load reads path ("" means defaults only), merges it over the defaults and
// validates the result. A missing or unreadable file is an error.
func Load(path string) (*Config, error) {
	c := Default()
	if path == "" {
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		return c, nil
	}
	f, err := os.Open(path) // #nosec G304 -- path comes from the --config flag by design
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sections, err := parse(f)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	if err := c.apply(sections); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return c, nil
}

// Apply mutates c with flag-provided overrides.
func (c *Config) Apply(o Overrides) {
	if o.Workers != nil {
		c.Extract.Workers = *o.Workers
	}
	if o.MinConfidence != nil {
		c.Verify.MinConfidence = *o.MinConfidence
	}
	if o.RunsDir != nil {
		c.Paths.RunsDir = *o.RunsDir
	}
}

// section is one top-level block of the config file.
type section struct {
	name string
	keys map[string]string
}

var knownSections = map[string]map[string]bool{
	"models":  {"primary": true, "fast": true},
	"ollama":  {"url": true, "num_ctx": true, "keep_alive": true},
	"segment": {"max_tokens": true, "max_seconds": true, "pause_gap": true},
	"extract": {"workers": true, "retries": true},
	"verify":  {"min_confidence": true},
	"paths":   {"runs_dir": true},
}

func parse(r io.Reader) ([]section, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)

	var secs []section
	var cur *section
	total := 0
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := sc.Text()
		total += len(line)
		if total > maxFileBytes {
			return nil, fmt.Errorf("line %d: file exceeds %d bytes", lineNo, maxFileBytes)
		}
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, ok := strings.Cut(text, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\", got %q", lineNo, text)
		}
		key = strings.TrimSpace(key)
		val = stripComment(strings.TrimSpace(val))
		if val == "" {
			if key == "" || strings.ContainsAny(key, " \t") {
				return nil, fmt.Errorf("line %d: empty key", lineNo)
			}
			for _, s := range secs {
				if s.name == key {
					return nil, fmt.Errorf("line %d: duplicate section %q", lineNo, key)
				}
			}
			if _, ok := knownSections[key]; !ok {
				return nil, fmt.Errorf("line %d: unknown section %q", lineNo, key)
			}
			secs = append(secs, section{name: key, keys: map[string]string{}})
			cur = &secs[len(secs)-1]
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: %q before any section header", lineNo, key)
		}
		if !knownSections[cur.name][key] {
			return nil, fmt.Errorf("line %d: unknown key %q in section %q", lineNo, key, cur.name)
		}
		if _, dup := cur.keys[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q in section %q", lineNo, key, cur.name)
		}
		cur.keys[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return secs, nil
}

// stripComment removes an inline "# ..." comment (a '#' preceded by whitespace).
func stripComment(val string) string {
	if i := strings.Index(val, " #"); i >= 0 {
		return strings.TrimSpace(val[:i])
	}
	return val
}

func (c *Config) apply(secs []section) error {
	for _, sec := range secs {
		var err error
		switch sec.name {
		case "models":
			err = applyStr(sec.keys, map[string]*string{
				"primary": &c.Models.Primary,
				"fast":    &c.Models.Fast,
			})
		case "ollama":
			if v, ok := sec.keys["url"]; ok {
				c.Ollama.URL = v
			}
			if v, ok := sec.keys["num_ctx"]; ok {
				if c.Ollama.NumCtx, err = atoi(v); err != nil {
					return fmt.Errorf("ollama.num_ctx: %w", err)
				}
			}
			if v, ok := sec.keys["keep_alive"]; ok {
				if c.Ollama.KeepAlive, err = adur(v); err != nil {
					return fmt.Errorf("ollama.keep_alive: %w", err)
				}
			}
		case "segment":
			if v, ok := sec.keys["max_tokens"]; ok {
				if c.Segment.MaxTokens, err = atoi(v); err != nil {
					return fmt.Errorf("segment.max_tokens: %w", err)
				}
			}
			if v, ok := sec.keys["max_seconds"]; ok {
				if c.Segment.MaxSeconds, err = atoi(v); err != nil {
					return fmt.Errorf("segment.max_seconds: %w", err)
				}
			}
			if v, ok := sec.keys["pause_gap"]; ok {
				if c.Segment.PauseGap, err = adur(v); err != nil {
					return fmt.Errorf("segment.pause_gap: %w", err)
				}
			}
		case "extract":
			if v, ok := sec.keys["workers"]; ok {
				if c.Extract.Workers, err = atoi(v); err != nil {
					return fmt.Errorf("extract.workers: %w", err)
				}
			}
			if v, ok := sec.keys["retries"]; ok {
				if c.Extract.Retries, err = atoi(v); err != nil {
					return fmt.Errorf("extract.retries: %w", err)
				}
			}
		case "verify":
			if v, ok := sec.keys["min_confidence"]; ok {
				if c.Verify.MinConfidence, err = atof(v); err != nil {
					return fmt.Errorf("verify.min_confidence: %w", err)
				}
			}
		case "paths":
			if v, ok := sec.keys["runs_dir"]; ok {
				c.Paths.RunsDir = v
			}
		default:
			return fmt.Errorf("unknown section %q", sec.name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyStr(keys map[string]string, targets map[string]*string) error {
	for k, v := range keys {
		t, ok := targets[k]
		if !ok {
			return fmt.Errorf("unknown key %q", k)
		}
		*t = v
	}
	return nil
}

func (c *Config) validate() error {
	if c.Models.Primary == "" || c.Models.Fast == "" {
		return fmt.Errorf("models: primary and fast must be non-empty")
	}
	if !strings.HasPrefix(c.Ollama.URL, "http://") && !strings.HasPrefix(c.Ollama.URL, "https://") {
		return fmt.Errorf("ollama.url must be an http(s) URL, got %q", c.Ollama.URL)
	}
	if c.Ollama.NumCtx < 512 || c.Ollama.NumCtx > 32768 {
		return fmt.Errorf("ollama.num_ctx must be in [512, 32768], got %d", c.Ollama.NumCtx)
	}
	if c.Ollama.KeepAlive <= 0 {
		return fmt.Errorf("ollama.keep_alive must be positive, got %s", c.Ollama.KeepAlive)
	}
	if c.Segment.MaxTokens <= 0 {
		return fmt.Errorf("segment.max_tokens must be positive, got %d", c.Segment.MaxTokens)
	}
	if c.Segment.MaxSeconds <= 0 {
		return fmt.Errorf("segment.max_seconds must be positive, got %d", c.Segment.MaxSeconds)
	}
	if c.Segment.PauseGap <= 0 {
		return fmt.Errorf("segment.pause_gap must be positive, got %s", c.Segment.PauseGap)
	}
	if c.Extract.Workers < 1 || c.Extract.Workers > 8 {
		return fmt.Errorf("extract.workers must be in [1, 8], got %d", c.Extract.Workers)
	}
	if c.Extract.Retries < 0 || c.Extract.Retries > 5 {
		return fmt.Errorf("extract.retries must be in [0, 5], got %d", c.Extract.Retries)
	}
	if c.Verify.MinConfidence < 0 || c.Verify.MinConfidence > 1 {
		return fmt.Errorf("verify.min_confidence must be in [0, 1], got %f", c.Verify.MinConfidence)
	}
	if c.Paths.RunsDir == "" {
		return fmt.Errorf("paths.runs_dir must be non-empty")
	}
	return nil
}

func atoi(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not an integer: %q", s)
	}
	return n, nil
}

func atof(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	return f, nil
}

func adur(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("not a duration: %q", s)
	}
	return d, nil
}
