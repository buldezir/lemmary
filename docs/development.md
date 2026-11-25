# Development environment

The supported installation path is the published [Docker image](/self_hosting).
This guide is only for contributors and anyone who deliberately wants to build
or run Lemmary directly on the host.

## Prerequisites

- Go 1.27+ with cgo enabled (a C toolchain: `gcc` or `clang`)
- Node.js 20+
- [pnpm](https://pnpm.io/installation) 11+ (`npm install -g pnpm`)
- PDFium for all PDF work (see [PDFium](#pdfium) below)

These host dependencies are intentionally not prerequisites for ordinary
self-hosting: they are already included in the Docker image.

## FAISS

FAISS is required to build the backend. Search is backed by
[Bleve](https://github.com/blevesearch/bleve), whose vector support is a cgo
binding to FAISS. Bleve compiles that API out unless the `vectors` build tag is
set, and Lemmary is always built with it: one binary and one image, with vector
search included. A build without the tag stops immediately in
`backend/internal/fulltext/vectors_required.go`.

The library has to be **Bleve's fork** of FAISS. A distribution `libfaiss`
package is not enough because the Go binding calls C entry points that only
exist in the fork. `scripts/faiss-build.sh` owns the pinned commit.
It is the single source of truth and moves only when Bleve moves, following the
compatibility table in Bleve's `docs/vectors.md`.

Every route below also needs OpenBLAS and libgomp when the backend is linked and
when it runs (`apt install libopenblas0-pthread libgomp1`;
`libopenblas-dev` brings them along).

```bash
# Option 1 — install system-wide. One sudo, and nothing to set afterwards.
sudo apt install cmake ninja-build g++ libopenblas-dev
sudo scripts/faiss-build.sh --prefix /usr/local && sudo ldconfig

# Option 2 — install in your home directory. Same build, no root.
scripts/faiss-build.sh --prefix "$HOME/.local/faiss"

# Option 3 — export the artifacts from the Docker build. This needs no local
# cmake or compiler; the exported FAISS stage is only about 10 MB.
docker buildx build --target faiss --output type=local,dest=./.faiss .
mkdir -p "$HOME/.local/faiss" && cp -a .faiss/lib .faiss/include "$HOME/.local/faiss/"
```

Options 2 and 3 put FAISS outside the compiler and loader's normal paths:

```bash
export CGO_CFLAGS=-I$HOME/.local/faiss/include
export CGO_LDFLAGS=-L$HOME/.local/faiss/lib
export LD_LIBRARY_PATH=$HOME/.local/faiss/lib
```

The repository's `.envrc` sets all three when `~/.local/faiss` exists. With
[direnv](https://direnv.net), run `direnv allow`; it also exports
`GOFLAGS=-tags=vectors`, making bare `go build`, `go test`, and gopls work in
this tree. Without direnv, pass `-tags vectors` or set it once with
`go env -w GOFLAGS=-tags=vectors`.

FAISS only has to be on the host for Go commands run there: a plain
`go build`/`go test`, or host-mode verification. The normal verification suite
runs in Docker, where it is already installed.

On macOS, run `brew install cmake ninja libomp openblas`, then use option 1 or
2; the build script discovers Homebrew's libomp automatically.

## PDFium

All PDF work — page counts, previews and thumbnails, page text, splitting and
merging, removing a password — goes through [PDFium](https://pdfium.googlesource.com/pdfium/), linked
through cgo with [go-pdfium](https://github.com/klippa-app/go-pdfium). The
backend runs it in worker processes started from its own binary, one per call,
so a crash or a hang inside PDFium costs that call rather than the server.

There is no distribution package to install. `scripts/pdfium-fetch.sh` downloads
the prebuilt library from
[bblanchon/pdfium-binaries](https://github.com/bblanchon/pdfium-binaries) at the
release it pins, verifies the checksum, and installs it with the pkg-config file
cgo finds it through. The Docker build runs the same script.

```bash
# System-wide: nothing to set afterwards (on macOS, skip ldconfig).
sudo scripts/pdfium-fetch.sh --prefix /usr/local && sudo ldconfig

# In your home directory, no root.
scripts/pdfium-fetch.sh --prefix "$HOME/.local/pdfium"
```

The home-directory install is outside the compiler and loader's normal paths.
The repository's `.envrc` sets both when `~/.local/pdfium` exists; without
direnv, export them yourself:

```bash
export PKG_CONFIG_PATH=$HOME/.local/pdfium/lib/pkgconfig
export LD_LIBRARY_PATH=$HOME/.local/pdfium/lib   # DYLD_LIBRARY_PATH on macOS
```

PDFium cannot encrypt, so the tests that need a password-protected PDF build it
with [qpdf](https://qpdf.readthedocs.io/) (`apt install qpdf`,
`brew install qpdf`) and are skipped without it. The app itself does not use it.

## Run from source

Copy the example environment first. Runtime options are explained in the
[Configuration Guide](/setup), with AI-specific options in
[AI providers and models](/ai_providers).

```bash
cp .env.example .env
```

Start the backend:

```bash
cd backend
go run . serve --http=127.0.0.1:8090
```

On first run, migrations create the PocketBase collections:

- `tags`
- `correspondents`
- `document_types`
- `documents`
- `processing_jobs`
- `app_settings`
- `ai_providers`
- `outbound_emails`

The app then opens the same
[first-launch wizard](/setup#first-launch-setup-wizard) as a Docker
installation. `app_settings` and `ai_providers` are seeded from `.env` on first
boot.

Build the frontend and documentation once, then restart the backend:

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm run build
```

This writes the app to `public/` and the docs to `public/docs/`; the backend
serves both from its own address.

## Useful commands

```bash
# Frontend production build (SPA -> ../public, docs -> ../public/docs)
cd frontend && pnpm run build

# Create or update an admin (PocketBase superuser + paired users account)
cd backend && go run . superuser upsert admin@example.com 'your-password'
```
