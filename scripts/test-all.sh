#!/usr/bin/env bash
# Run the verification suite. The stages live in the overlay repository
# (see scripts/overlay.sh). No overlay is a failure, not a reduced pass.
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

if ! OVERLAY="$("$ROOT/scripts/overlay.sh" "${ARGS[@]+"${ARGS[@]}"}")"; then
  echo "No verification overlay found." >&2
  echo "scripts/overlay.sh looks in \$LEMMARY_DEV, $ROOT/dev, and the lemmary-dev sibling." >&2
  exit 3
fi

# LEMMARY_ROOT is how the overlay learns which tree to test. It no longer
# infers that from its own location, which is what let it move out of the
# tree in the first place.
exec env LEMMARY_ROOT="$ROOT" "$OVERLAY/scripts/test-all.sh" "$@"
