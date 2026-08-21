#!/usr/bin/env bash
#
# git-push.sh — auto-complete the full git pipeline: stage, quality-gate,
# commit, and push to origin in one command.
#
#   ./bash/git-push.sh                  auto-generated message + push
#   ./bash/git-push.sh -m "my message"  write your own commit message
#   ./bash/git-push.sh -n               dry run — print the plan, change nothing
#   ./bash/git-push.sh --no-checks      skip ./bash/dev.sh --strict
#
# The auto message is a conventional commit (<type>(<scope>): <summary>)
# derived from the staged diff, e.g. "feat(internal/cfg): config.go".
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MESSAGE=""
DRY_RUN=0
SKIP_CHECKS=0

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[!]\033[0m %s\n' "$*" >&2; exit 1; }

# auto_message derives a conventional commit message from the staged diff:
#   feat  — any new non-test file
#   fix   — only modified files
#   test  — only *_test.go files changed
#   docs  — only *.md files changed
#   chore — anything else (pure deletions, config, etc.)
# Scope is the top-level directory of the first changed file; summary is the
# comma-joined basenames of the changed files, truncated to 70 chars.
auto_message() {
  local files first scope summary
  files="$(git diff --cached --name-only)"
  [[ -n "$files" ]] || { echo "chore: no changes"; return; }

  local type="chore"
  if git diff --cached --name-only --diff-filter=A | grep -qv '_test\.go$'; then
    type="feat"
  elif git diff --cached --name-only --diff-filter=D | grep -q .; then
    : # pure deletions stay "chore"
  elif git diff --cached --name-only --diff-filter=M | grep -q .; then
    type="fix"
  fi
  if [[ -z "$(git diff --cached --name-only | grep -vE '_test\.go$|\.md$' | grep . )" ]]; then
    if git diff --cached --name-only | grep -q '_test\.go$'; then type="test"; fi
    if git diff --cached --name-only | grep -q '\.md$';            then type="docs"; fi
  fi

  first="$(git diff --cached --name-only | head -1)"
  scope="$(dirname "$first")"
  [[ "$scope" != "." ]] || scope="$(basename "$first")"

  summary="$(git diff --cached --name-only | xargs -n1 basename | paste -sd, -)"
  if [[ ${#summary} -gt 70 ]]; then summary="${summary:0:67}..."; fi
  echo "${type}(${scope}): ${summary}"
}

usage() {
  cat <<'EOF'
Usage: ./bash/git-push.sh [options]

Auto-completes the git pipeline: git add -A → dev checks → commit → push.

Options:
  -m, --message "msg"   use your own commit message (default: auto-generated)
  -n, --dry-run         print the plan without changing anything
      --no-checks       skip ./bash/dev.sh --strict before committing
  -h, --help            show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--message) MESSAGE="${2:?usage: $1 requires a message}"; shift 2 ;;
    -n|--dry-run)  DRY_RUN=1; shift ;;
    --no-checks)   SKIP_CHECKS=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) die "unknown option: $1 (try -h)" ;;
  esac
done

# --- sanity -----------------------------------------------------------------
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  die "not inside a git repository"
fi
if ! git remote get-url origin >/dev/null 2>&1; then
  die "no 'origin' remote configured — add one first, e.g. git remote add origin <url>"
fi

# --- stage ------------------------------------------------------------------
say "stage"
git add -A
if git diff --cached --quiet; then
  say "nothing to commit — working tree is already clean"
  exit 0
fi

# --- message ----------------------------------------------------------------
if [[ -z "$MESSAGE" ]]; then
  MESSAGE="$(auto_message)"
fi
say "message: $MESSAGE"

# --- dry run ----------------------------------------------------------------
if (( DRY_RUN )); then
  say "DRY RUN — would run:"
  (( SKIP_CHECKS )) || echo "  ./bash/dev.sh --strict"
  echo "  git add -A"
  echo "  git commit -m \"$MESSAGE\""
  branch="$(git branch --show-current)"
  if git rev-parse --abbrev-ref --symbolic-full-name "@{u}" >/dev/null 2>&1; then
    echo "  git push"
  else
    echo "  git push -u origin $branch"
  fi
  exit 0
fi

# --- quality gate -----------------------------------------------------------
if (( ! SKIP_CHECKS )); then
  say "quality gate: ./bash/dev.sh --strict"
  ./bash/dev.sh --strict
fi

# --- commit + push ----------------------------------------------------------
say "commit"
git commit -m "$MESSAGE"

say "push"
branch="$(git branch --show-current)"
if git rev-parse --abbrev-ref --symbolic-full-name "@{u}" >/dev/null 2>&1; then
  git push
else
  git push -u origin "$branch"
fi

say "done ✔  pushed $(git rev-parse --short HEAD) to origin/$branch"
