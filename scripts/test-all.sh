#!/usr/bin/env bash
# Run the verification suite. The stages live in the overlay repository
# (see scripts/overlay.sh). No overlay is a failure, not a reduced pass.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# bleve compiles its kNN API out without the vectors tag (see
# internal/fulltext/vectors_required.go). Exported so the overlay's own go
# commands inherit it.
export GOFLAGS=-tags=vectors
export CGO_ENABLED=1

ARGS=()
[[ "${1:-}" == --no-sync ]] && { ARGS+=(--no-sync); shift; }

if ! OVERLAY="$("$ROOT/scripts/overlay.sh" "${ARGS[@]+"${ARGS[@]}"}")"; then
  echo "No verification overlay found." >&2
  echo "scripts/overlay.sh looks in \$LEMMARY_DEV, $ROOT/dev, and the lemmary-dev sibling." >&2
  exit 3
fi

# LEMMARY_ROOT tells the overlay which tree to test.
exec env LEMMARY_ROOT="$ROOT" "$OVERLAY/scripts/test-all.sh" "$@"
