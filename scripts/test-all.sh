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
docker run --rm \
  -v "$ROOT:$ROOT" \
  -w "$ROOT/backend" \
  -e GOCACHE=/tmp/go-build \
  -e GOMODCACHE=/tmp/go-mod \
  -e GOTOOLCHAIN=local \
  golang:1.26.3-bookworm \
  bash -c 'set -euo pipefail
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq poppler-utils
    GOWORK=off go test ./... -count=1
    echo
    echo "==> Vet"
    GOWORK=off go vet ./...
  ' || fail "unit tests"

stage "Frontend unit tests"
docker run --rm \
  -u "$(id -u):$(id -g)" \
  -e HOME=/tmp/lemmary-reduced \
  -e COREPACK_HOME=/tmp/lemmary-reduced/corepack \
  -e COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
  -e npm_config_cache=/tmp/lemmary-reduced/npm \
  -v "$ROOT:$ROOT" \
  -w "$ROOT/frontend" \
  node:26-bookworm \
  bash -c 'set -euo pipefail
    mkdir -p "$HOME" "$COREPACK_HOME"
    corepack pnpm install --frozen-lockfile
    corepack pnpm test
    echo
    echo "==> Frontend build"
    corepack pnpm run build
  ' || fail "frontend unit tests"

echo ""
echo "Reduced verification passed. API and browser e2e did not run."
