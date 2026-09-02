#!/usr/bin/env bash
# Start the dev servers for this checkout.
#
# The runner itself lives in the overlay repository (see scripts/overlay.sh);
# this resolves it and tells it which tree to run. The overlay starts the
# servers inside Docker (lemmary-verify:local) on the first free host port from
# 8081 and prints the URL. LEMMARY_DEV_HOST=1 runs on the host instead.
# Options are passed straight through -- try --help.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if ! OVERLAY="$("$ROOT/scripts/overlay.sh")"; then
  echo "No overlay found, so there is no dev runner here." >&2
  echo "Start the two halves by hand instead:" >&2
  echo "  (cd backend && go run . serve --http=127.0.0.1:8090)" >&2
  echo "  (cd frontend && pnpm run dev)" >&2
  exit 3
fi

exec env LEMMARY_ROOT="$ROOT" "$OVERLAY/scripts/dev.sh" "$@"
