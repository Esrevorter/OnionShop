#!/usr/bin/env bash
# =============================================================================
# sync.sh — keep your local OnionShop clone in sync with the hardened branch,
# verify it (go build / vet / test), and rebuild Docker images from the exact
# synced commit so regressions can never slip in silently.
#
# Usage:
#   export GITHUB_TOKEN=ghp_xxx            # fine-grained PAT, repo contents:read
#   ./sync.sh                              # fetch + hard-sync + verify + docker build
#   ./sync.sh --no-docker                  # skip docker steps
#   ./sync.sh --branch main                # sync a different branch (default: qwen-security-fixes)
#
# The script is idempotent and fails closed: if any verification step fails,
# your working tree stays pinned at the last-known-good commit (it re-hards
# to the previous SHA before exiting non-zero).
# =============================================================================
set -euo pipefail

REPO_URL_SSH="git@github.com:Esrevorter/Onionshop.git"
REPO_URL_HTTPS="https://x-access-token:${GITHUB_TOKEN:-}@github.com/Esrevorter/Onionshop.git"
BRANCH="${2:-qwen-security-fixes}"
[ "${1:-}" = "--branch" ] && BRANCH="${2:-qwen-security-fixes}"
NO_DOCKER=0
for arg in "$@"; do [ "$arg" = "--no-docker" ] && NO_DOCKER=1; done

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

if [ ! -d .git ]; then
  echo "==> No git repo here; cloning Esrevorter/Onionshop"
  git clone "$REPO_URL_SSH" . 2>/dev/null || git clone "$REPO_URL_HTTPS" .
fi

PREV_SHA="$(git rev-parse HEAD 2>/dev/null || echo '')"

echo "==> Fetching origin/$BRANCH"
git remote set-url origin "$REPO_URL_SSH" 2>/dev/null || true
git fetch origin "$BRANCH" --prune || {
  git remote set-url origin "$REPO_URL_HTTPS"
  git fetch origin "$BRANCH" --prune
}

echo "==> Hard-syncing working tree to origin/$BRANCH (discards local drift)"
git checkout -B "$BRANCH" "origin/$BRANCH"
git clean -fdx -e .env -e '*.local' || true
NEW_SHA="$(git rev-parse HEAD)"
echo "    now at $NEW_SHA"

rollback() {
  echo "!! Verification failed — rolling back to $PREV_SHA" >&2
  [ -n "$PREV_SHA" ] && git reset --hard "$PREV_SHA" >/dev/null 2>&1 || true
  exit 1
}

echo "==> go build ./..."
go build ./... || rollback

echo "==> go vet ./..."
go vet ./... || rollback

echo "==> go test ./..."
go test ./... || rollback

if [ "$NO_DOCKER" -eq 0 ] && command -v docker >/dev/null 2>&1; then
  echo "==> docker build (tagged onionshop:$NEW_SHA and :latest)"
  docker build -t "onionshop:${NEW_SHA:0:12}" -t onionshop:latest . || rollback
  if [ -f docker-compose.yml ]; then
    echo "==> docker compose config validation"
    docker compose config -q || rollback
  fi
else
  echo "==> Skipping Docker (--no-docker or docker not installed)"
fi

echo "==> OK: synced to $NEW_SHA, all checks passed."
