#!/usr/bin/env bash
#
# dev.sh — one-command developer loop for this repo.
#
#   ./bash/dev.sh             format, vet, lint, test, build, tidy-check
#   ./bash/dev.sh --strict    same, but FAIL if optional tools are missing (use in CI)
#
# Optional tools (install once with ./bash/setup.sh):
#   golangci-lint, goimports
#
set -euo pipefail

# Repo root = parent of the script's directory.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

STRICT=0
[[ "${1:-}" == "--strict" ]] && STRICT=1

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[!]\033[0m %s\n' "$*" >&2; exit 1; }

# require <tool> <hint>: returns 0 if the tool exists, else warns (or dies
# under --strict) and returns 1.
require() {
  if command -v "$1" >/dev/null 2>&1; then
    return 0
  fi
  if (( STRICT )); then
    die "required tool '$1' not found: $2"
  fi
  warn "'$1' not found — skipping this step ($2)"
  return 1
}

GO_FILES() { find . -type f -name '*.go' -not -path './vendor/*'; }

# --- format ---------------------------------------------------------------
say "format"
GO_FILES | xargs gofmt -w
if require goimports "install with: ./bash/setup.sh"; then
  GO_FILES | xargs goimports -w
fi
unformatted="$(GO_FILES | xargs gofmt -l)"
if [[ -n "$unformatted" ]]; then
  printf '%s\n' "$unformatted"
  die "gofmt: files above are not formatted (ran gofmt -w; check manually)"
fi

# --- vet ------------------------------------------------------------------
say "vet"
go vet ./...

# --- lint -----------------------------------------------------------------
say "lint"
if require golangci-lint "install with: ./bash/setup.sh"; then
  golangci-lint run ./...
fi

# --- test -----------------------------------------------------------------
say "test"
if [[ "$(go env CGO_ENABLED)" == "1" ]]; then
  go test -race -cover ./...
else
  warn "CGO_ENABLED=0 (no C toolchain) — skipping -race; running without it"
  go test -cover ./...
fi

# --- build ----------------------------------------------------------------
say "build"
go build ./...

# --- module hygiene -------------------------------------------------------
say "tidy"
go mod tidy
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if ! git diff --quiet -- go.mod go.sum; then
    warn "go.mod/go.sum changed after 'go mod tidy' — commit the change"
  fi
fi

say "done ✔  all checks passed"
