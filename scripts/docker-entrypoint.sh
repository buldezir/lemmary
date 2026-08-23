#!/bin/sh
set -e

# Default to serving on $PORT so the server and the image HEALTHCHECK
# always agree on a single port. Explicit args (serve with custom flags,
# migrate, superuser, ...) are passed through untouched.
if [ "$#" -eq 0 ]; then
    set -- serve --http="0.0.0.0:${PORT:-80}"
fi

exec /app/paperless-go "$@"
