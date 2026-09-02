#!/usr/bin/env bash
# Run every verification stage this checkout can run.
#
# The full suite lives in a separate overlay repository (see scripts/overlay.sh)
# and is delegated to when it is present. When it is not -- a plain clone, or a
# fork pull request, which cannot reach it -- this runs the reduced set and says
# plainly what did not run. It never reports success for suites it skipped.
#
# Docker is required. The overlay builds a verify image (Chromium, Go, pnpm)
# and runs the stages inside it. With no overlay this script triggers official
# golang and node images for the reduced set; it has no verify Dockerfile of
# its own.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ARGS=()
[[ "${1:-}" == --no-sync ]] && { ARGS+=(--no-sync); shift; }

if OVERLAY="$("$ROOT/scripts/overlay.sh" "${ARGS[@]+"${ARGS[@]}"}")"; then
  # LEMMARY_ROOT is how the overlay learns which tree to test. It no longer
  # infers that from its own location, which is what let it move out of the
  # tree in the first place.
  exec env LEMMARY_ROOT="$ROOT" "$OVERLAY/scripts/test-all.sh" "$@"
fi

cd "$ROOT"

if ! docker info >/dev/null 2>&1; then
  echo "Docker is required for verification." >&2
  exit 1
fi

stage() { echo ""; echo "==> $1"; }
fail()  { echo ""; echo "FAILED: $1" >&2; exit 1; }

echo "No verification overlay found: running unit tests and the frontend build."
echo "API and browser e2e will NOT run."

stage "Unit tests"
# apt-get needs root; tests must not run as root (vault Wipe as uid 0 unlinks
# a chmod-0500 parent that stands in for a tmpfs mount).
docker run --rm \
  -v "$ROOT:$ROOT" \
  -w "$ROOT/backend" \
  -e HOST_UID="$(id -u)" \
  -e HOST_GID="$(id -g)" \
  golang:1.26.3-bookworm \
  bash -c 'set -euo pipefail
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq poppler-utils gosu
    mkdir -p /tmp/go-build /tmp/go-mod
    chown "$HOST_UID:$HOST_GID" /tmp/go-build /tmp/go-mod
    export GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod GOTOOLCHAIN=local HOME=/tmp
    run_go() {
      GOWORK=off go test ./... -count=1
      echo
      echo "==> Vet"
      GOWORK=off go vet ./...
    }
    if [[ "$HOST_UID" != 0 ]]; then
      groupadd -g "$HOST_GID" -o lemmary 2>/dev/null || true
      useradd -u "$HOST_UID" -g "$HOST_GID" -o -M -d /tmp -s /bin/bash lemmary 2>/dev/null || true
      exec gosu "$HOST_UID:$HOST_GID" bash -c "$(declare -f run_go); run_go"
    fi
    run_go
  ' || fail "unit tests"

stage "Frontend unit tests"
# Node 26 does not ship corepack. npm install -g needs root if it writes to
# /usr/local, so install pnpm under $HOME as the bind-mount user: that keeps
# frontend/node_modules and public/ host-owned, and puts pnpm on PATH for
# `pnpm run docs:build` inside the build script. Pin matches package.json.
docker run --rm \
  -u "$(id -u):$(id -g)" \
  -e HOME=/tmp/lemmary-reduced \
  -e npm_config_cache=/tmp/lemmary-reduced/npm \
  -v "$ROOT:$ROOT" \
  -w "$ROOT/frontend" \
  node:26-bookworm \
  bash -c 'set -euo pipefail
    mkdir -p "$HOME"
    npm install -g pnpm@11.24.0 --prefix "$HOME"
    export PATH="$HOME/bin:$PATH"
    pnpm install --frozen-lockfile
    pnpm test
    echo
    echo "==> Frontend build"
    pnpm run build
  ' || fail "frontend unit tests"

echo ""
echo "Reduced verification passed. API and browser e2e did not run."
