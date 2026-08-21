# design.md — mql5-tutorial-pipeline

## 1. Purpose

Turn YouTube MQL5 tutorial videos into exact `.mq5` code replicas — without manually
re-watching and transcribing. Feed in a URL, get copy-pasteable code out.

**Hard targets**

| Metric | Target |
|---|---|
| Code fidelity | ≥95% vs. what the tutorial actually produces |
| Processing time | ≤15 min per video (20–80 min videos), LLM stage |
| Supervision | Fully unattended once started |

## 2. Measured environment (target machine)

| Component | Value | Consequence |
|---|---|---|
| CPU | Ryzen 7 PRO 3700U (4C/8T) | Inference host; modest throughput |
| RAM | 15 GB | Models up to ~4B Q4 fit comfortably |
| GPU | AMD Vega 10 iGPU, 2 GB | **Unusable by Ollama** (no CUDA, ROCm lacks Vega iGPU) → **CPU-only inference** |
| yt-dlp | 2026.07.04 ✅ | Ingest solved |
| ffmpeg | 9.0 ✅ | Audio extraction solved |
| Python | 3.12 ✅ | Host for faster-whisper |
| Ollama / Whisper | ❌ missing | Installed by `bash/setup.sh` |

CPU-only reality: generation ≈ 8–15 tok/s and **prompt prefill ≈ 30–60 tok/s** for a 3B
Q4 model. Prefill is the hidden killer — the design below is shaped around avoiding it
(stable prompt prefixes, tiny prompts, two-pass triage).

## 3. Core principle

**The LLM never "writes the tutorial."** It performs narrow, schema-constrained
classification and extraction over small transcript chunks. All stitching is done by
deterministic Go code. A small model given one tiny job at a time is near-perfect at
that job; a small model asked to "reproduce this video" fails. Every design decision
serves this:

1. **Small chunks** (150–350 tokens) — context never exceeds what a 3B model holds reliably.
2. **Structured outputs** (Ollama JSON-schema `format`) — invalid JSON becomes impossible,
   eliminating the main source of small-model flakiness.
3. **Two-pass extraction** — a nearly-free triage pass skips non-code chatter before any
   expensive generation happens.
4. **Deterministic assembly** — replaying extracted events is pure Go; zero model variance.
5. **Verification gate** — nothing reaches the user without passing static + optional LLM checks.

## 4. Pipeline

```
YouTube URL
   │
   ▼
[fetch]      yt-dlp: captions-first, audio fallback → transcript.json
   │
   ▼
[segment]    merge caption lines into code-step chunks → chunks.json
   │
   ▼
[extract]    Pass A: triage (is there a code action here?)     → triage.jsonl
             Pass B: deep extract (schema-bound code events)  → events.jsonl
   │
   ▼
[assemble]   deterministic event replay                       → out/*.mq5
   │
   ▼
[verify]     static checks (+ optional LLM diff check)        → report.json
```

Orchestrated by `cmd/pipeline`, which also implements **resume**: every stage records an
input hash in its output manifest; re-running skips stages whose inputs are unchanged.

### 4.1 fetch

- Subtitle preference: manual `en` → auto-generated `en` → none.
- If no captions: download audio (`m4a`, 48 kbps) and transcribe with **faster-whisper**
  (`small`, `compute_type=int8`, VAD filter, `cpu_threads=8`).
- Honest caveat, documented rather than hidden: the Whisper path on this CPU runs roughly
  realtime, so a captionless 80-min video may exceed the 15-min budget *before* the LLM
  even starts. Captions path is near-instant. `--transcript-mode` flag lets the user force
  either.

### 4.2 segment

Merge raw caption lines into chunks using three signals:

1. **Pause gaps** — caption silence > 1.5 s starts a new chunk.
2. **Code-action cues** — phrases like "let's add", "next line", "so now we", "type this"
   start a new chunk when they open a caption line.
3. **Caps** — max ~350 tokens or ~45 s of speech per chunk, whichever first.

Chunk IDs are deterministic (`c0001`, `c0002`, …) so downstream artifacts are stable
across re-runs.

### 4.3 extract — two passes

**Pass A — triage.** One tiny call per chunk: `{chunk_id, has_code_action, confidence}`,
`num_predict ≤ 16`, temperature 0. Tutorials typically contain 40–60% non-code talk;
this pass removes it from the expensive path at ~3–6 s per chunk.

**Pass B — deep extract.** For triaged-positive chunks only. Emits strict events:

```json
{ "chunk_id": "c0017", "seq": 1, "op": "append",
  "file": "MyEA.mq5", "anchor": null,
  "code": "input double LotSize = 0.10;" }
```

Ops: `create | append | replace | property | include`. `replace` carries an `anchor`
(exact snippet from the current file state to be replaced) so replay stays deterministic.

Retry policy: 2 retries on schema/validation failure, then mark the chunk `failed` and
continue — the verify report surfaces it instead of killing the run.

**Prefill economics:** the system prompt + few-shot block is byte-identical across calls,
so Ollama's KV-cache prefix reuse applies within the keep-alive window. Per-call unique
prefill is only the chunk text itself. This is why prompts live in one place
(`internal/prompts`) and must never be interpolated with per-run data ahead of the shared
prefix.

### 4.4 assemble

Pure Go event replay in `(chunk_id, seq)` order onto an in-memory file map. Handles
`create`, `append`, `replace` (anchor match required — mismatch is reported, never
guessed), `property`/`include` header insertion. Emits `assembly-report.json`
(ops applied / skipped / conflicts).

### 4.5 verify

Static checks (no model): brace/paren balance, `#property strict` presence, expected
entry points per program type (`OnInit` etc.), suspicious artifacts (placeholder text,
truncated lines), metadata extraction mirroring the sibling `mql5-code-extraction`
extractor.

Optional `--llm-check`: one model call comparing assembled file vs. concatenated code
events, returning a diff-style verdict JSON. Off by default (budget), recommended for
final copies.

Output: `report.json` with a weighted confidence score and itemized findings.

## 5. Model strategy

| Role | Model | Why |
|---|---|---|
| Primary | `qwen2.5-coder:3b-instruct` Q4_K_M | Best code fidelity per token/s on CPU; ~2 GB RAM |
| Fast (`--fast`) | `qwen2.5-coder:1.5b` | Drafts / long videos where speed dominates |

Generation params: `temperature 0`, `num_ctx 4096`, JSON-schema `format` always,
`num_predict` capped per pass (64 triage / 512 deep). Concurrency: 2 workers
(`OLLAMA_NUM_PARALLEL=2`) — memory-bound CPU inference gains modestly from overlap.

Model choice is a config value, not a hardcoded fact — benchmark task exists to revisit it.

## 6. Performance budget (60-min video, captions path, ~90 chunks)

| Stage | Math | Time |
|---|---|---|
| fetch + segment | network-bound | < 1 min |
| triage | 90 calls × 3–6 s ÷ 2 workers | 3–5 min |
| deep extract | ~35 calls × 25–40 s ÷ 2 workers | 8–12 min |
| assemble + verify | pure Go | < 10 s |
| **Total** | | **≈ 12–18 min** |

Levers when over budget: `--fast`, stricter triage threshold, smaller `num_ctx`.
80-min videos: expect up to ~25 min on the primary model; `--fast` brings it back near 15.

## 7. Repository layout

```
cmd/
  fetch/ segment/ extract/ assemble/ verify/ pipeline/   # thin mains, logic in internal/
internal/
  ytdlp/       # yt-dlp/ffmpeg process wrappers
  transcript/  # caption parsing, whisper invocation, normalization
  segment/     # chunking heuristics
  ollama/      # REST client (net/http), retry, structured-output helpers
  prompts/     # ALL prompt templates; stable-prefix rule enforced here
  events/      # event types, validation
  assemble/    # deterministic replay
  verify/      # static + LLM checks
  cfg/         # pipeline.yaml loading, flag merging
  runstore/    # runs/<id>/ layout, manifests, resume logic
bash/
  setup.sh     # ollama install + model pull + faster-whisper pip install
  dev.sh       # gofmt/goimports → vet → lint → test -race → build → tidy check
  git-push.sh  # conventional-commit push pipeline
runs/<video-id>/   # all artifacts (gitignored)
pipeline.yaml      # default config
```

## 8. Conventions carried over from sibling repo

- Latest Go only; stdlib only (Ollama accessed via plain `net/http` — no SDK dep).
- Exit codes: 0 ok, 1 runtime error, 2 usage error.
- Errors wrapped with context; data to stdout, diagnostics to stderr.
- Table-driven tests; `-race` clean; bounded reads everywhere (`bufio.Scanner` caps).
- External binaries invoked via `exec.Command` argument lists — never shell strings
  (URLs are untrusted input).
- `*.sh` forced LF via `.gitattributes`; golangci-lint v2 (`standard` + `gosec`).

## 9. Testing without the model

CI/dev loop must stay green with **no Ollama, no network**: the Ollama client is tested
against `httptest`, and the full mini-pipeline runs on a bundled fixture transcript with
a scripted fake server. Model-dependent behavior is isolated behind `internal/ollama`.

## 10. Non-goals

- No GUI. No watching video frames (transcript-only by design — vision models are out of
  budget). No multi-video playlists in v1. No non-MQL5 languages.
