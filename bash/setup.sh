#!/usr/bin/env bash
#
# setup.sh — one-time environment setup for this repo.
#
#   ./bash/setup.sh            install everything that is missing
#   ./bash/setup.sh --check    only report what is missing
#
# Installs / verifies:
#   golangci-lint, goimports   -> $(go env GOBIN)        (dev toolchain)
#   yt-dlp, ffmpeg             -> must already be on PATH (warn + hint)
#   ollama                     -> winget/scoop/user installer
#   qwen2.5-coder models       -> via `ollama pull`
#   faster-whisper             -> pip install (only needed for captionless videos)
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CHECK_ONLY=0
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=1

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[!]\033[0m %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

run_step() {
  if (( CHECK_ONLY )); then
    warn "check-only: would run: $*"
    return 0
  fi
  "$@"
}

# --- dev toolchain ----------------------------------------------------------
say "dev toolchain"
if ! have golangci-lint; then
  run_step go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
fi
if ! have goimports; then
  run_step go install golang.org/x/tools/cmd/goimports@latest
fi

# --- ingest tools -----------------------------------------------------------
say "ingest tools"
if ! have yt-dlp; then
  warn "yt-dlp not found — install with: scoop install yt-dlp"
fi
if ! have ffmpeg; then
  warn "ffmpeg not found — install with: scoop install ffmpeg"
fi

# --- ollama -----------------------------------------------------------------
say "ollama"
if have ollama; then
  say "ollama already installed: $(ollama --version 2>/dev/null || echo unknown)"
elif have scoop; then
  say "installing ollama via scoop"
  run_step scoop install ollama
else
  die "scoop not found — install scoop first (https://scoop.sh), then re-run ./bash/setup.sh"
fi

MODELS=("qwen2.5-coder:3b-instruct" "qwen2.5-coder:1.5b")
if have ollama; then
  for m in "${MODELS[@]}"; do
    if ollama list 2>/dev/null | grep -q "^${m//:/\\:}\b\|^$m\b"; then
      say "model $m already pulled"
    else
      say "pulling model $m (~2 GB, one-time)"
      run_step ollama pull "$m"
    fi
  done
else
  warn "ollama unavailable — skipping model pulls (re-run ./bash/setup.sh after installing)"
fi

# --- faster-whisper (optional, captionless videos only) ---------------------
say "faster-whisper"
if python -c "import faster_whisper" >/dev/null 2>&1; then
  say "faster-whisper already installed"
else
  say "installing faster-whisper via pip"
  run_step python -m pip install faster-whisper
fi

GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
say "done ✔  dev tools in: $GOBIN"
