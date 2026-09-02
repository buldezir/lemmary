#!/usr/bin/env bash
# Run the verification suite. The stages live in the overlay repository
# (see scripts/overlay.sh). No overlay is a failure, not a reduced pass.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

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
