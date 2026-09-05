# The backend links FAISS through bleve's vector search, so the image is
# glibc-based end to end: blevesearch's FAISS fork, go-faiss and bleve are all
# built and tested against glibc, and musl's 128 KiB default thread stacks are a
# poor fit for FAISS's OpenMP worker threads. Debian also gives us multiarch,
# which is what keeps the arm64 image a native-speed cross build instead of an
# hour under QEMU.

# FAISS is compiled on the build machine and cross-compiled for the target, so
# this stage never runs under emulation. The pinned commit lives in the script;
# this layer is rebuilt only when the script changes.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS faiss-build

ARG TARGETARCH

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-cache \
    && if [ "$TARGETARCH" != "$(dpkg --print-architecture)" ]; then dpkg --add-architecture "$TARGETARCH"; fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends git cmake ninja-build g++ libopenblas-dev \
    && if [ "$TARGETARCH" = arm64 ] && [ "$TARGETARCH" != "$(dpkg --print-architecture)" ]; then \
         apt-get install -y --no-install-recommends g++-aarch64-linux-gnu libopenblas-dev:arm64; \
       fi

COPY scripts/faiss-build.sh /usr/local/bin/faiss-build.sh
RUN /usr/local/bin/faiss-build.sh --prefix /opt/faiss --target-arch "$TARGETARCH"

# Just the artifacts, so `docker buildx build --target faiss
# --output type=local,dest=./.faiss .` gives a developer lib/ and include/
# rather than a builder's whole root filesystem. See docs/development.md.
FROM scratch AS faiss
COPY --from=faiss-build /opt/faiss/ /

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS backend-builder

ARG TARGETOS
ARG TARGETARCH

# Linking against libfaiss.so drags in the libraries it was itself linked
# against -- OpenBLAS and libgomp -- so they have to be here even for a native
# build. For a cross build they have to be here twice: multiarch installs the
# target's copies alongside the host's, next to the target's compiler.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-cache \
    && if [ "$TARGETARCH" = arm64 ] && [ "$TARGETARCH" != "$(dpkg --print-architecture)" ]; then \
         dpkg --add-architecture arm64; \
       fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends libopenblas0-pthread libgomp1 \
    && if [ "$TARGETARCH" = arm64 ] && [ "$TARGETARCH" != "$(dpkg --print-architecture)" ]; then \
         apt-get install -y --no-install-recommends \
           gcc-aarch64-linux-gnu libc6-dev-arm64-cross \
           libopenblas0-pthread:arm64 libgomp1:arm64; \
       fi

COPY --from=faiss / /opt/faiss/

WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY backend/ .

# -tags vectors is not optional: without it bleve compiles out SearchRequest.KNN
# and the backend does not build at all (see internal/fulltext/vectors_required.go).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    set -eu; \
    case "$TARGETARCH" in \
      amd64) CC=gcc; EXTRA_LDFLAGS= ;; \
      arm64) CC=aarch64-linux-gnu-gcc; EXTRA_LDFLAGS=-L/usr/lib/aarch64-linux-gnu ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    CGO_ENABLED=1 \
    CC="$CC" \
    GOOS="$TARGETOS" \
    GOARCH="$TARGETARCH" \
    CGO_CFLAGS="-I/opt/faiss/include" \
    CGO_LDFLAGS="-L/opt/faiss/lib -Wl,-rpath-link,/opt/faiss/lib $EXTRA_LDFLAGS" \
    go build -tags vectors -o lemmary .

FROM node:26-alpine AS frontend-builder

RUN npm install -g pnpm

WORKDIR /app/frontend
# pnpm-workspace.yaml carries the allowBuilds list, without which the install
# refuses to run esbuild's postinstall and VitePress cannot build.
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ .
COPY docs/ /app/docs/

RUN pnpm run build

FROM debian:trixie-slim
LABEL org.opencontainers.image.title="Lemmary" \
      org.opencontainers.image.description="Source-available document storage with OCR and AI metadata extraction" \
      org.opencontainers.image.source="https://github.com/buldezir/lemmary" \
      org.opencontainers.image.licenses="PolyForm-Noncommercial-1.0.0"
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
         poppler-utils libopenblas0-pthread libgomp1 wget ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# FAISS is only ever loaded by the binary next to it, so the two libraries go to
# /usr/local/lib and ldconfig makes them findable without an rpath. faiss.ref
# rides along so a running image can be asked which FAISS it has, the same way
# CI asks a runner.
COPY --from=faiss /lib/libfaiss.so /lib/libfaiss_c.so /lib/faiss.ref /usr/local/lib/
RUN ldconfig
WORKDIR /app
# The app runs as this user, not root — the entrypoint drops privileges after
# adopting any pre-existing volume. pb_data is created here so a fresh named
# volume inherits its ownership instead of being initialised root-owned.
RUN groupadd --system --gid 1000 app \
    && useradd --system --uid 1000 --gid 1000 --no-create-home app \
    && mkdir -p /app/pb_data && chown app:app /app/pb_data
COPY --from=backend-builder /app/backend/lemmary /app/lemmary
COPY --from=frontend-builder /app/public /app/public
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# OpenBLAS and OpenMP each start a thread per core by default, inside a process
# that is already serving requests concurrently. One thread apiece keeps a
# vector search from stalling the rest of the server on a small machine.
ENV OPENBLAS_NUM_THREADS=1 \
    OMP_NUM_THREADS=1
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
