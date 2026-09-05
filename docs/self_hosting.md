# Self-hosting with Docker

The published image carries everything Lemmary needs — the Go binary, the built
SPA and these docs, poppler for PDF work, and the FAISS build its vector search
links against. Nothing has to be installed on the host but Docker.

To run from source instead, see [Development environment](/development).

## Quick start

```bash
cp .env.example .env
# Optional: put an AI key in .env so the first boot skips the wizard.
docker compose up -d
```

Open [http://127.0.0.1:8090](http://127.0.0.1:8090). On a fresh volume the
in-app [setup wizard](/setup#first-launch-setup-wizard) creates the admin
account and collects the OCR and language-model keys; a single Mistral key
covers both. See [AI providers](/ai_providers) for what to put in `.env`.

`docker-compose.yml` is deliberately short:

```yaml
services:
  app:
    image: ghcr.io/buldezir/lemmary:latest
    restart: unless-stopped
    env_file: [.env]
    ports: ["8090:${PORT:-80}"]
    volumes: [app_data:/app/pb_data]
```

Add `--build` to build the image locally from the `Dockerfile` instead of
pulling it. Published tags are `latest` (default branch), the release version
(`1.2.3`, `1.2`, `1`) and the commit sha, for `linux/amd64` and `linux/arm64`.

## What lives where

For the short architectural overview, including S3 document storage and where
embedding vectors live, see [Storage](/storage).

With the default local file storage, everything stateful is under
`/app/pb_data`, on the `app_data` volume:

| Path | Contents |
| --- | --- |
| `data.db` | documents, metadata, settings, chats — including OCR text, stored inline |
| `storage/` | the uploaded original files and generated thumbnails |
| `bleve/documents`, `bleve/chunks` | the [search indexes](/setup#full-text-search) — derived data, rebuilt on the next boot if deleted |

When sizing the volume, budget for the database as well as the files: extracted
text is stored in the row, and embeddings add roughly 30–60 KB per document.

The container starts as root only long enough to adopt a volume an older
root-running image created, then drops to the unprivileged `app` user
(uid/gid 1000) for good — it renders untrusted PDFs through poppler, and an
exploit there should not be root.

## Ports and reverse proxies

The server listens on `$PORT` (80 in the image) and the compose file publishes
it on `8090`. The image's healthcheck polls `/api/health` with a generous
120-second start period, because the first boot runs migrations and may rebuild
the search index over an archive that is already large.

The SPA calls whatever origin it was served from, so **no URL has to be
configured for the container** — `VITE_POCKETBASE_URL` in `.env` only affects a
frontend built from source. Put a TLS-terminating proxy in front and two things
are worth setting explicitly:

- [`PASSKEY_RP_ID` and `PASSKEY_ORIGINS`](/setup#always-env-backed) when the
  proxy does not forward the original `Host` or `X-Forwarded-Proto`. Every
  enrolled passkey is bound to `PASSKEY_RP_ID`, so changing it later makes all
  of them unusable.
- `IMPORT_ALLOW_PRIVATE=1` if you import from a Paperless-ngx instance on the
  same LAN or Docker network. Cloud-metadata addresses stay blocked either way.

### Traefik

An overlay, so the base file stays usable on its own:

```yaml
# docker-compose.traefik.yml
#   docker compose -f docker-compose.yml -f docker-compose.traefik.yml up -d
services:
  app:
    # Traefik reaches the container over the shared network, so nothing needs
    # publishing on the host any more. (!override replaces the base list.)
    ports: !override []
    networks: [proxy]
    labels:
      traefik.enable: "true"
      # Which network to dial when the container is on more than one.
      traefik.docker.network: proxy
      traefik.http.routers.lemmary.rule: "Host(`archive.example.com`)"
      traefik.http.routers.lemmary.entrypoints: websecure
      traefik.http.routers.lemmary.tls.certresolver: letsencrypt
      # The port inside the container, not the 8090 the base file published.
      traefik.http.services.lemmary.loadbalancer.server.port: "${PORT:-80}"

networks:
  proxy:
    external: true
```

That assumes Traefik is already running with a `websecure` entrypoint, a
`letsencrypt` certificate resolver and the Docker provider, on an external
network named `proxy`.

Three things this setup gets right without configuration, each of which a
different proxy can get wrong:

- **Passkeys need no variables.** Traefik forwards the original `Host` and sets
  `X-Forwarded-Proto`, which is exactly what `PASSKEY_RP_ID` and
  `PASSKEY_ORIGINS` derive themselves from when unset.
- **Uploads are not capped by the proxy.** Traefik buffers no request body by
  default, so a 20 MB document and a gigabyte-scale staged archive both pass
  through — there is no `client_max_body_size` to raise. If you add a
  `buffering` middleware, set its `maxRequestBodyBytes` past
  `IMPORT_STAGING_MAX_BYTES` or archive uploads start failing.
- **Deep Search keeps streaming.** `POST /api/app/search/stream` is
  server-sent events, and Traefik streams responses rather than buffering them,
  so each search, read and survey still appears as it happens. Do not put a
  `compress` middleware in front of it without excluding `text/event-stream`,
  or the steps arrive in one lump at the end.
- **Long answers need room.** A research run is minutes of work, and the
  stream sends a comment frame every 15 seconds so the connection is never
  idle. That satisfies `idleTimeout`, but **not**
  `respondingTimeouts.writeTimeout`: like Go's `http.Server.WriteTimeout`, it
  is an absolute deadline from the start of the response, and no amount of
  heartbeat refreshes it. Leave it at 0 on the entrypoint that serves the app,
  or set it past the longest run you expect. `forwardingTimeouts.responseHeaderTimeout`
  matters for the same reason — it caps the wait for the backend's first byte.
  Losing the connection no longer loses the answer (the run finishes and the
  turn is saved either way), but the user watching it does lose the progress.

With [encryption at rest](/encryption), add `VAULT_ALLOW_INSECURE_GATE=1`: the
unlock gate refuses a non-loopback bind address, and inside a container the app
binds `0.0.0.0` whether the port is published or only reachable from Traefik —
it cannot tell the difference from in there. TLS then ends at Traefik, so the
unlock password crosses the Docker network in cleartext. That is a different
trade from the loopback publication `docker-compose.encrypted.yml` makes, and
worth making deliberately.

## Day-to-day

```bash
# Create or reset the admin account (never resets a password that exists)
docker compose exec -u app app /app/lemmary superuser upsert admin@example.com 'your-password'

# Logs; LOG_LEVEL=info in .env turns on JSON slog on stdout
docker compose logs -f app

# Upgrade
docker compose pull && docker compose up -d
```

Migrations run at startup, so an upgrade is a pull and a restart. Explicit
arguments are passed through to the binary untouched, which is what makes
`exec` and `docker compose run --rm app <subcommand>` work.

## Backups

Two kinds, and they answer different questions:

- **In-app export** — any signed-in user downloads their own library as one zip
  (files, OCR text, metadata, thumbnails, taxonomy) and restores it into this or
  another instance. It carries no settings and no API keys. See
  [Backup and restore](/setup#backup-and-restore).
- **Volume snapshot** — the whole instance, settings and keys included. Stop the
  container first so SQLite is not copied mid-write:

  ```bash
  docker compose stop
  docker run --rm -v lemmary_app_data:/v -v "$PWD:/out" alpine \
    tar czf /out/lemmary-pb_data.tar.gz -C /v .
  docker compose start
  ```

## Instance limits and resources

The `LIMIT_*` family bounds how much one instance may hold and is read at
startup only, never from Settings — change one by recreating the container. All
of them are unlimited when unset. See
[Instance limits](/setup#instance-limits).

The image pins `OPENBLAS_NUM_THREADS=1` and `OMP_NUM_THREADS=1`: OpenBLAS and
OpenMP each start a thread per core by default, inside a process that is already
serving requests concurrently, and one thread apiece keeps a vector search from
stalling the rest of the server on a small machine.

## Local OCR

By default OCR is a hosted API, which means every scan is uploaded to somebody
else. A second overlay puts the OCR engine on this host instead — a sidecar
container with no port published and no API key:

```bash
docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml \
  --profile docling up -d
```

Then `OCR_SDK=docling` in `.env` on a fresh volume, or a Docling provider under
**Settings → Providers** on an instance that has already booted. It is not free:
a multi-gigabyte image, several gigabytes of RAM, and seconds a page instead of
milliseconds. Read [Local OCR](/local_ocr) first, in particular the timeout,
which has to go up.

## Encryption at rest

`VAULT_ENABLED=1` makes the volume ciphertext-only and boots the instance
locked. It needs a memory-backed working directory the app cannot arrange for
itself, which is what the overlay provides:

```bash
docker compose -f docker-compose.yml -f docker-compose.encrypted.yml up -d
```

The overlay also sizes the tmpfs, disables container swap, and republishes the
port on loopback only. Read [Encryption at rest](/encryption) before enabling
it: losing every account password *and* the recovery code loses the archive, and
there is no operator override.

## Local embeddings

Deep Search can find documents by meaning rather than only by keyword, which
needs an embedding model. Two more overlays run one on this host instead of
sending every passage of every document to a hosted provider:

```bash
# CPU, runs anywhere
docker compose -f docker-compose.yml -f docker-compose.embeddings.yml up -d

# NVIDIA GPU -- instead of the CPU file, not alongside it
docker compose -f docker-compose.yml -f docker-compose.embeddings-gpu.yml up -d
```

They add a sidecar with no published port and bind it, changing nothing else:
OCR, extraction and chat stay wherever they were. Overlays stack, so encryption
and local embeddings compose:

```bash
docker compose -f docker-compose.yml \
               -f docker-compose.encrypted.yml \
               -f docker-compose.embeddings.yml up -d
```

Budget for the model on top of the vectors — ~2.2 GB of weights for the default
`BAAI/bge-m3` — and note that under a vault the vectors themselves live in the
tmpfs. See [Embeddings on your own
hardware](/local_embeddings).
