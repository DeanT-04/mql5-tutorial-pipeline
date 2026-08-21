# spec.md — mql5-tutorial-pipeline

Buildable specification derived from `design.md`. Where this file and `design.md`
disagree, this file wins.

## 1. Product

A Go CLI that turns one YouTube MQL5 tutorial video into exact `.mq5` source files,
using a small local LLM (Ollama) only for narrow, schema-constrained extraction.
All assembly and verification is deterministic Go code.

### Targets

| Metric | Requirement |
|---|---|
| Code fidelity | ≥95% vs. the tutorial's final code |
| Processing time | ≤15 min per 20–80 min video on captions path |
| Supervision | Unattended once started; resumable at any stage |

## 2. CLI surface

Single orchestrating binary; each stage also runnable standalone.

```
go run ./cmd/pipeline <youtube-url> [flags]
go run ./cmd/fetch     <youtube-url> [--runs-dir DIR]
go run ./cmd/segment   --run DIR
go run ./cmd/extract   --run DIR [--fast]
go run ./cmd/assemble  --run DIR
go run ./cmd/verify    --run DIR [--llm-check]
```

Global flags:

| Flag | Default | Meaning |
|---|---|---|
| `--config FILE` | `pipeline.yaml` | Config file path |
| `--transcript-mode MODE` | `auto` | `auto \| captions \| whisper` |
| `--fast` | off | Use fast model (`models.fast`) |
| `--force` | off | Ignore cached stage outputs, re-run all |
| `--workers N` | from config | Parallel Ollama calls |

Exit codes: `0` ok · `1` runtime error · `2` usage error.
Data → stdout, diagnostics → stderr.

## 3. Run store layout

Every run lives in `runs/<video-id>/`. Stage outputs are content-addressed by the
SHA-256 of their inputs (recorded in `manifest.json`); re-running skips stages whose
input hash is unchanged unless `--force`.

```
runs/<video-id>/
  manifest.json        # video id, url, title, per-stage {input-hash, status, finished}
  transcript.json      # [{start, end, text}] seconds float64
  chunks.json          # [{id, start, end, text}]
  triage.jsonl         # {"chunk_id","has_code_action","confidence"}
  events.jsonl         # extraction events (§5)
  out/*.mq5            # assembled files
  assembly-report.json # ops applied / skipped / conflicts
  report.json          # verify verdict + confidence score
```

## 4. Stage contracts

### 4.1 fetch — `internal/ytdlp`, `internal/transcript`

- Invoke `yt-dlp` / `ffmpeg` via `exec.Command` argument lists only (URLs are untrusted).
- Subtitle preference: manual `en` → auto-generated `en` → audio fallback.
- Audio fallback: m4a 48 kbps → faster-whisper `small`, `int8`, VAD filter,
  `cpu_threads=8`. Warn on stderr that this may exceed the time budget.
- Output `transcript.json`: array of `{ "start": float, "end": float, "text": string }`,
  sorted, non-overlapping, normalized whitespace.
- Failure with no captions and whisper unavailable → exit 1 with actionable message.

### 4.2 segment — `internal/segment`

Merge caption lines into chunks using:
1. silence gap > 1.5 s → new chunk;
2. code-action cue opening a line ("let's add", "next line", "so now we", "type this",
   list in `internal/segment/cues.go`, extensible via config) → new chunk;
3. caps: > ~350 tokens or > 45 s → force split.

Chunk IDs deterministic: `c0001`, `c0002`, … Same transcript in ⇒ byte-identical
`chunks.json` out.

### 4.3 extract — `internal/ollama`, `internal/prompts`, `internal/events`

**Pass A (triage).** Per chunk, JSON-schema-forced output
`{"chunk_id": string, "has_code_action": bool, "confidence": number}`.
`num_predict ≤ 16`, temperature 0. Low-confidence positives pass through (fail open).

**Pass B (deep).** Triaged-positive chunks only. Emits events:

```json
{ "chunk_id": "c0017", "seq": 1, "op": "append",
  "file": "MyEA.mq5", "anchor": null,
  "code": "input double LotSize = 0.10;" }
```

- Ops: `create | append | replace | property | include`.
- `replace` requires non-empty `anchor` matching current file state exactly.
- Validation failure → 2 retries → chunk marked `"failed"` in `events.jsonl`
  as `{"chunk_id": "...", "error": "..."}`; run continues.

Prompt rules (enforced in `internal/prompts`):
- System prompt + few-shot block form a **byte-stable prefix**, identical across all
  calls of a pass (KV-cache prefix reuse). Chunk text goes last, never interpolated
  into the prefix.
- All requests use Ollama structured output (`format` = JSON schema), `temperature 0`,
  `num_ctx 4096`; `num_predict` 512 (deep) / 16 (triage).

Ollama client: plain `net/http`, no SDK. Timeouts + bounded response reads.
Concurrency default 2 workers.

### 4.4 assemble — `internal/assemble`

Pure replay of events in `(chunk_id, seq)` order onto an in-memory map.
- `create`: fails if file already exists in map (reported, not fatal).
- `append`: appends with newline discipline (no leading blank lines, trailing newline).
- `replace`: anchor must occur exactly once in current file state; zero or multiple
  matches → conflict recorded, op skipped, never guessed.
- `property` / `include`: inserted into the header block after `#property` lines.
Output: `out/*.mq5` + `assembly-report.json`.

### 4.5 verify — `internal/verify`

Static checks (no model): brace/paren balance, `#property strict` presence, expected
entry points per program type (EA: `OnInit`+`OnTick`; indicator: `OnCalculate`;
script: `OnStart`), suspicious artifacts (placeholder text, truncated last line),
metadata extraction mirroring the sibling extractor's semantics.

Optional `--llm-check`: single model call comparing assembled file vs concatenated
code events → diff-style verdict JSON.

Output `report.json`:
```json
{ "files": [...], "findings": [{"file","severity","check","detail"}],
  "confidence": 0.87 }
```
Weighted confidence: static findings deduct fixed weights; `llm-check` verdict adjusts.
Pipeline exits 1 if any file scores below threshold (default 0.6, configurable).

## 5. Config — `pipeline.yaml`

```yaml
models:
  primary: qwen2.5-coder:3b-instruct
  fast:    qwen2.5-coder:1.5b
ollama:
  url: http://localhost:11434
  num_ctx: 4096
  keep_alive: 30m
segment:
  max_tokens: 350
  max_seconds: 45
  pause_gap: 1.5s
extract:
  workers: 2
  retries: 2
verify:
  min_confidence: 0.6
paths:
  runs_dir: runs
```

Flags override config; config overrides built-in defaults. Stdlib YAML parsing is not
available — use a minimal hand-rolled parser for this flat-ish schema or vendor nothing:
**zero third-party deps** is a hard rule, so implement a tiny purpose-built reader in
`internal/cfg` (documented format above).

## 6. Conventions (hard rules)

- Latest Go only; **stdlib only** — no third-party modules, including YAML libs.
- Table-driven tests; `-race` clean (plain-test fallback when cgo unavailable, same as
  sibling repo); every exported function tested incl. edge cases.
- Bounded reads everywhere (`bufio.Scanner` caps); no unbounded input.
- Errors wrapped with `%w`; never ignored.
- External binaries: argument-list `exec.Command` only, never shell strings.
- Full dev loop green without network or Ollama: Ollama client tested against
  `httptest`; mini-pipeline test runs on bundled fixture transcript + scripted fake server.
- Model-dependent behavior isolated behind `internal/ollama`.
- `*.sh` forced LF via `.gitattributes`; golangci-lint v2 (`standard` + `gosec`);
  gofmt + goimports.

## 7. Acceptance criteria

1. `./bash/dev.sh` passes end-to-end with no Ollama running and no network.
2. Fixture run: bundled mini-transcript → `out/fixture.mq5` byte-equal to golden file.
3. Resume: deleting `events.jsonl` and re-running `pipeline` re-does extract but skips
   fetch/segment (manifest hashes unchanged).
4. A captioned real video ≤20 min completes unattended within budget on target hardware.
5. Malformed URL, missing yt-dlp, dead Ollama → clean exit codes + actionable stderr,
   no panics, no partial writes outside the run dir.
