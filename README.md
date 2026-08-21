# mql5-tutorial-pipeline

Go CLI that turns YouTube MQL5 tutorial videos into exact `.mq5` code replicas using a
small local LLM (Ollama) for narrow schema-constrained extraction only — all assembly
and verification is deterministic Go. Stdlib only, latest Go only.

Feed in a URL, get copy-pasteable code out:

```
YouTube URL → fetch → segment → extract → assemble → verify → out/*.mq5
```

- `spec.md` — buildable contract (stage I/O formats, CLI surface, acceptance criteria)
- `design.md` — design rationale (why the LLM never "writes the tutorial")
- `task.yaml` — implementation task tracker

## Quick start

```bash
./bash/setup.sh        # one-time: golangci-lint v2 + goimports into ~/go/bin
./bash/dev.sh          # THE dev loop: format → vet → lint → test → build → tidy check
go run ./cmd/pipeline <youtube-url>
```

Stage CLIs: `go run ./cmd/{fetch,segment,extract,assemble,verify}` (see each `--help`).

## Requirements

- Latest Go
- [Ollama](https://ollama.com) with `qwen2.5-coder:3b-instruct` (primary) / `qwen2.5-coder:1.5b` (`--fast`)
- `yt-dlp` + `ffmpeg` on PATH (fetch stage)
- Python 3.12 + faster-whisper (only for captionless videos)

## Status

Work in progress — see `task.yaml`. Tests pass with no Ollama running and no network.
