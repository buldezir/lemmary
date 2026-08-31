#!/usr/bin/env bash
# Run every verification stage this checkout can run.
#
# The full suite lives in a separate overlay repository (see scripts/overlay.sh)
# and is delegated to when it is present. When it is not -- a plain clone, or a
# fork pull request, which cannot reach it -- this runs the reduced set and says
# plainly what did not run. It never reports success for suites it skipped.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ARGS=()
[[ "${1:-}" == --no-sync ]] && { ARGS+=(--no-sync); shift; }

if OVERLAY="$("$ROOT/scripts/overlay.sh" "${ARGS[@]+"${ARGS[@]}"}")"; then
  # Its own dependencies, in its own checkout. pnpm hardlinks from a shared
  # store, so a per-worktree install costs disk only for what differs.
  if [[ -f "$OVERLAY/package.json" && ! -d "$OVERLAY/node_modules" ]]; then
    echo "==> Installing overlay dependencies ($OVERLAY)"
    (cd "$OVERLAY" && pnpm install --frozen-lockfile)
  fi
  # LEMMARY_ROOT is how the overlay learns which tree to test. It no longer
  # infers that from its own location, which is what let it move out of the
  # tree in the first place.
  exec env LEMMARY_ROOT="$ROOT" "$OVERLAY/scripts/test-all.sh" "$@"
fi

cd "$ROOT"

# pnpm verifies that node_modules matches the lockfile before running a script
# and fails outright when it does not, so a bare clone needs this first.
if [[ ! -d frontend/node_modules ]]; then
  echo "==> Installing frontend dependencies"
  (cd frontend && pnpm install --frozen-lockfile)
fi

stage() { echo ""; echo "==> $1"; }
fail()  { echo ""; echo "FAILED: $1" >&2; exit 1; }

echo "No verification overlay found: running unit tests and the compile-level"
echo "extension-seam check. API and browser e2e will NOT run."

stage "Unit tests"
(cd backend && go test ./... -count=1) || fail "unit tests"

stage "Extension seams (lemmary_exttest build)"
# The private cloud build lives in a fork neither repository can build, so this
# tag is the only thing here that proves ext.Edition and boot.Result are still
# wired. The HTTP-level assertions are in the overlay; this is the compile half.
(cd backend && go vet -tags lemmary_exttest ./... \
  && go test -tags lemmary_exttest ./internal/boot/ -count=1) || fail "extension seams"

stage "Frontend unit tests"
(cd frontend && pnpm test) || fail "frontend unit tests"

stage "Frontend build"
# Nothing else here compiles the SPA, so without this a broken tsc, Vite or
# VitePress would go unnoticed on a run that skipped the overlay.
(cd frontend && pnpm run build) || fail "frontend build"

echo ""
echo "Reduced verification passed. API and browser e2e did not run."
