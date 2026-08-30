FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-builder

WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .

ARG TARGETOS
ARG TARGETARCH

# EDITION names a private edition to build into this image; empty builds the
# open-source one. It is passed to both stages from a single build argument on
# purpose: the backend half is selected by a Go build tag and the frontend half
# by a module alias, and an image with one of the two would serve pages whose
# API routes do not exist.
ARG EDITION=""
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags "${EDITION:+lemmary_$EDITION}" -o lemmary .

FROM node:26-alpine AS frontend-builder

WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ .
COPY docs/ /app/docs/

ARG EDITION=""
RUN LEMMARY_EXT="${EDITION:+./src/ext-$EDITION}" npm run build

FROM alpine:3.21
LABEL org.opencontainers.image.title="Lemmary" \
      org.opencontainers.image.description="Source-available document storage with OCR and AI metadata extraction" \
      org.opencontainers.image.source="https://github.com/buldezir/lemmary" \
      org.opencontainers.image.licenses="PolyForm-Noncommercial-1.0.0"
RUN apk add --no-cache poppler-utils su-exec
WORKDIR /app
# The app runs as this user, not root — the entrypoint drops privileges after
# adopting any pre-existing volume. pb_data is created here so a fresh named
# volume inherits its ownership instead of being initialised root-owned.
RUN adduser -D -H -u 1000 app && mkdir -p /app/pb_data && chown app:app /app/pb_data
COPY --from=backend-builder /app/backend/lemmary /app/lemmary
COPY --from=frontend-builder /app/public /app/public
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

ENV PORT=80
EXPOSE ${PORT}
# start-period is generous because first boot is the slow one: migrations, and
# rebuilding the search index over an archive that may already be large. A build
# that has more to do before it can serve (restoring or unpacking a data
# directory in a pre-boot step) needs the room too, and a start-period only
# delays when Docker starts reporting unhealthy — it costs a fast boot nothing.
HEALTHCHECK --interval=30s --timeout=3s --start-period=120s --retries=3 \
    CMD wget -q --spider "http://127.0.0.1:${PORT}/api/health" || exit 1
ENTRYPOINT ["/app/docker-entrypoint.sh"]
