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
# Starts as root only to adopt volumes created by older root-running images,
# then drops privileges. :80 still binds: Docker sets
# net.ipv4.ip_unprivileged_port_start=0.
#
# setpriv: util-linux is Essential on Debian, so nothing to install.
# --init-groups, or the process keeps root's supplementary groups.
if [ "$(id -u)" = "0" ]; then
    if [ -d /app/pb_data ] && [ "$(stat -c %u /app/pb_data)" != "$(id -u app)" ]; then
        chown -R app:app /app/pb_data
    fi
    exec setpriv --reuid=app --regid=app --init-groups /app/lemmary "$@"
fi

exec /app/lemmary "$@"
