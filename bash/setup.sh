#!/usr/bin/env bash
#
# setup.sh — install the optional dev toolchain for this repo (one-time).
#
#   ./bash/setup.sh
#
# Installs into $(go env GOBIN) (default: ~/go/bin):
#   golangci-lint  — linter runner used by bash/dev.sh (v2)
#   goimports      — import grouping/formatting used by bash/dev.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

say "installing golangci-lint (latest)"
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

say "installing goimports (latest)"
go install golang.org/x/tools/cmd/goimports@latest

GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
say "done ✔  tools installed in: $GOBIN"
say "ensure that directory is on your PATH, then run ./bash/dev.sh"
