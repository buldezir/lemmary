#!/bin/sh
set -e

# Default to serving on $PORT so the server and the image HEALTHCHECK
# always agree on a single port. Explicit args (serve with custom flags,
# migrate, superuser, ...) are passed through untouched.
if [ "$#" -eq 0 ]; then
    set -- serve --http="0.0.0.0:${PORT:-80}"
fi

# The app runs as the unprivileged "app" user: it renders untrusted PDFs
# through poppler, and an exploit there should not be root in the container.
# The container itself starts as root only so that volumes created by older
# images (which ran everything as root) can be adopted — ownership is fixed
# once, then privileges are dropped for good. Binding :80 still works because
# Docker starts containers with net.ipv4.ip_unprivileged_port_start=0.
if [ "$(id -u)" = "0" ]; then
    if [ -d /app/pb_data ] && [ "$(stat -c %u /app/pb_data)" != "$(id -u app)" ]; then
        chown -R app:app /app/pb_data
    fi
    exec su-exec app /app/lemmary "$@"
fi

exec /app/lemmary "$@"
