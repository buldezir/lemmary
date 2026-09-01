#!/usr/bin/env bash
# Run every verification stage this checkout can run.
#
# The full suite lives in a separate overlay repository (see scripts/overlay.sh)
# and is delegated to when it is present. When it is not -- a plain clone, or a
# fork pull request, which cannot reach it -- this runs the reduced set and says
# plainly what did not run. It never reports success for suites it skipped.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# The backend does not build without the vectors tag: bleve compiles its kNN API
# out otherwise, and internal/fulltext/vectors_required.go stops a tag-less
# build with one readable error. Exported rather than passed per command so the
# overlay suite, which runs go commands of its own, inherits it too.
export GOFLAGS=-tags=vectors
export CGO_ENABLED=1

ARGS=()
[[ "${1:-}" == --no-sync ]] && { ARGS+=(--no-sync); shift; }

stage() { echo ""; echo "==> $1"; }
fail()  { echo ""; echo "FAILED: $1" >&2; exit 1; }

# The vectors tag means cgo against blevesearch's FAISS fork. Checked before
# anything else, including the overlay handoff: without it every Go stage fails
# with linker output that says nothing about how to fix it.
faiss_preflight() {
  stage "FAISS (bleve vector search)"

  # A developer who built FAISS into a prefix of their own says so through
  # CGO_LDFLAGS, and the library will not be on the default loader path.
  if [[ -n "${CGO_LDFLAGS:-}" ]]; then
    echo "CGO_LDFLAGS is set, trusting it: $CGO_LDFLAGS"
    return
  fi

  local ldconfig
  ldconfig="$(command -v ldconfig || echo /sbin/ldconfig)"
  if [[ -x "$ldconfig" ]] && "$ldconfig" -p 2>/dev/null | grep -q 'libfaiss_c\.'; then
    echo "libfaiss_c is on the loader path"
    return
  fi
  local f
  for f in /usr/local/lib/libfaiss_c.so* /usr/lib/libfaiss_c.so* \
           /usr/local/lib/libfaiss_c.dylib /opt/homebrew/lib/libfaiss_c.dylib; do
    if [[ -e "$f" ]]; then
      echo "libfaiss_c found: $f"
      return
    fi
  done

  cat >&2 <<'EOF'

libfaiss_c was not found. The backend links blevesearch's FAISS fork through
cgo, so nothing here compiles until it is installed. Any one of:

  system-wide (needs root once):
    sudo apt-get install -y cmake ninja-build g++ libopenblas-dev
    sudo scripts/faiss-build.sh --prefix /usr/local && sudo ldconfig

  in your home directory (no root):
    scripts/faiss-build.sh --prefix "$HOME/.local/faiss"
    export CGO_CFLAGS=-I$HOME/.local/faiss/include
    export CGO_LDFLAGS=-L$HOME/.local/faiss/lib
    export LD_LIBRARY_PATH=$HOME/.local/faiss/lib

  out of the Docker build, needing no local toolchain at all:
    docker buildx build --target faiss --output type=local,dest=./.faiss .

docs/setup.md has the details.
EOF
  fail "FAISS preflight"
}

faiss_preflight

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

echo "No verification overlay found: running unit tests and the frontend build."
echo "API and browser e2e will NOT run."

stage "Unit tests"
(cd backend && go test ./... -count=1) || fail "unit tests"

stage "Vet"
(cd backend && go vet ./...) || fail "vet"

stage "Frontend unit tests"
(cd frontend && pnpm test) || fail "frontend unit tests"

stage "Frontend build"
# Nothing else here compiles the SPA, so without this a broken tsc, Vite or
# VitePress would go unnoticed on a run that skipped the overlay.
(cd frontend && pnpm run build) || fail "frontend build"

echo ""
echo "Reduced verification passed. API and browser e2e did not run."
