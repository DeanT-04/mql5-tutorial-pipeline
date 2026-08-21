# AGENTS.md — mql5-tutorial-pipeline

Go CLI that turns YouTube MQL5 tutorial videos into exact `.mq5` code using a small
local LLM (Ollama) for narrow schema-constrained extraction only; all assembly and
verification is deterministic Go. Stdlib only, latest Go only. Read `spec.md` (buildable
contract) and `design.md` (rationale); `task.yaml` tracks implementation tasks.

## Commands

- `./bash/dev.sh` — THE dev loop: gofmt + goimports → `go vet` → golangci-lint → `go test -race -cover` → `go build` → `go mod tidy` check. Run it after any change.
- `./bash/dev.sh --strict` — same, but fails if golangci-lint/goimports are missing (CI mode).
- `./bash/git-push.sh` — full git pipeline: stage → dev checks → commit → push. Auto-generates a conventional-commit message; `-m "msg"` for your own, `-n` dry run, `--no-checks` to skip the gate.
- `./bash/setup.sh` — one-time install of golangci-lint v2 + goimports into `~/go/bin`.
- `go test -race -cover ./...` — tests only.
- `go run ./cmd/pipeline <youtube-url>` — run the whole pipeline.
- Stage CLIs: `go run ./cmd/{fetch,segment,extract,assemble,verify}` (see each `--help`).

## Architecture

- Pipeline: fetch → segment → extract (triage + deep) → assemble → verify, orchestrated by `cmd/pipeline` with content-hash resume via `internal/runstore`.
- All logic in `internal/`; `cmd/` mains are thin wrappers around a testable `run()`. Exit codes: 0 ok, 1 runtime error, 2 usage error.
- Model-dependent behavior is isolated behind `internal/ollama` (plain `net/http`, no SDK). Tests must pass with no Ollama and no network (`httptest` + scripted fake server).
- Prompts live in `internal/prompts` and must keep a byte-stable shared prefix across calls (KV-cache reuse) — never interpolate per-run data into the prefix.

## Conventions

- **Formatting**: gofmt + goimports via `./bash/dev.sh`; never commit unformatted code.
- **Errors**: wrap with context (`fmt.Errorf("...: %w", err)`), never ignore; CLI reports to stderr, data to stdout.
- **Tests**: table-driven; every exported function covered including edge cases. `go test -race` must stay clean — note `-race` needs `CGO_ENABLED=1` (a C toolchain); `./bash/dev.sh` auto-falls back to plain `go test` when cgo is unavailable (this dev machine has none).
- **Security**: bounded reads everywhere (`bufio.Scanner` caps); external binaries invoked via `exec.Command` argument lists only — never shell strings (URLs are untrusted input); gosec clean.
- **Dependencies**: stdlib only — no third-party modules, including YAML libraries (`internal/cfg` has a purpose-built parser).
- **Modernity**: latest Go only (go.mod: 1.26). No deprecated/outdated APIs or patterns.
- **Git**: finish work by running `./bash/git-push.sh` (or `-m "message"`) and ALWAYS verify the push landed: `git status -sb` should read `## main...origin/main` (nothing ahead/behind). Keep `main` as the only branch; no force-push to `origin/main`; conventional-commit messages.

## Notes

-
