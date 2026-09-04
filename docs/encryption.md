# Encryption at rest

Off by default, in every build. Set `VAULT_ENABLED=1` and the persistent volume
holds only ciphertext.

## Configuration

All of these are environment variables, read before PocketBase exists — a
toggle stored in `app_settings` would live inside the very database it is meant
to protect, and could not be consulted before that database is decrypted. The
commented block in `.env.example` has them all.

| Variable | Default | Meaning |
| --- | --- | --- |
| `VAULT_ENABLED` | unset (off) | Set to `1` to encrypt the data directory at rest. The instance then boots **locked** and waits for a sign-in. |
| `VAULT_DIR` | `pb_data` | Where the encrypted vault lives (the persistent volume). |
| `VAULT_WORKDIR` | `pb_work` beside `VAULT_DIR` | Where the archive is decrypted. Must be a memory-backed filesystem (tmpfs); startup fails otherwise. |
| `VAULT_PASSPHRASE` | unset | Unlocks without the web form, for CLI subcommands and tests. Not how a server should normally run: it sits next to the ciphertext it protects. |
| `VAULT_ALLOW_DISK_WORKDIR` | unset | Permits a working directory that is not memory-backed, **or one that cannot be checked** — the filesystem-type test is Linux-only, so this is required to run off Linux at all. Development only — it means plaintext on disk. |
| `VAULT_ALLOW_SHRINK` | unset | Permits a flush that would drop more than half the archive. |
| `VAULT_ALLOW_INSECURE_GATE` | unset | Permits serving the unlock form on an address reachable from off this host. Only if you accept sending the archive password in the clear. |
| `VAULT_KEEP_GENERATIONS` | `3` | How many generations are retained, i.e. how far back a rollback can reach. |

Every `VAULT_*` switch above is a strict boolean: `1`/`true`/`yes`/`on` or
`0`/`false`/`no`/`off`, and anything else refuses to start rather than guessing.
`VAULT_ENABLED=Y` reading as "off" would run an install you believed was
encrypted, filling the volume with plaintext while none of the guarantees below
held — so it is an error, not a default.

The reverse mistake is caught too: starting **without** `VAULT_ENABLED` against a
volume that holds a vault is refused. Otherwise PocketBase would find no
database there, create one, and serve a fresh setup wizard beside the
ciphertext — an archive that appears to have lost every document, and a plaintext
database now written into the volume that was supposed to hold only ciphertext.
Forgetting `-f docker-compose.encrypted.yml` is all it takes, so it fails loudly
instead.


With `VAULT_ENABLED=1` the persistent volume holds only ciphertext: the SQLite
databases and every uploaded document and preview. A stolen disk, a leaked volume
snapshot, a `docker cp` of a stopped container, or an operator browsing the
filesystem all yield nothing. The full-text index is not on the volume in any
form — it is derived data, so it is rebuilt into the memory-backed working
directory on each unlock rather than encrypted and stored, which keeps a
plaintext shadow of every document's OCR text off the disk entirely.

Deep Search's passage vectors follow the same split. The vectors themselves live
in `data.db`, so they are encrypted on the volume like every other row and are
covered by the vault's snapshots and by PocketBase's backups without any extra
step. The vector index built from them is derived data like the text index: it is
rebuilt into the working directory on each unlock, from the stored vectors,
without a single request to the embedding provider — so unlocking costs nothing
and works with the provider offline.

**Read this whole section before enabling it.** It changes how the instance
starts, what happens when a password is lost, and which features are available.

## How it works

The volume holds a keyring, a chain of sealed manifests, and a content-addressed
store of encrypted blobs — nothing else. On start the instance is **locked**: it
serves an unlock page and answers every API route with `423`. The first sign-in
supplies the credential that unwraps the master key, the archive is decrypted
into a memory-backed working directory, and PocketBase is pointed at it. From
then on the instance behaves exactly like an unencrypted install until it stops.

Changes are flushed back to the volume about ten seconds after the last write, on
a one-minute cron, and on shutdown.

The master key is sealed once per credential, so **any user of the instance can
unlock it** — every account's password is enrolled automatically when it is
created or changed, alongside a recovery code. Encryption is instance-wide: once
unlocked, all users see all data, exactly as they do today. For SaaS, isolation
comes from giving each customer their own container and volume.

## What it protects, and what it does not

It defends against offline access to the volume: theft or loss of the disk,
a leaked snapshot, a backup copied off the host, a decommissioned drive.

It does **not** defend against anyone who controls the running process. Once
unlocked the key is in memory, and whoever runs the binary can modify it to
capture the key at unlock. This is at-rest encryption, not zero-knowledge, and it
should not be described as the latter. End-to-end encryption is incompatible with
this product: OCR sends documents to Google Vision or Mistral, extraction, chat
and deep search send OCR text to an LLM, and both full-text search and the SQL
filters need plaintext on the server.

Also outside the boundary: container logs on the host stay plaintext.

**Revoking an account does not always revoke its key to the archive.** The
keyring is what unlocks the volume, and it is separate from the login settings
by necessity: it has to be readable before PocketBase exists, because the users
collection it would otherwise consult is inside the database still waiting to be
decrypted. Two consequences an operator has to know about, because neither is
visible from the admin UI:

- Turning password authentication off stops `POST /api/token`, Basic auth and
  the PocketBase login routes. It does **not** stop those same passwords
  unlocking the archive at the boot gate, which cannot see that setting.
- Deleting a user normally removes their wrap with them. Deleting the **last**
  remaining account does not: the keyring refuses to drop its only credential,
  since doing so would destroy the archive. An offboarded final admin's password
  therefore still decrypts the volume.

Where either matters, rotate: unlock, enrol the credential you intend to keep,
and delete the account whose key should stop working while another account
remains.

And the volume itself is not opaque. The blob store reveals how many files an
instance holds, the plaintext size of each one — the AEAD is length-preserving —
and, content addresses being deterministic within a vault, whether two blobs are
identical across snapshots. That is inherent to a content-addressed store and is
not something a passphrase changes.

## Keep the port private until the vault is initialised

An uninitialised vault sets its master password from the first request that
reaches it, and hands that visitor the recovery code. Until `vault init` has run
or you have set the password yourself, whoever reaches the port first owns the
instance.

Publish it on loopback behind a TLS-terminating proxy — which is what
`docker-compose.encrypted.yml` does — or run `vault init` at provisioning time
so there is no window. Over plain HTTP on a LAN the gate's same-origin checks do
not stop DNS rebinding, so "nobody else is on this network" is not the guarantee
it sounds like.

## Enabling it on an install that already has data

You cannot. Startup refuses to initialise a vault in a directory that already
holds an unencrypted install, because doing so would begin with an empty archive
and leave the real documents beside it in the clear.

Migrate deliberately: bring up the encrypted instance against an **empty**
`VAULT_DIR` on a **new** volume, re-import the documents, then destroy the old
volume. Deleting the old files in place is not enough — their contents stay
recoverable from free space, which is not encryption at rest in any useful sense.

## Losing access

Losing every password **and** the recovery code loses the archive. There is no
operator override, by design.

The code generated at first boot is returned to the browser that initialised the
vault, once. It is deliberately **not** written to the log: container logs land
unencrypted on the same host disk this feature protects against, and a recovery
wrap cannot be revoked afterwards.

## Creating a vault non-interactively

`lemmary vault init` creates the keyring for a brand new instance and is the only
path to the recovery code when no browser initialised it:

```bash
VAULT_ENABLED=1 VAULT_PASSPHRASE='...' lemmary vault init
```

| Outcome | Exit | Output |
| --- | --- | --- |
| Created a vault | 0 | `vault-recovery-code: <code>` on stdout |
| A keyring is already there | 0 | `vault: already initialised` on stdout |
| Anything else | 1 | reason on stderr |

Re-running it is safe, so a provisioning step can retry without a special case
for "maybe it worked". It does **not** bootstrap PocketBase — no migrations, no
database, no bleve — which is deliberate: nothing should be able to reach an
un-bootstrapped setup wizard at the one moment a fresh archive is unlocked with
no account in it.

The password given here seals a **bootstrap credential**, and it is revoked
automatically the first time a real account is enrolled. That matters because
this password reaches the instance through whatever provisioned it — its memory,
and the environment of a container anybody with the daemon socket can inspect —
so it must not remain a valid key to the archive for the life of the instance.
Practically: use the account's own password as `VAULT_PASSPHRASE`, create the
account in the same provisioning run, and record the recovery code as you go.
After that, the instance unlocks with account passwords only.

Leaving `VAULT_PASSPHRASE` set afterwards is harmless. Once the bootstrap wrap is
revoked the passphrase no longer opens anything, and rather than failing startup
— which would crash-loop the container on every restart — the server logs that
the passphrase did not work and serves the unlock form as usual. Removing it from
the environment is still tidier.

## Serving the unlock page over TLS

The unlock form carries the password that decrypts the whole archive, so it must
not be served in cleartext. Terminate TLS in front and bind the app locally:

```bash
lemmary serve --http 127.0.0.1:8090   # behind nginx/Caddy/Traefik
```

The gate that serves this form runs *before* PocketBase exists, so it speaks
plain HTTP and has no TLS configuration to fall back on. Binding a **loopback**
address is therefore the only configuration it can verify is safe by itself:

- `serve --http 127.0.0.1:8090` — allowed. Reachable only through a proxy on this
  host, which is where TLS belongs.
- `serve yourdomain.com` (PocketBase's built-in autocert) — refused. In that mode
  PocketBase serves HTTPS on `:443` and uses `:80` only to redirect, but nothing
  listens on `:443` while the instance is locked, so a browser reaching `:80` has
  no TLS to fall back to.
- any other non-loopback address, `--http 0.0.0.0:8090` included — refused unless
  `VAULT_ALLOW_INSECURE_GATE=1`.

**In a container that last case is unavoidable and is not itself a problem.** The
app must bind `0.0.0.0` inside the container or Docker could not publish the port
at all, and from in there it cannot tell a port published on `127.0.0.1` from one
published LAN-wide — that decision lives in the compose file. So the compose file
is where it is stated: `docker-compose.encrypted.yml` publishes the port on
`127.0.0.1` only *and* sets `VAULT_ALLOW_INSECURE_GATE=1` to say that it has done
so. The two lines belong together. **If you widen that port mapping, remove the
flag**, or you are carrying the archive's password in cleartext to wherever the
port now reaches.

What this catches is the configuration that has neither: enabling the
`VAULT_*` block in `.env.example` on the base `docker-compose.yml`, which
publishes on every host interface. That used to start and serve the unlock form
to the whole LAN, because an explicit `--http` was exempt from the check on the
grounds that the operator had chosen it — which exempted the stock entrypoint,
and so every containerised install. Now it refuses to start and says what to do.

## Requirements and limits

- `VAULT_WORKDIR` **must** be a memory-backed filesystem. The app refuses to
  start otherwise, because decrypting onto disk would void the entire feature.
  Size the tmpfs above the archive plus headroom for PDF page rendering, and keep
  the container memory limit comfortably above the tmpfs size. See
  `docker-compose.encrypted.yml`.
- **tmpfs is swappable, so the container must not be allowed to swap.** Under
  memory pressure the kernel will page tmpfs — the decrypted archive — out to
  the host's swap device, which is persistent disk. `docker-compose.encrypted.yml`
  sets `memswap_limit` equal to `mem_limit`, which denies the container swap
  entirely; keep that pairing if you change either number. The app cannot
  detect this from inside the container. Related and also outside its sight:
  host **hibernation** writes all of RAM, tmpfs included, to disk — do not
  hibernate a host that runs unlocked instances.
- The whole archive is resident in RAM while unlocked, so RAM bounds how large an
  instance can grow. **Embeddings enlarge it twice over**: once for the vectors
  stored in `data.db`, and again for the vector index built from them at
  `bleve/chunks`. Budget roughly `chunks x dimensions x 4 bytes` for each, plus
  the passage text the index stores for quoting — a 1536-dimension model over an
  archive of 10,000 documents at ~5 passages each is about 300 MB of vectors plus
  a similar index, so half a gigabyte of tmpfs that an unembedded instance does
  not need. A model with 1024 dimensions or fewer costs proportionally less,
  which is the reason to prefer one here even where the accuracy of a larger
  model would be free. Size the tmpfs in `docker-compose.encrypted.yml`
  accordingly before turning `AI_EMBEDDING_MODEL` on, and see
  [AI providers](/ai_providers#what-embeddings-cost).
- An [embedding model you run yourself](/local_embeddings)
  costs RAM in a *second*, separate place: the sidecar's own container, ~2.2 GB
  of weights for the default `BAAI/bge-m3`. That memory is outside the `app`
  container's `mem_limit` and outside the tmpfs, so it adds to the host's
  budget rather than competing for the vault's — but it is the same RAM, and a
  host sized exactly for an encrypted instance has none spare for it. Its
  choice of model also decides the dimension count above, which is where a
  1024- or 384-dimension local model pays for itself twice.
- Nothing about the vector index costs an API call. Like the text index it is
  derived data, rebuilt inside the vault from the vectors in `data.db` — so an
  unlock, a restore, or a wiped work directory costs a rebuild, never a second
  pass over the embedding provider. The vectors themselves are ciphertext at
  rest, because they live in `data.db` like everything else.
- **Only one process may use a vault at a time.** `superuser upsert`, `migrate`
  and other CLI subcommands cannot run against a running server: each process
  would decrypt its own private copy and the last one to flush would silently
  win. Stop the server first, or use the admin UI. CLI subcommands unlock from
  `VAULT_PASSPHRASE`.
- A hard kill (`kill -9`, OOM, power loss) loses writes since the last flush —
  seconds, not the archive. A clean stop loses nothing, provided
  `stop_grace_period` is long enough for the shutdown flush: on `SIGTERM` the
  server stops the job scheduler, waits up to 20s for in-flight requests and
  worker jobs to finish, and only then seals the archive — so a request already
  answered `200` is in it. If that wait times out the shutdown says so in the
  log and flushes anyway, since hanging until Docker escalates to `SIGKILL`
  would lose strictly more.
- PocketBase's own backups and S3 storage are refused while encryption is on:
  both would write unencrypted copies outside the vault. The vault directory is
  itself a consistent encrypted backup — copy it.

## Operating it

```bash
# Status, including generation, entry count and pending writes.
curl -H "Authorization: $TOKEN" localhost:8090/api/vault/status

# Force a flush before stopping (superuser).
curl -X POST -H "Authorization: $TOKEN" localhost:8090/api/vault/flush

# Mint another recovery code; shown once.
curl -X POST -H "Authorization: $TOKEN" localhost:8090/api/vault/recovery-code
```

To verify no plaintext reaches the volume:

```bash
docker compose stop
docker run --rm -v lemmary_app_data:/v alpine sh -c \
  'grep -rl "SQLite format 3" /v; grep -rl "%PDF-" /v' | wc -l   # must print 0
```

## How it attaches

`backend/internal/boot/vault.go` is the whole attachment: it answers `vault
init` with `Result.Handled`, opens the vault via `vault.Open`, points
`Result.DataDir` at the decrypted working directory, and hands `v.Close` to
`Result.Close` for the shutdown flush.

`backend/main.go` knows none of this. It knows only that something may need to
act before the app is constructed, which is the whole of what `internal/boot`
is for — encryption at rest is the one feature that cannot be wired onto a live
app like every other.
