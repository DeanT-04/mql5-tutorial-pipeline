# AGENTS.md — mql5-tutorial-pipeline

Go CLI that turns YouTube MQL5 tutorial videos into exact `.mq5` code using a small
local LLM (Ollama) for narrow schema-constrained extraction only; all assembly and
verification is deterministic Go. Stdlib only, latest Go only. Read `spec.md` (buildable
contract) and `design.md` (rationale); `task.yaml` tracks implementation tasks.

## Commands

- `./bash/dev.sh` — THE dev loop: gofmt + goimports → `go vet` → golangci-lint → `go test -race -cover` → `go build` → `go mod tidy` check. Run it after any change.
- `./bash/dev.sh --strict` — same, but fails if golangci-lint/goimports are missing (CI mode).
- `./bash/git-push.sh` — full git pipeline: stage → dev checks → commit → push. Auto-generates a conventional-commit message; `-m "msg"` for your own, `-n` dry run, `--no-checks` to skip the gate.
- `./bash/setup.sh` — one-time environment setup (dev tools via `go install`, Ollama + model pulls via scoop, faster-whisper via pip). `--check` reports only.
- `go test -race -cover ./...` — tests only.
- `go run ./cmd/pipeline <youtube-url>` — run the whole pipeline (`--force` re-runs all stages).
- Stage CLIs: `go run ./cmd/{fetch,segment,extract,assemble,verify}` (see each `--help`).

## Environment quirks (this machine)

- WSL bash is BROKEN here (`ext4.vhdx` mount error) — invoke shell scripts via Git Bash:
  `& "C:\Program Files\Git\bin\bash.exe" -c "cd '/c/Users/Deano/Documents/projects/mql5-tutorial-pipeline' && ./bash/dev.sh"`
- Package installs must use **scoop** (already installed); do not use winget.
- `-race` needs cgo (no C toolchain here); `./bash/dev.sh` auto-falls back to plain `go test`.
- Ollama runs as `ollama serve`; models `qwen2.5-coder:3b-instruct` (primary) and
  `qwen2.5-coder:1.5b` (`--fast`) are already pulled.

## Architecture

- Pipeline: fetch → segment → extract (triage + deep) → assemble → verify, orchestrated by `cmd/pipeline`. Resume = per-stage SHA-256 input hash **plus output artifact existence** (`internal/runstore` manifest + `internal/stages.cached`) — deleting an output file forces that stage to re-run.
- All logic in `internal/`; `internal/stages` holds the shared stage implementations used by both the orchestrator and the standalone CLIs — change behavior there, not in `cmd/*`, which are thin wrappers around a testable `run()`. Exit codes: 0 ok, 1 runtime error, 2 usage error.
- Model-dependent behavior is isolated behind `internal/ollama` (plain `net/http`, no SDK). Tests must pass with no Ollama and no network (`httptest` + scripted fake server / injected `Runner`).
- Prompts live in `internal/prompts` and must keep a byte-stable shared prefix across calls (KV-cache reuse) — never interpolate per-run data into the prefix.
- Config precedence: built-in defaults < `pipeline.yaml` < flags; use `cfg.LoadOrDefault` (never hand-roll the missing-file fallback per CLI).

## Conventions

- **Formatting**: gofmt + goimports via `./bash/dev.sh`; never commit unformatted code.
- **Errors**: wrap with context (`fmt.Errorf("...: %w", err)`), never ignore; CLI reports to stderr, data to stdout.
- **Tests**: table-driven; every exported function covered including edge cases. `go test -race` must stay clean when cgo is available.
- **Security**: bounded reads everywhere (caps on files, scanner buffers, subprocess stdout/stderr); external binaries invoked via `exec.Command` argument lists only — never shell strings (URLs are untrusted input); gosec clean.
- **Dependencies**: stdlib only — no third-party modules, including YAML libraries (`internal/cfg` has a purpose-built parser for the flat schema in spec §5).
- **Modernity**: latest Go only (go.mod: 1.26). No deprecated/outdated APIs or patterns.
- **Git**: finish work by running `./bash/git-push.sh` (or `-m "message"`) and ALWAYS verify the push landed: `git status -sb` should read `## main...origin/main` (nothing ahead/behind). Keep `main` as the only branch; no force-push to `origin/main`; conventional-commit messages.

## Gotchas

- Triage pass uses `num_predict 64` (deep pass 512). It was once 16 and every JSON reply was truncated mid-token ("unexpected EOF"), silently fail-opening ALL chunks into the deep pass — don't shrink it back.
- Extraction fidelity on real videos is ~20% vs the 95% target; known defects and v1.x follow-ups are recorded in `experiments/fidelity-e2d5eh3Zi9o.md` (local-only; `experiments/` is gitignored). Videos where code is shown but not dictated yield ~0 events by design.
- `runs/` is gitignored pipeline state; safe to delete for clean re-runs.
