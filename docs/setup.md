# Configuration Guide

Runtime configuration, first-launch choices, and how Lemmary's features
behave. For the usual installation path, start with
[Self-hosting with Docker](/self_hosting). Host toolchains and source builds
live separately in [Development environment](/development).

Two areas have their own guides:

- [AI providers and models](/ai_providers) — which provider to pick, the
  `AI_*` / `OCR_*` block, and what embeddings cost
- [Paperless-ngx API compatibility](/paperless_ngx) — the compatible `/api/`
  surface, connecting third-party clients, and importing an existing library

## Environment variables

All variables live in `.env` at the project root (see `.env.example`). The
`AI_*` and `OCR_*` families are documented in
[AI providers and models](/ai_providers#the-provider-block).

### Always env-backed

| Variable | Default | Description |
| --- | --- | --- |
| `WORKER_CRON_EXPR` | `* * * * *` | Cron expression for sweeping stuck pending jobs (registered once at startup) |
| `LOG_LEVEL` | unset (no stdout slog) | Min level for JSON slog lines on stdout (`debug`, `info`, `warn`/`warning`, `error`). Ignored while PocketBase `--dev` is on (that mode already prints to the console, including SQL). PocketBase Admin → Settings → Logs still controls the logs table. |
| `METRICS_ADDR` | unset (off) | Address for the OpenTelemetry metrics endpoint, served as a Prometheus scrape target on its own port so nothing scraping it holds a credential for the archive. A bare port (`9464`) or one with no host (`:9464`) listens on every interface, which is the only form reachable in Docker through a published port &mdash; the mapping is deliberately not in `docker-compose.yml`, since an unauthenticated endpoint should not become internet-reachable by default. Name a host (`127.0.0.1:9464`) to keep it on loopback when running the binary directly. Exposes request rate and latency by route, job durations by outcome, pending queue depth, outbound AI/OCR latency by provider and model, token counts, how full the instance is against the instance-wide totals (documents, pages, stored bytes, accounts) &mdash; not the per-file `LIMIT_FILE_*` ceilings &mdash; and Go runtime metrics; no document text, account names or paths. The usage gauges read the same measurement the Settings page shows, and pair with an allowance under the same `resource` label, so utilisation is one division &mdash; `lemmary_usage / lemmary_limit`, and `lemmary_usage_bytes / lemmary_limit_bytes`. A `LIMIT_*` you have not set emits no allowance series at all, since `0` is an allowance somebody sells and cannot also mean unbounded. Costs three queries per scrape: a `COUNT` over `processing_jobs`, plus the usage aggregate (one index-only scan of `documents` and a `COUNT` over `users`). A port that will not bind is logged and the app serves anyway. |
| `MCP_ENABLED` | unset (on) | Set to `0`/`false`/`no`/`off` to remove the read-only [Model Context Protocol](/mcp) endpoint at `POST /api/mcp` on the app port. It is on by default because it answers only to the ordinary bearer token and costs nothing until an agent calls it. Search tools (`search_documents`, `read_documents`, `count_documents`) share Deep Search's index; plain-access tools (`list_documents`, `get_document`, `list_taxonomy`) read the rows directly. All scoped to the token's user. Costs one embedding request per search, and per read with a `focus`, when embeddings are configured; reads are excerpted text and never call a language model. Read at startup. |
| `IMPORT_ALLOW_PRIVATE` | unset (blocked) | Set to `1`/`true` to let ngx import reach loopback and RFC1918 hosts. Link-local / cloud-metadata addresses stay blocked. Needed when Paperless-ngx is on the same LAN or Docker network. |
| `UPLOAD_MAX_MB` | `100` | Cap on a staged split-document PDF upload, in megabytes. Read at startup, not from Settings: staging a PDF costs several times its size in memory while pages are rendered, so it protects the host as much as it shapes the product. A malformed or non-positive value falls back to the default rather than failing the boot. Per-file uploads are capped separately by the `documents.file` field (47 MB). |
| `INGEST_DIR` | unset (off) | A directory inside the container to watch as a consume folder, see [Ingest folder](#ingest-folder). Bind-mount a host folder there (`docker-compose.yml` carries the line commented out). Read at startup; the owner, scan interval and delete-after-consume switch live in Settings → Ingest. Costs one recursive directory walk per interval and one read of each new file; with deleting off, one small database row per consumed file. |
| `INGEST_IMAP_ENABLED` | unset (off) | Set to `1`/`true` to read an IMAP mailbox and turn its attachments into documents, see [Ingest from IMAP](#ingest-from-imap). Read at startup; the server, login, folder and after-import action live in Settings → Ingest. Costs one IMAP login and UID search per interval, one BODYSTRUCTURE fetch per new message and the download of each storable attachment. The mailbox password is stored in `app_settings` like provider API keys, encrypted on disk only with `VAULT_ENABLED`. |
| `IMPORT_STAGING_MAX_BYTES` | `1073741824` (1 GiB) | Cap on an archive staged for import (Amazon orders, Lemmary backup), in bytes. Staging a new archive discards that account's previous one, so this is also the disk a single account can occupy while deciding whether to confirm — the staging area's ceiling is roughly this times the number of accounts. Lower it on a small volume; raise it for a library whose backup runs past a gigabyte. A malformed value, or one under 1 MiB, falls back to the default rather than rejecting every upload. |
| `PASSKEY_RP_ID` | derived from the request host | Relying-party ID for [passkey sign-in](/passkeys): a bare domain name, no scheme and no port. Defaults to the hostname the request arrived with, which is right whenever the proxy forwards the public `Host`. Set it when it does not, or to pin a parent domain (`example.com` while serving `app.example.com`). **Every enrolled passkey is bound to this value — changing it makes all of them unusable.** Read at startup, not from Settings. |
| `PASSKEY_ORIGINS` | derived from the request scheme + host | Comma-separated full origins (scheme, host and port) allowed to complete a passkey ceremony. Defaults to the origin the request arrived on, using `X-Forwarded-Proto` for the scheme when present. Set it when the app is reachable at more than one origin, or when a TLS-terminating proxy does not set that header. |
| `LIMIT_DOCUMENTS` | unset (unlimited) | Total documents this instance may store. |
| `LIMIT_DOCUMENT_PAGES` | unset (unlimited) | Total pages across all stored documents. Anything that is not a PDF counts as one page &mdash; including a multi-page `.docx` or `.xlsx`, whose real page count is not knowable without converting the file. |
| `LIMIT_STORAGE_BYTES` | unset (unlimited) | Total bytes of stored document files. Counts the uploaded originals only, not the generated thumbnails or the extracted OCR text. When sizing a volume, budget for the database separately: extracted text is stored inline in the row and a single document may hold up to 47 Mi characters of it, so a text-heavy library's `data.db` can approach the same order as the files themselves. |
| `LIMIT_FILE_BYTES` | unset (unlimited) | Largest single document file, in bytes. Can only **lower** the effective cap: the `documents.file` field carries its own 47 MB `MaxSize` (49,283,072 bytes, just under Mistral OCR's documented 50 MB) that PocketBase validates on every save, and no value here can raise it. Files over 20 MB upload but are flagged on the upload page: OCR providers and the worker get slower and less reliable with them. Distinct from `UPLOAD_MAX_MB`, which caps the one staged PDF a split is cut from rather than each document it produces. |
| `LIMIT_FILE_PAGES` | unset (unlimited) | Most pages in a single document. Can only **lower** the effective cap: a 1000-page ceiling applies to every install regardless (see [the page ceiling](#the-page-ceiling)), and no value here can raise it. |
| `LIMIT_ADDITIONAL_USERS` | unset (unlimited) | Accounts beyond the admin account. Exactly one account is free, so `0` is a single-account instance. |
| `VITE_POCKETBASE_URL` | `http://127.0.0.1:8090` | PocketBase API URL (frontend) |
| `SETUP_ADMIN_EMAIL` | — | The first admin account, created on the first boot that finds none. Creates a `_superusers` record **and** the paired `users` account, exactly as the setup wizard does. Never resets a password that already exists. In a development build the SPA also signs itself in with this pair; a production bundle contains neither value. **Commented out in `.env.example`** — uncommenting it in a served install would hand it an admin whose password is published in this repository. |
| `SETUP_ADMIN_PASSWORD` | — | Its password, at least 8 characters. Readable from `docker inspect` and `/proc/<pid>/environ` for the life of the container, so this is for local and CI instances — a served install should use the wizard or `superuser upsert`. |

#### Instance limits

The six `LIMIT_*` variables bound how much one instance may hold. **All of them are
unlimited when unset**, so an install that sets none of them runs no extra queries
per upload and shows no quota in the UI. It is not entirely unmeasured, though —
see [the page ceiling](#the-page-ceiling) below, which applies to every install.

They are read at startup and deliberately never stored in `app_settings`: they say
what an instance is *allowed* to hold, and an admin editing the Settings page must
not be able to raise their own allowance. Change one by recreating the container
with a new value.

- An explicit `0` means zero, not unlimited. `LIMIT_ADDITIONAL_USERS=0` is a
  single-account instance.
- A value that cannot be read — a typo, a negative, a decimal — falls back to
  unlimited and is logged at `ERROR`, and the variable is named in
  `GET /api/app/limits` for an admin session and on the **Maintenance** page. The
  fallback direction is deliberate: a stray character in an orchestrator's
  environment should grant room rather than lock an owner out of their own archive,
  and being told loudly is what keeps that from going unnoticed.
- Lowering a limit under a library that already exceeds it never deletes anything.
  Usage is simply reported as over, and the next addition is refused.
- The three instance-wide totals are measured from the live rows on each write, so
  deleting a document (or a user, which cascades to their documents) frees its
  allowance immediately.
- Documents created **before** this version was installed count as zero pages and
  zero bytes: the page and size columns are added without a backfill, because
  filling them would mean running `pdfinfo` once per existing PDF during a
  migration. `LIMIT_DOCUMENTS` and `LIMIT_ADDITIONAL_USERS` are exact regardless;
  the page and byte totals read low on an upgraded library until those documents
  are replaced.
- A bulk path — a backup restore, an Amazon-orders import, a document split — is
  checked against the remaining allowance up front, so the common case of a batch
  that plainly does not fit is refused before anything is created. That check is
  **not** a reservation, and a bulk run can still stop partway:
  - a restore or an Amazon import knows its document count and bytes, but not its
    page count (an archive's real page counts are only discoverable by opening
    every PDF in it), so a page limit is enforced per document as the run
    proceeds;
  - a split knows its document and page counts exactly, but not the size of parts
    that do not exist yet, so a storage limit is enforced per part;
  - a Paperless-ngx import checks only the document count the remote reports;
  - and any of them can be confirmed minutes after its preview, by which time
    another upload may have taken the room.

  A run that stops partway keeps what it already created and reports the rest as
  errors; nothing is rolled back. The per-document checks are what make the limit
  itself exact — the up-front check is there to turn the common failure into one
  clear message instead of several hundred.

#### The page ceiling

One bound is not a plan and not configurable: **a document may hold at most 1000
pages**, on every install. An upload over that is refused with
`limit_ocr_pages`, before any OCR provider is called.

It exists because of where the text goes. The OCR providers return a document's
whole text as one string, and that string has to fit the `ocr_text` column,
which holds 47 Mi characters — the same 47 MB the `documents.file` field accepts,
counted in characters instead of bytes. Nothing else bounds an OCR result:
Mistral is the only provider that documents a page limit (1000 pages, which is
where this number comes from), and Google Vision reads however many pages the
file has, five at a time. The page count, taken before the first provider call,
is the one measurement that says whether the answer could be stored — and
refusing there means an over-long document costs nothing rather than being paid
for and then discarded.

Consequences worth knowing:

- `LIMIT_FILE_PAGES` can lower this and cannot raise it, the same way
  `LIMIT_FILE_BYTES` relates to the 47 MB `documents.file` cap. When both would
  refuse a file, the message names the plan limit, since that is the one the
  account can do something about.
- Every install now counts the pages of each PDF upload with `pdfinfo`, where
  before only an install with a limit set did. Other file types cost a five-byte
  header read. A PDF whose page count cannot be read counts as one page, so on a
  host without poppler this ceiling is not enforced at upload — the OCR step
  refuses an over-long result there instead, which fails the document rather than
  the upload.
- A restore or a Paperless-ngx import that brings a document's text with it skips
  the ceiling: no OCR will run, so there is nothing to spend, and a long document
  archived before this existed stays restorable.
- DOCX and XLSX are not bounded by page count — a spreadsheet has none — so they
  are measured as they are parsed instead, and an extraction that runs past the
  column is abandoned with an error rather than stored short. An XLSX is the case
  this matters for: cells reference a shared string table, so the text one
  extracts to is not bounded by the bytes it arrived in.

## First-launch setup wizard

On a fresh install the SPA hard-gates until setup is complete:

1. **Create admin** — email + password. Creates a PocketBase `_superusers` account **and** a matching `users` account (same credentials) so the admin can own documents. Replaces PocketBase’s browser installer UI.
2. **Passkey** *(optional)* — offer to add a [passkey](/passkeys) for the account just created. Skipping it changes nothing and the offer does not come back; a passkey can be added later from **More → Account**. The step is hidden on an address where a passkey cannot be created (an IP address, or plain HTTP outside `localhost`).
3. **Providers** — the guided form of [Guided AI provider setup](/guided_ai_setup): a Mistral key (OCR and embeddings) and one other language-model provider, both created in a single submit. Either half may be left blank; **Add a provider manually instead** falls back to the one-at-a-time form, which is the way to a ChatGPT sign-in, a `google_vision` key, or `docling`/`local`, the keyless sidecars.
4. **Models** — pick provider → model for OCR and General AI (everything a language model does), and optionally for embeddings. All three arrive prefilled when the providers came from the guided form; embeddings can be set to **None**, and setup is complete without them.

Steps 3 and 4 are skipped when `.env` already carries the keys — see
[AI providers and models](/ai_providers). The admin can likewise come from the
environment (`SETUP_ADMIN_EMAIL` / `SETUP_ADMIN_PASSWORD`, applied on the first
boot that finds no account) or from the CLI (`go run . superuser upsert EMAIL
PASS` from `backend/`, which also upserts the paired `users` account). With
both, a fresh volume comes up with nothing left to answer. Until keys are
present, regular users see a “setup incomplete” screen; only an admin can finish
configuration.

## Settings (admin UI)

1. Sign in with the **admin** email/password (login prefers the `users` account; legacy `_superusers`-only installs are linked automatically via `/api/app/ensure-user`, which sets a hidden `is_app_admin` flag on the paired `users` record).
2. Open **Settings** in the nav (shown when `/api/app/me` reports `is_admin`). It has one tab per section, each on its own path and each saving only its own fields: **Appearance** (`/settings`), **AI** (`/settings/ai`), **Processing** (`/settings/processing`), **Worker** (`/settings/worker`), **Duplicates** (`/settings/duplicates`) and, when `INGEST_DIR` or `INGEST_IMAP_ENABLED` is set, **Ingest** (`/settings/ingest`). On the AI tab, add providers, then bind OCR, General AI and optionally the Advanced model to a provider and model — see [Binding models in Settings](/ai_providers#binding-models-in-settings). Changes hot-reload the in-process clients (no restart).

`WORKER_CRON_EXPR` is not editable there; change `.env` and restart, or use PocketBase Admin → Settings → Crons.

**Extra extraction rules** (Processing tab) is the one part of the extraction prompt an admin writes. Whatever is in it is appended to the built-in prompt, after the list of existing correspondents and document types and before the format rules, so it can state house conventions the fixed prompt cannot know — “treat *Rechnung* as the document type Invoice”, “tag insurance documents with the policy number”. It cannot change which fields are stored: the pipeline parses the answer into a fixed set, and the prompt says so after the rules. Up to 4000 characters, empty by default, and applied to documents processed or reprocessed from then on. It is tenant-owned, so a managed instance keeps it. The extraction log line reports its length as `rule_chars`, and each document's `extract_metadata` step run records the prompt it actually ran under (see below).

`EXTRACTION_PROMPT_VERSION` is not offered there either. It is pure bookkeeping — it is recorded on each document's `extract_metadata` step run so metadata can be traced back to a prompt, and never reaches the prompt itself — so there is nothing for an admin to tune. Where extraction rules are set, that step run records `v1+rules.<digest>` instead of the bare version: the rules change the prompt while the version does not, and a run recorded under `v1` alone would name a prompt that no longer exists. Documents extracted with no rules keep the bare version, so nothing changes for an instance that sets none. `PATCH /api/app/settings` still accepts `extraction_prompt_version`, and it can be edited in PocketBase Admin → `app_settings`.

### Ingest folder

With `INGEST_DIR` set, a cron walks that directory and turns every storable file (the same types the upload accepts: PDF, JPEG, PNG, WebP, TXT, CSV, DOCX, XLSX) into a document, exactly as if it had been uploaded: the file is hashed, measured against the instance limits, and queued for the full pipeline. The walk is recursive (a root that is itself a symlink is followed), and the folders between the root and the file become its tags — `Taxes/2024/invoice.pdf` arrives tagged **Taxes** and **2024**, reusing an existing tag whose name matches case-insensitively and creating the ones that do not exist yet. Extraction adds the model's tags to these rather than replacing them. Dot-files and dot-folders, symlinks, empty files and anything not storable are ignored; a file modified in the last 30 seconds waits for the next scan so nothing half-written is picked up.

The **Ingest** tab (`/settings/ingest`) holds the three settings, all tenant-owned (owner and interval are shared with [IMAP ingest](#ingest-from-imap)):

- **Owner** — the account every document from the folder belongs to. Default is the first admin's paired `users` account; any account can be picked.
- **Scan every** — default 5 minutes. Saving re-schedules the `dir_ingest` cron (visible in PocketBase Admin → Settings → Crons), so a change applies without a restart. Accepted are the minutes that divide an hour (1–30) and the hours that divide a day (1–24); anything else is refused, since a cron step would space it unevenly.
- **Delete the original file after it is consumed** — off by default. Off, files stay in place and each is imported once: the account's `ingest_files` ledger remembers every consumed path with its size and modification time, so neither a restart nor deleting the document brings a file back; changing the file imports it again. A file whose content is already in the library is skipped by the checksum duplicate check. Files above the 47 MB document cap and symlinks are never read. On, a file is removed once its document exists, and a file that turns out to be a duplicate is removed as well; with encryption at rest the removal waits until the vault has sealed the document, so a hard kill cannot lose both copies. A file the pipeline refuses (wrong content for its extension, over a per-file limit) is never deleted; it is logged once and skipped. Reaching an instance-wide limit stops the scan until the next interval.

### Ingest from IMAP

With `INGEST_IMAP_ENABLED` set, the **Ingest** tab also offers a mailbox, and a cron (`imap_ingest`, on the same interval as the folder) reads its folder: every attachment whose name has a storable extension of a chosen type becomes a document owned by the same account, through the same hooks as an upload. The message body is never a document, nor is an image anywhere under a `multipart/related` part — the logos and icons an HTML body embeds by `cid:` — unless the sender marked it `Content-Disposition: attachment`; an image sent as a real attachment still counts. An inline image outside `multipart/related` is imported, since that is how iPhone Mail sends attached photos. Mail without such an attachment is left untouched. Leaving the server empty turns it off.

Only mail received after the mailbox was set up is scanned: saving a new server, username or folder records that moment (`imap_since`, shown under the Mailbox fields), and anything the folder already held is left alone, whatever the after-import action. A new password or after-import action keeps the moment, so nothing that arrived meanwhile is skipped. Older mail is a backfill: **Maintenance → Mailbox** takes two days, inclusive, and imports the attachments of everything received between them (IMAP `SINCE`/`BEFORE`, by the server's received date). The backfill opens the folder read-only, never moves or deletes a message, does not touch the keep-mode ledger, and relies on the checksum check to skip attachments already in the library, so running it twice is harmless. It runs in the background and holds the same lock as the scheduled scan, so neither starts while the other runs — a delete or move scan never works on the folder a backfill is reading. A message that fails is counted and passed over; only an instance limit stops it early.

Messages are fetched 200 at a time. Attachments inside a forwarded message (`message/rfc822`) count too. Messages already flagged `\Deleted` are ignored. A message whose import keeps failing for another reason than a refusal (a hook error, a full disk) is retried on the next two scans and then given up on with an error in the log, so it cannot hold back the mail behind it; the backfill can import it later. The since cut allows 10 minutes for a server clock running behind this host's.

Delete and move rely on UIDPLUS (or IMAP4rev2) to expunge only the messages Lemmary consumed. On a server without it the only expunge purges every `\Deleted` message in the folder — including ones a mail client flagged and has not expunged yet — so Lemmary never runs it there: delete leaves consumed messages flagged `\Deleted`, and move (without MOVE either) copies them to the target and flags the original, for the next expunge the user's own client runs.

- **IMAP server** — `host` or `host:port`; the port defaults to 993 for **TLS** and 143 for **STARTTLS**. The certificate must verify; plaintext IMAP is not offered.
- **Username**, **Password** — the password is write-only: the API reports only whether one is stored, and saving with the field blank keeps it.
- **Folder** — default `INBOX`.
- **Import** — which file types become documents: **PDF**, **Office** (DOCX, XLSX), **Images** (JPG, PNG, WEBP) and **Text** (TXT, CSV); all by default. Stored as the types to skip (`imap_skip_types`); at least one must stay on. A message with an attachment of an unchecked type is never moved or deleted, like one with a refused attachment, so Delete cannot take a file that was never imported; embedded logos do not hold a message. Turning a type back on does not revisit mail already scanned; a backfill picks it up. The backfill applies the same filter.
- **After import** — **Keep** (default) opens the folder read-only and remembers the highest UID consumed in the `ingest_files` ledger, so neither a restart nor deleting the document brings a message back; a server that resets UIDVALIDITY starts over, and the checksum check skips what is already in the library. **Move** moves each consumed message to another folder (created if missing; it must differ from the source). **Delete** flags it `\Deleted` and expunges it. With encryption at rest, moving or deleting waits until the vault has sealed the documents.

A message whose attachment is refused (empty, over the 47 MB cap, wrong content for its extension) is never moved or deleted; it is logged and skipped. Reaching an instance-wide limit, or any other error, stops the scan and the message is retried next interval.

## Management (admin UI)

**Management** in the nav (admin only, below Settings) manages the accounts on the instance. Its **Users** tab lists every `users` account, adds one (email, optional name, password; created verified, so it can sign in at once), edits a regular account's email, name or password, and deletes one. Deleting an account also deletes its documents, tags, shares and passkeys. Admin accounts (`is_app_admin`) are listed but read-only here; manage them in the PocketBase dashboard. The routes are `GET`/`POST /api/app/admin/users` and `PATCH`/`DELETE /api/app/admin/users/{id}`, admin only, and `403` on an admin account. A new account counts against `LIMIT_ADDITIONAL_USERS`, and with the vault on its password gets its own key wrap, as with any other account.

## Maintenance (admin UI)

**Maintenance** in the nav (admin only, next to Settings) holds maintenance actions that run over the whole library, not per document:

- **Scan for duplicates** — `POST /api/app/duplicates/scan`, see [Duplicate detection](#duplicate-detection).
- **Scan mailbox** (only with `INGEST_IMAP_ENABLED`) — `POST /api/app/ingest/imap/scan` with `{ "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" }` starts a backfill of older mail in the background, see [Ingest from IMAP](#ingest-from-imap), and answers `202` with the status; `GET` on the same path returns `{ "running", "from", "to", "created", "skipped", "failed", "error" }` for the last one, which the page polls every 3s. `409` while any mailbox scan runs, `400` for bad dates or no mailbox.
- **Clear stale data** — `POST /api/app/taxonomy/prune` deletes every correspondent and document type that no document references any more (left behind by deleted documents, renames, or an aborted import). Documents are never modified, and neither are tags: those are created by hand, so an unreferenced tag is one its owner has not applied yet rather than debris. The response still carries a `tags` count, always `0`. Reference collection and deletion share one transaction, so a document saved concurrently either counts as a reference or fails its own relation check; it cannot keep a dangling id.
  The button is disabled while any processing job is `pending` or `running`, so an entity a job is about to attach cannot be swept up. The count comes from the PocketBase collection API (`GET /api/collections/processing_jobs/records`), polled every 5s and re-checked on click. That list rule is `document.user = @request.auth.id`, so the gate only sees jobs on the admin's own documents — another user's in-flight upload does not block the button.
- **Rebuild search index** — `POST /api/app/search/reindex`, see [Full-text search](#full-text-search).
- **Embed N missing documents** — `POST /api/app/embeddings/backfill` sweeps every document that still needs passage vectors: the archive that existed before an embedding model was bound, restored backups (whose documents get no processing job at all), documents edited since they were embedded, and anything a model or chunker change invalidated. It answers immediately with `{ "started", "running", "stats" }` and works through the backlog in the background, in batches, for up to 30 minutes; `GET` on the same path returns `{ "running", "stats" }`, which is what the progress line polls every 3s. The section is disabled with a pointer to Settings when no embedding model is bound (`409`), and the button is disabled while a sweep is running. A sweep and the `EMBEDDING_BACKFILL_BATCH` cron share one lock, so they never embed the same document twice — and `EMBEDDING_BACKFILL_BATCH=0` disables only the cron, never this button.

Admin-only items in the nav menu are prefixed with a shield icon (decorative — the items only render for admins in the first place).

## Outbound mail (SMTP / `outbound_emails`)

Configure SMTP under PocketBase Admin → Settings → Mail. When SMTP is **disabled** (the default), PocketBase would normally fall back to local `sendmail`. This app replaces that fallback: messages are written to the `outbound_emails` collection instead (password reset, verification, OTP, auth alerts, etc.).

Browse them in PocketBase Admin as a superuser. Enable SMTP when you want real delivery; the DB sink is skipped while SMTP is on.

## Upload page

**Upload** (`/upload`) groups the ways documents enter the library into sub-sections, each on its own route so a section can be linked, bookmarked, and reached with the browser back button:

| Section | Route | State |
| --- | --- | --- |
| Files | `/upload` (default) | Implemented — drag-and-drop / file-picker upload of files or whole folders, see the processing flow below |
| Scan | `/upload/scan` | Implemented — scans from an eSCL (AirScan) scanner on the local network, see [Network scanning](/scanning) |
| Amazon orders | `/upload/amazon` | Implemented — imports the invoice PDFs out of an order archive requested from Amazon, see [Amazon order import](#amazon-order-import) |
| Zip archive | `/upload/zip` | Implemented — imports the documents out of a zip the user packed themselves, see [Zip archive import](#zip-archive-import) |
| Split documents | `/upload/split` | Implemented — splits a PDF holding several joined documents into one document per part, see [Document splitting](#document-splitting) |

Plain file upload stays on `/upload` itself (an index route), so existing links and the **Upload** nav entry keep landing on it.

A folder can be dropped on the Files tab or picked with **Choose a folder instead**, and is walked to the bottom in the browser: each file inside it is posted as its own document, exactly as if it had been picked by hand. A file that came out of a folder is named `<parent folder>-<file>`, the same rule the zip imports use, because a scanner that writes `1.pdf` into a folder per batch would otherwise fill the library with documents called `1.pdf`. Finder and archiver leftovers are dropped silently rather than reported as the wrong type — `__MACOSX/`, AppleDouble `._` shadows (which carry the extension of the file they belong to) and any other dot-file, the same rule the zip import applies. A `.zip` dropped here is not uploaded; it points at the Zip archive tab, which can show what the archive holds first.

### Amazon order import

Request the archive from Amazon under Account → Request your data → Your Orders; Amazon emails a download link once the export is ready. The zip holds CSV reports, delivery photos and — under `Additional Data/Retail.TransactionalInvoicing.*` — the invoice PDFs. Only the PDFs are imported; every other entry is counted as ignored and left alone.

Uploading and importing are two steps, so nothing is created before the user has seen what the archive holds:

1. `POST /api/app/import/amazon/upload` (multipart, field `file`) streams the zip to `<data dir>/temp/zip_import/` — it is never buffered in memory, since real exports run to hundreds of MB. The archive is scanned and every PDF is hashed, then returned as a preview: total file count, how many are importable, how many are duplicates or oversized, the ignored-entry count, and the per-file list. Duplicates are PDFs whose checksum already exists among the owner's documents (`duplicate_of` names the existing id) or that repeat earlier in the same archive. Imported documents are named `<parent folder>-<file>`, because Amazon numbers the invoices per folder (`1.pdf`, `2.pdf`, …).
2. `POST /api/app/import/amazon` with `{ "upload_id": "..." }` starts the import and returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/import/amazon/status?job_id=...` for `progress` (`{ done, total }`) until `status` is `completed` (with `result`) or `failed` (with `error`). The `result` counts `imported`, `skipped_duplicates`, `skipped_oversized` and `failed`, plus up to 25 per-file error messages. Each imported document is saved as `pending`, so it goes through the normal OCR + AI [processing flow](#processing-flow).

`DELETE /api/app/import/amazon/upload?upload_id=...` discards a staged archive the user chose not to import. Staged archives expire after 30 minutes and are swept on the next upload, including files left behind by an earlier process — the staging registry and the job state are in memory, so both are lost on restart. Uploading a second archive also discards the account's previous one, so an account holds at most one at a time. Confirming consumes the upload id: the same archive cannot be imported twice, and one import may run at a time per user (a second start returns `409`).

Rejections come back as `400` at preview time rather than mid-import: not a readable zip, no PDFs, more than 5000 PDFs, an upload over `IMPORT_STAGING_MAX_BYTES` (1 GiB by default), or an archive that decompresses beyond 8 GiB (a zip bomb). A single PDF over the 47 MB `documents.file` limit is not fatal — it is flagged `oversized` in the preview and skipped on import. A PDF over [the page ceiling](#the-page-ceiling) is refused per entry as the run proceeds rather than at preview time, since an archive's real page counts are only discoverable by opening every PDF in it.

### Zip archive import

The same two steps as the Amazon import above, against `/api/app/import/zip/upload`, `/api/app/import/zip` and `/api/app/import/zip/status`, with identical payloads, limits and rejections. It is the same implementation: the only difference between the two flows is which entries in the archive count as documents, which the upload route says and the staged upload then remembers.

Here that means every type `documents.file` can store — PDF, JPEG, PNG, WebP, plain text, CSV, `.docx` and `.xlsx` — rather than PDFs alone. Anything else in the archive is counted as ignored and left alone, as are directory entries, empty entries and archiver bookkeeping (`__MACOSX/`, AppleDouble `._` files). Imported documents are named `<parent folder>-<file>`, so a zip of a scanner's output stays legible.

The extension list is a pre-filter, not the last word: PocketBase decides what the `file` field accepts by sniffing the content on save, so an entry whose extension lies about its contents is refused there and reported per file in the run's errors rather than at preview time.

A staged archive and a running import are one per account across both zip flows, so uploading an Amazon export discards a zip staged a moment earlier and a second import while one runs returns `409`. Restoring a Lemmary backup is a different thing entirely and lives on [`/import/archive`](#restoring); it keeps its own staging and can run alongside.

### Network scanning

**Scan** (`/upload/scan`) scans from an eSCL ("AirScan") device on the local network and adds the pages as one document. Setting it up, the two ways scanners are found, and why mDNS needs `network_mode: host` under Docker are covered in [Network scanning](/scanning); the API is:

1. `GET /api/app/scan/discover?cidr=...` returns `{ scanners, cidr }`. Discovery is an mDNS browse of `_uscan._tcp`/`_uscans._tcp` and a sweep of `cidr` probing `GET http://<ip>/eSCL/ScannerCapabilities`, run concurrently and merged by address; the two run under one 5-second budget. An omitted `cidr` is derived as the /24 of the request's client address, which is the only hint a containerised app has about the LAN, and comes back in the response so the UI can say what was searched. A range that is not private, or larger than a /22, is refused with `400`.
2. `POST /api/app/scan` with `{ "scanner", "source", "upload_id" }` returns `202 Accepted` and `{ "job_id" }`. `source` is `platen` (one page) or `feeder` (every sheet in one job). An empty `upload_id` starts a new document; otherwise the pages are appended to that one. Poll `GET /api/app/scan/status?job_id=...` until `completed`, whose `result` is the staged document: `upload_id`, `page_count`, `size_bytes`, `expires_at`. One scan runs at a time per user (a second start returns `409`), which is also what keeps two runs from merging onto the same file.
3. `GET /api/app/scan/pdf?upload_id=...` streams the document so far, for the preview. `DELETE /api/app/scan?upload_id=...` discards it.
4. `POST /api/app/scan/document` with `{ "upload_id" }` saves it as a `pending` document and returns `{ "document_id" }`, so it goes through the normal OCR + AI [processing flow](#processing-flow). A re-scan of something already in the library comes back as `400` with `duplicate_of`.

The scan itself is three requests to the device: `POST {base}/ScanJobs` with a PWG ScanSettings document (A4, 300 dpi, RGB24, `application/pdf`), then `GET {job}/NextDocument` until it answers `404`, then `DELETE {job}` — the delete always runs, including after a failure, because a device left holding an open job refuses the next one. Pages are merged with `pdfunite` into `<data dir>/temp/scan/<upload id>.pdf`, which expires after 30 minutes like any other staged upload.

Because the address comes from whoever is signed in, every request goes through a dial-time guard that allows only RFC1918 and IPv6 ULA addresses: public addresses, `localhost` and link-local `169.254.x` (the cloud metadata service) are refused, redirects are not followed, and a `Location` header pointing at another host is rejected. The staged document is capped at the `documents.file` field's own 47 MB, checked before each scan so a full document is reported while there is still something to do about it.

### Document splitting

**Split documents** (`/upload/split`) takes a PDF that holds several separate documents scanned into one file and creates one document per part. The staged original is discarded — only the parts become documents.

Uploading and splitting are two steps, so nothing is created before the user has decided where the cuts go:

1. `POST /api/app/split/upload` (multipart, field `file`) streams the PDF to `<data dir>/temp/split_upload/<upload id>/source.pdf` and renders one thumbnail per page next to it at 900 px on the longest edge — larger than the 400 px document-card preview, because deciding where a document ends means reading the letterhead of a page (a single `pdftoppm` run; `pdftoppm` zero-pads its own output, so the files are renamed to `page-<n>.png`). The response carries `upload_id`, `file_name`, `page_count`, `size_bytes` and `expires_at`.
2. `GET /api/app/split/page?upload_id=...&page=n` serves one cached thumbnail as `image/png`. The endpoint needs the session token, which an `<img src>` cannot carry, so the SPA fetches each page and wraps it in a blob URL.
3. `POST /api/app/split` with `{ "upload_id": "...", "parts": [{ "from": 1, "to": 2 }, …] }` starts the split and returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/split/status?job_id=...` for `progress` (`{ done, total }`) until `status` is `completed` (with `result`) or `failed` (with `error`). The `result` counts `created`, `skipped_duplicates`, `skipped_oversized` and `failed`, plus up to 25 per-part error messages and the `document_ids` created. Each part is saved as `pending`, so it goes through the normal OCR + AI [processing flow](#processing-flow).

`parts` must cover every page exactly once, in order — that is all the cut-marking UI can express, so a gap, an overlap, an unsorted list or a range outside the file comes back as `400` with a message naming the page it went wrong at. A rejected request leaves the upload staged, so a corrected request can follow.

Parts are named after the pages they hold (`scan-page-1.pdf`, `scan-pages-2-5.pdf`) from a sanitized form of the uploaded file name. `pdfseparate` and `pdfunite` copy the original page objects rather than re-rasterizing, so the text layer and image quality survive; both stamp a random trailer `/ID` into what they write, which is rewritten to a fixed value so extracting the same pages twice produces the same bytes. Without that, the exact-duplicate check could never recognize a re-split part and splitting the same scan twice would silently create a second copy of everything. The rewrite only happens when the `/ID` sits past the offset `startxref` names (so no cross-reference offset can shift) and is reverted if the result does not open.

`DELETE /api/app/split/upload?upload_id=...` discards a staged PDF the user chose not to split. Staged uploads expire after 30 minutes and are swept on the next upload, including directories left behind by an earlier process — the staging registry and the job state are in memory, so both are lost on restart. Confirming consumes the upload id: the same PDF cannot be split twice, and one split may run at a time per user (a second start returns `409`).

Rejections come back as `400` at upload time: not a readable PDF (the `%PDF-` header and `pdfinfo` decide, not the declared content type), a one-page PDF (nothing to split), more than 100 pages, or an upload over 100 MiB. A part over the 47 MB `documents.file` limit is not fatal — it is counted as `skipped_oversized`.

#### Automatic detection

`POST /api/app/split/detect` with `{ "upload_id": "..." }` proposes the cuts and returns `202 Accepted` with a `job_id`; poll `GET /api/app/split/detect/status?job_id=...` the same way. The `result` is `{ "parts": [{ "from", "to", "title" }], "text_source" }`. Detection does not consume the upload: it can be repeated, and the user still confirms the split.

Page text comes from `pdftotext` per page first (`text_source: "pdf"`). A page counts as having a text layer at 16 characters or more; when fewer than half the pages clear that bar the file is treated as a scan and every page is read by the configured OCR provider instead (`text_source: "ocr"`) — counted per page rather than averaged, since one born-digital cover sheet in front of thirty scanned pages would otherwise lift an average over any threshold. The OCR fallback extracts each page to its own PDF and is capped at 40 pages; beyond that the job fails with a message telling the user to mark the cuts by hand. Detection needs an extraction model (`400` otherwise) and, for a scan, an OCR provider.

The page texts go to the extraction provider and model in one request asking for `{"parts":[{"from","to","title"}]}`, with a per-page character budget of `max(200, 30000 / pages)` so even a 100-page file arrives whole rather than truncated to its first pages. The answer is then normalized server-side into a contiguous cover of every page: only the cut positions it implies are kept and the parts are rebuilt from them, so an unsorted, gapped, overlapping or out-of-range proposal still yields something `POST /api/app/split` accepts, and an unusable one degrades to a single whole-file part.

## Backup and restore

Any signed-in user can download their whole library as one zip and restore it — into this instance or another one. Export is per user: it contains the caller's documents and taxonomy, never anyone else's, and never the instance's settings or API keys. Saved AI chats are not included: an export carries the archive, not the conversations about it.

### Exporting

**More → Export → Download backup**, or `GET /api/app/documents/export`. The response streams a zip named `lemmary-export.zip`; there are no options.

Every entry lives flat under `lemmary-export/`, so the archive stays browsable by hand:

```text
lemmary-export/manifest.json
lemmary-export/[<id>] <title><ext>              the original upload
lemmary-export/[<id>] <title>.ocr.txt           extracted text (omitted when empty)
lemmary-export/[<id>] <title>.metadata.json     titles, tags, dates, checksum, timestamps
lemmary-export/[<id>] <title>.preview.png       generated thumbnail (omitted when there is none)
```

`<title>` is sanitized and truncated so the longest name stays under the 255-byte limit filesystems put on one path element. Relations are written as **names**, not ids, because ids mean nothing in the instance the archive is restored into.

`manifest.json` is the table of contents: `format`, `version`, `exported_at`, `document_count`, the full `taxonomy`, and the exact entry paths of each document. Two things depend on it:

- **Tags, correspondents and document types no document references.** They exist nowhere else in the archive, so without the manifest a restore would drop them.
- **Disambiguating sidecars.** A document whose own file is a `.txt` named like an OCR sidecar cannot be told apart from one by name alone.

A document whose stored file is missing from storage is skipped and left out of the manifest, so the manifest never claims something the archive does not hold.

### Restoring

**More → Import** (`/import`), or the API below. The archive is streamed to `<data dir>/temp/archive_import/` — never buffered in memory — then scanned and previewed: how many documents it holds, how many are new, how many are duplicates, oversized or missing, how much taxonomy comes with them. Nothing is created until you confirm.

Two modes:

- **Restore the archive as it was** (`restore`, the default): recreates titles, tags, correspondents, document types, dates, OCR text and thumbnails, and restores the taxonomy first so records nothing references still land. Restored documents get **no processing job at all** — everything a pipeline would derive is already in the archive — so a restore makes **no OCR or LLM calls** and cannot overwrite what it just restored. A document the archive holds no `.metadata.json` for has nothing to restore, so it takes the ordinary upload path instead; only pre-manifest `originals` archives contain those.
- **Import the files only and reprocess** (`reprocess`): ignores every sidecar and queues the full OCR + AI pipeline, as for a new upload.

Because a restore does not run the pipeline, near-duplicate detection does not re-run over the restored documents. Exact duplicates are still rejected on create by checksum, and `duplicate_of` / `text_fingerprint` come back from the archive; **Maintenance → Scan for duplicates** re-derives near-duplicate links across the whole library when you want them recomputed. A restored document also keeps whatever thumbnail the archive carried — an older archive without `.preview.png` sidecars leaves those documents without one until they are reprocessed.

What a restore does *not* preserve: **document ids**. Restored documents get fresh ids, and `duplicate_of` is remapped to the restored copy when the original is in the same archive (dropped when it is not). `created` and `updated` are written back after the save, so the library comes back in its original order. Documents whose file checksum is already in your library are skipped, which makes restoring the same archive twice safe.

Archives exported before manifests existed still restore: their documents are reconstructed from the entry names alone. Only orphan taxonomy — and that one sidecar-lookalike case — cannot be recovered from them.

### API

1. `POST /api/app/import/archive/upload` (multipart, field `file`) stages the zip and returns the preview, including `upload_id` and `has_manifest`.
2. `DELETE /api/app/import/archive/upload?upload_id=...` discards a staged archive. Staged archives also expire on their own after 30 minutes.
3. `POST /api/app/import/archive` with `{ "upload_id": "...", "mode": "restore" | "reprocess" }` returns `202 Accepted` with `{ "job_id", "status": "running" }`.
4. `GET /api/app/import/archive/status?job_id=...` until `status` is `completed` (with `result`) or `failed` (with `error`).

Job state is in memory for the running process only, and one import may run at a time per user. Staging a new archive discards the account's previous one, so an account holds at most one at a time. Uploads are capped by `IMPORT_STAGING_MAX_BYTES` (1 GiB by default) and 5000 documents, each entry at the 47 MB document limit; one budget covers everything the inspection and the restore inflate, so an archive that unpacks far beyond its size is rejected as a zip bomb. A restored document that carries its own OCR sidecar is exempt from [the page ceiling](#the-page-ceiling) — it needs no OCR, so a long document archived before that ceiling existed still restores; an entry without a sidecar takes the ordinary upload path and is subject to it.

An archive with no documents but a non-empty taxonomy is valid and restorable — that is what a backup of a library with tags but no documents looks like.

## Processing flow

1. User uploads a document from `/upload`
2. PocketBase stores the file and creates a `processing_jobs` record via Go hook
3. An `OnRecordAfterCreateSuccess` hook dispatches the job immediately; a cron job (`process_pending_jobs`) sweeps any stuck pending jobs
4. Worker generates a PNG preview from the first PDF page (via `pdftoppm`), then extracts text, optionally checks for near-duplicates, and runs AI metadata extraction
5. Extracted metadata is saved on the document
6. UI shows status on list and detail pages, and anything awaiting a human appears in the [review Inbox](#review-inbox)

Metadata extraction sends the current document's OCR text **and** up to 500 of that owner's existing correspondent names and document-type names to the configured LLM provider, so the model can reuse existing labels instead of creating near-duplicates. Names are sent as a JSON array marked as untrusted data. Apply still matches exact names, then a punctuation/accent-insensitive form (`Amazon EU S.à r.l.` vs `Amazon EU S.a.r.l.`). Existing `name` / `name_original` values are not overwritten on reuse.

### Review inbox

**Inbox** in the header is the list of documents whose status is `needs_review` — everything waiting on you rather than on the worker. It carries a count, so a glance at the header says whether there is anything to do.

A document lands there for one of three reasons:

- **Low extraction confidence** — the model scored its own answer below 0.5.
- **A possible duplicate** — see [duplicate detection](#duplicate-detection) below; the card and the detail page link to the document it may duplicate.
- **Because you asked for all of them** — Settings → **Always require review for new documents** (on for a new instance; an upgraded one keeps its setting). With it on, every document the AI extracted metadata for waits in the Inbox however confident the extraction was, reprocessed documents included: nothing reaches `completed` except by your saying so. This is the setting for the workflow of uploading as things arrive and correcting a month's worth in one sitting.

  It does not apply to paperless-ngx imports in preserve mode. Those run no AI extraction — the metadata is the one curated in paperless — so there is nothing for a review to check, and a migrated archive is not emptied into the Inbox.

  Turning it on also rearranges the two lists around it, because otherwise **Documents** would be mostly a second copy of the Inbox:

  - **Documents** defaults to **Completed** — the archive you have actually read. The status dropdown still offers *All statuses*, and picking it puts `?status=all` in the URL.
  - An upload of several files, an Amazon import and a split all finish in the **Inbox** rather than on **Documents**, which would filter out exactly what was just added. A single-file upload still opens that document.

  The instance announces the setting on `GET /api/app/meta` so the SPA can do both for every user, not only for admins; only an admin can change it.

Two ways out, both of which set the status to `completed`:

- **Save corrections** on the detail page — fixing a misread `document_date` and saving counts as reviewing it.
- **Mark reviewed**, when the metadata is already right — on the detail page, on a card in the list, or on a selection of cards at once from the Inbox. It writes nothing but the status, so `metadata_source` still records that the model wrote the metadata.

With **Always require review** on, extraction also asks the model for up to three **suggested tags**: new names not yet in your vocabulary. They appear as dashed `+ name` chips on the card and the detail page while the document waits, and accepting one creates the tag and adds it to the document. Suggestions are stored on the processing job, never on the document, and vanish once it is reviewed. Tags are otherwise never created by the AI.

A document marked reviewed while `duplicate_of` is set keeps that link: the relationship is still true, and marking it reviewed says you looked and kept both.

### Duplicate detection

- **Exact duplicates** — on create, the uploaded file is hashed (SHA-256) into `documents.checksum`. A second upload with the same checksum for the same user is **rejected**, with an error pointing at the existing document id. Uniqueness is enforced with a per-user unique index on non-empty checksums so concurrent uploads cannot both succeed.
- **Near-duplicates (optional)** — after OCR, a `detect_duplicates` step can compare normalized OCR text (SimHash + Jaccard). This is controlled by Settings → **Enable near-duplicate detection after OCR** (off by default). Matches are marked `needs_review` with `duplicate_of` set to the earlier document (never a newer one), so they show up in the [Inbox](#review-inbox); AI extract/apply steps are skipped.
- **Bulk scan** — Maintenance → **Scan for duplicates** (admin) backfills missing checksums/fingerprints and marks exact (and, if enabled, near) duplicates among existing documents.

Text extraction:

- **PDF and images** — configured OCR provider (Google Vision, Mistral Document OCR, an OpenAI/OpenRouter model that accepts files/images, or the [local Docling sidecar](/local_ocr))
- **TXT, CSV, DOCX, XLSX** — native parsers (no OCR API call); preview is skipped for these formats. A DOCX or XLSX whose text runs past what `ocr_text` can hold fails the document rather than being stored short; see [the page ceiling](#the-page-ceiling)

Cron jobs are visible and manually triggerable in PocketBase Admin → Settings → Crons.

## Full-text search

Archive search uses a [Bleve](https://github.com/blevesearch/bleve) inverted index (not SQLite `LIKE`). The index lives at `{dataDir}/bleve/documents` (Docker: `/app/pb_data/bleve/documents` on the existing `app_data` volume). It is derived data: wiping `pb_data` also wipes the index, and the next boot rebuilds it from documents.

Query behavior:

- Terms are **AND**ed (all must match) and ranked with **BM25**. Quoted `"phrases"` must appear in order.
- **When nothing matches exactly, terms of three letters or more are retried as prefixes** — `amaz` finds *Amazon* while you are still typing, `Rechnung` finds *Rechnungsnummer*. Exact and prefix hits never mix: the retry only runs when the whole-word search came back empty, so a document that really contains the word is never pushed down by one that merely starts with it. Terms containing a digit are excluded (`202` would prefix every year in the archive), and it is a prefix, not a substring — `mazon` still finds nothing.
- Search covers bilingual title/purpose/summary, OCR text, tag/type/correspondent names, and `people_or_organizations`.
- The homepage search box calls `GET /api/app/documents/search` once three characters are typed — below that it says so and leaves the list unfiltered, since a one- or two-letter prefix would match most of the archive. An empty search box still lists via PocketBase (sort by created).
- The `search_documents` tool behind both [search pages](/deep_research) and paperless-ngx `GET /api/documents/?query=` use the same index. Deep Research’s `read_documents` reads `ocr_text` straight from the database, not the index.
- **The agent’s searches relax that AND; the search box does not** (with an embedding model it adds documents close in meaning, below). The prompts ask the model to expand a question into keywords, and requiring every one of them returned nothing for archives that held a document per keyword. So `search_documents` asks for most of the terms (all of 2, n−1 up to 5, then 70%), and if *that* matches nothing at all it retries for any one of them — with one edit of slack on words of five letters or more that carry no digits — capping the retry at 10 hits. A quoted phrase stays mandatory in both attempts. The search box keeps strict AND on purpose: there the query is a filter over documents the user knows, and a hit that dropped a word reads as a bug.
- PocketBase collection filters (`field ~ "..."`) remain available to API clients; the UI no longer uses them for the search box.

With an embedding model bound there is a second index beside it, at
`{dataDir}/bleve/chunks`: one entry per embedded passage, carrying the passage's
text and its vector. The search agent queries it, and so does the search box:
there every strict keyword match is ranked together with up to 20 documents
whose passages stand out from the rest by meaning (reciprocal rank fusion, the
list's filters applied to both), so a paraphrase or a word in another language
still finds its document. A nearest neighbour is not enough: a document counts
when its similarity clears the query's median document by 15% of the headroom
above that median, or, on `bge-m3`, when it reaches 0.48 — the one model with a
measured floor, and the only way "invoice" finds an archive of German invoices
that all score alike. An unrelated query lists nothing. Each search
box query costs one embedding request. It is derived data like the first —
rebuilt from `data.db` with no calls to the embedding provider — and
it is versioned by model *and* dimension count, so changing either wipes and
refills that index alone while keyword search keeps serving. Clearing the
embedding binding deletes the directory.

Admins can force a rebuild from **Maintenance → Rebuild search index** (`POST /api/app/search/reindex`). It rebuilds both indexes.

## Deep Research

Both search pages, how they retrieve and what a broad question costs are on
their own page: [Deep Research](/deep_research).

## Chat sessions

The two search pages (`/rag/search`, `/rag/research`) and a document's **Ask AI** page (`/document/<id>/ask`) both save their conversations. Each page lists past chats in a sidebar, gives the open one its own URL (`/rag/search/<chatId>`, `/rag/research/<chatId>`, `/document/<id>/ask/<chatId>`), and lets you rename or delete a chat. One sidebar covers both: a chat is listed on either page, and opens on the path it ran on.

The server owns the transcript. A request carries a session id and one new message — `POST /api/app/search` with `{"session_id": "...", "content": "...", "mode": "search|research"}`, `POST /api/app/documents/<id>/chat` with `{"session_id": "...", "content": "..."}` — and the history is read back from the database rather than replayed by the browser. An omitted `session_id` starts a new chat, titled after its first message.

`POST /api/app/search/stream` takes the same body and saves the same way; because its status line goes out with the first step event, the stored turn arrives as the `saved` event that closes the stream rather than as the response body. Both go through it — Deep Research for its step events, AI assisted search for the heartbeat underneath, since a response that writes nothing until the answer is ready is indistinguishable from a hung backend to a proxy with a read timeout. Reopening a search chat restores the page its last turn ran on.

A run does not end when its connection does. Losing the stream costs the live view of the run, not the run: it finishes and the turn is stored, so a network drop mid-answer leaves the chat waiting in the sidebar rather than losing an answer the provider has already been paid for. Cancelling is therefore said explicitly — send a `run_id` with the request and `POST /api/app/search/cancel` with `{"run_id": "..."}` to stop it. A run left uncancelled ends on its own budget, 20 minutes.

A chat is created as soon as the first message is submitted, before the model is called, so the whole run happens inside the conversation it will be stored in — that id is also the `x-opencode-session` an OpenCode request carries, so every turn of one chat shares a prompt cache. A first turn that never produces an answer takes its chat back with it, so a provider that is misconfigured or times out still leaves no empty chats behind. A breach of the 500-chat limit is refused up front with `409` rather than after a reply has been paid for. If a reply is produced but cannot be stored, the response carries `"saved": false` and the answer is shown without being added to the history.

Managing saved chats:

| Route | Purpose |
| --- | --- |
| `GET /api/app/chats` | List chats. Filters: `kind=search\|document`, `document=<id>`; paged with `page` / `perPage` |
| `GET /api/app/chats/{id}` | One chat with its messages |
| `PATCH /api/app/chats/{id}` | Rename (`{"title": "..."}`) |
| `DELETE /api/app/chats/{id}` | Delete the chat and its messages |

The chat list is not paged: one request carries every chat an account can hold, and the sidebar scrolls. A transcript read is capped, and the cap drops the oldest turns — `truncated` says the head was lost, never the live end.

Limits:

- A message has no length cap of its own; the request body is capped at 2 MB. What a long question costs is the model's context window, and the research composer says so before it is sent when that window is known.
- The model sees the transcript whole, up to the 500 most recent rows of one read. Nothing else trims it: the provider's context window is the only limit, and a conversation that outgrows it fails with the provider's own error rather than quietly losing its oldest turns. Research turns report what they used — see [AI providers → The model catalogue](/ai_providers#the-model-catalogue).
- A **research** chat stores more than the conversation. Every turn's tool calls and everything the tools returned are stored as they happen and replayed with the next question, so a follow-up builds on what earlier turns found instead of reading the same documents again — and a run that is cancelled, refused or cut off by a restart keeps the work it had already done. Those rows are the machinery under a turn, not messages: the transcript shows the questions and answers, with the rest folded into the trail beneath them. A turn that never reached an answer says so, and offers to continue. Search and Ask AI store one question and one answer, as before.
- One run at a time per research chat. A second question asked while one is still working is refused rather than interleaved into the stored conversation.
- An account may keep 500 chats. Past that, new ones are refused until some are deleted; nothing is pruned automatically.

Deleting a document deletes its Ask AI chats, and deleting an account deletes all of its chats. The `chat_sessions` and `chat_messages` collections carry no API rules, so — like `passkey_credentials` — they are not reachable through `/api/collections` at all and `/api/app/chats` is the only way in. That is deliberate: a client able to write its own `assistant` messages could plant text that the server would then replay to the model as a genuine prior answer.

## Troubleshooting

- **Stuck on setup wizard, OCR fails, AI extraction fails:** see [AI providers → Troubleshooting](/ai_providers#troubleshooting).
- **Upload succeeds but stays pending:** ensure the backend server is running; the worker starts with `serve`.
- **Settings page missing:** log in with the admin email (the account created at setup / `superuser upsert`). Regular non-admin users do not see Settings.
- **Auth errors in frontend:** delete the PocketBase data dir (`backend/pb_data`) and restart to recreate collections, then reload the app. This also deletes the Bleve index (rebuilt on next boot).
- **Search misses a document:** wait for processing to finish, then retry. Admins can use **Maintenance → Rebuild search index**, or delete `backend/pb_data/bleve` and restart.
