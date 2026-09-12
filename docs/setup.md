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
| `METRICS_ADDR` | unset (off) | Address for the OpenTelemetry metrics endpoint, served as a Prometheus scrape target on its own port so nothing scraping it holds a credential for the archive. A bare port (`9464`) or one with no host (`:9464`) listens on every interface, which is the only form reachable in Docker through a published port &mdash; the mapping is deliberately not in `docker-compose.yml`, since an unauthenticated endpoint should not become internet-reachable by default. Name a host (`127.0.0.1:9464`) to keep it on loopback when running the binary directly. Exposes request rate and latency by route, job durations by outcome, pending queue depth, outbound AI/OCR latency by provider and model, token counts, how full the instance is against every `LIMIT_*` below, and Go runtime metrics; no document text, account names or paths. The usage gauges read the same measurement the Settings page shows, and pair with an allowance under the same `resource` label, so utilisation is one division &mdash; `lemmary_usage / lemmary_limit`, and `lemmary_usage_bytes / lemmary_limit_bytes`. A `LIMIT_*` you have not set emits no allowance series at all, since `0` is an allowance somebody sells and cannot also mean unbounded. Costs three queries per scrape: a `COUNT` over `processing_jobs`, plus the usage aggregate (one index-only scan of `documents` and a `COUNT` over `users`). A port that will not bind is logged and the app serves anyway. |
| `IMPORT_ALLOW_PRIVATE` | unset (blocked) | Set to `1`/`true` to let ngx import reach loopback and RFC1918 hosts. Link-local / cloud-metadata addresses stay blocked. Needed when Paperless-ngx is on the same LAN or Docker network. |
| `UPLOAD_MAX_MB` | `100` | Cap on a staged split-document PDF upload, in megabytes. Read at startup, not from Settings: staging a PDF costs several times its size in memory while pages are rendered, so it protects the host as much as it shapes the product. A malformed or non-positive value falls back to the default rather than failing the boot. Per-file uploads are capped separately by the `documents.file` field (20 MB). |
| `IMPORT_STAGING_MAX_BYTES` | `1073741824` (1 GiB) | Cap on an archive staged for import (Amazon orders, Lemmary backup), in bytes. Staging a new archive discards that account's previous one, so this is also the disk a single account can occupy while deciding whether to confirm — the staging area's ceiling is roughly this times the number of accounts. Lower it on a small volume; raise it for a library whose backup runs past a gigabyte. A malformed value, or one under 1 MiB, falls back to the default rather than rejecting every upload. |
| `PASSKEY_RP_ID` | derived from the request host | Relying-party ID for [passkey sign-in](/passkeys): a bare domain name, no scheme and no port. Defaults to the hostname the request arrived with, which is right whenever the proxy forwards the public `Host`. Set it when it does not, or to pin a parent domain (`example.com` while serving `app.example.com`). **Every enrolled passkey is bound to this value — changing it makes all of them unusable.** Read at startup, not from Settings. |
| `PASSKEY_ORIGINS` | derived from the request scheme + host | Comma-separated full origins (scheme, host and port) allowed to complete a passkey ceremony. Defaults to the origin the request arrived on, using `X-Forwarded-Proto` for the scheme when present. Set it when the app is reachable at more than one origin, or when a TLS-terminating proxy does not set that header. |
| `LIMIT_DOCUMENTS` | unset (unlimited) | Total documents this instance may store. |
| `LIMIT_DOCUMENT_PAGES` | unset (unlimited) | Total pages across all stored documents. Anything that is not a PDF counts as one page &mdash; including a multi-page `.docx` or `.xlsx`, whose real page count is not knowable without converting the file. |
| `LIMIT_STORAGE_BYTES` | unset (unlimited) | Total bytes of stored document files. Counts the uploaded originals only, not the generated thumbnails or the extracted OCR text. When sizing a volume, budget for the database separately: extracted text is stored inline in the row and a single document may hold up to 20 Mi characters of it, so a text-heavy library's `data.db` can approach the same order as the files themselves. |
| `LIMIT_FILE_BYTES` | unset (unlimited) | Largest single document file, in bytes. Can only **lower** the effective cap: the `documents.file` field carries its own 20 MB `MaxSize` that PocketBase validates on every save, and no value here can raise it. Distinct from `UPLOAD_MAX_MB`, which caps the one staged PDF a split is cut from rather than each document it produces. |
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
  `GET /api/app/limits` for an admin session and on the **Management** page. The
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
which holds 20 Mi characters — the same 20 MB the `documents.file` field accepts,
counted in characters instead of bytes. Nothing else bounds an OCR result:
Mistral is the only provider that documents a page limit (1000 pages, which is
where this number comes from), and Google Vision reads however many pages the
file has, five at a time. The page count, taken before the first provider call,
is the one measurement that says whether the answer could be stored — and
refusing there means an over-long document costs nothing rather than being paid
for and then discarded.

Consequences worth knowing:

- `LIMIT_FILE_PAGES` can lower this and cannot raise it, the same way
  `LIMIT_FILE_BYTES` relates to the 20 MB `documents.file` cap. When both would
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
4. **Models** — pick provider → model for OCR and metadata extraction (chat/search inherit extraction), and optionally for embeddings. All three arrive prefilled when the providers came from the guided form; embeddings can be set to **None**, and setup is complete without them.

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
2. Open **Settings** in the nav (shown when `/api/app/me` reports `is_admin`). It has one tab per section, each on its own path and each saving only its own fields: **Appearance** (`/settings`), **AI** (`/settings/ai`), **Processing** (`/settings/processing`), **Worker** (`/settings/worker`) and **Duplicates** (`/settings/duplicates`). On the AI tab, add providers, then bind OCR / extraction / chat / search to a provider and model — see [Binding models in Settings](/ai_providers#binding-models-in-settings). Changes hot-reload the in-process clients (no restart).

`WORKER_CRON_EXPR` is not editable there; change `.env` and restart, or use PocketBase Admin → Settings → Crons.

**Extra extraction rules** (Processing tab) is the one part of the extraction prompt an admin writes. Whatever is in it is appended to the built-in prompt, after the list of existing correspondents and document types and before the format rules, so it can state house conventions the fixed prompt cannot know — “treat *Rechnung* as the document type Invoice”, “tag insurance documents with the policy number”. It cannot change which fields are stored: the pipeline parses the answer into a fixed set, and the prompt says so after the rules. Up to 4000 characters, empty by default, and applied to documents processed or reprocessed from then on. It is tenant-owned, so a managed instance keeps it. The extraction log line reports its length as `rule_chars`, and each document's `extract_metadata` step run records the prompt it actually ran under (see below).

`EXTRACTION_PROMPT_VERSION` is not offered there either. It is pure bookkeeping — it is recorded on each document's `extract_metadata` step run so metadata can be traced back to a prompt, and never reaches the prompt itself — so there is nothing for an admin to tune. Where extraction rules are set, that step run records `v1+rules.<digest>` instead of the bare version: the rules change the prompt while the version does not, and a run recorded under `v1` alone would name a prompt that no longer exists. Documents extracted with no rules keep the bare version, so nothing changes for an instance that sets none. `PATCH /api/app/settings` still accepts `extraction_prompt_version`, and it can be edited in PocketBase Admin → `app_settings`.

## Management (admin UI)

**Management** in the nav (admin only, next to Settings) holds maintenance actions that run over the whole library, not per document:

- **Scan for duplicates** — `POST /api/app/duplicates/scan`, see [Duplicate detection](#duplicate-detection).
- **Clear stale data** — `POST /api/app/taxonomy/prune` deletes every tag, correspondent and document type that no document references any more (left behind by deleted documents, renames, or an aborted import). Documents are never modified. Reference collection and deletion share one transaction, so a document saved concurrently either counts as a reference or fails its own relation check; it cannot keep a dangling id.
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

Rejections come back as `400` at preview time rather than mid-import: not a readable zip, no PDFs, more than 5000 PDFs, an upload over `IMPORT_STAGING_MAX_BYTES` (1 GiB by default), or an archive that decompresses beyond 8 GiB (a zip bomb). A single PDF over the 20 MB `documents.file` limit is not fatal — it is flagged `oversized` in the preview and skipped on import. A PDF over [the page ceiling](#the-page-ceiling) is refused per entry as the run proceeds rather than at preview time, since an archive's real page counts are only discoverable by opening every PDF in it.

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

Because the address comes from whoever is signed in, every request goes through a dial-time guard that allows only RFC1918 and IPv6 ULA addresses: public addresses, `localhost` and link-local `169.254.x` (the cloud metadata service) are refused, redirects are not followed, and a `Location` header pointing at another host is rejected. The staged document is capped at the `documents.file` field's own 20 MB, checked before each scan so a full document is reported while there is still something to do about it.

### Document splitting

**Split documents** (`/upload/split`) takes a PDF that holds several separate documents scanned into one file and creates one document per part. The staged original is discarded — only the parts become documents.

Uploading and splitting are two steps, so nothing is created before the user has decided where the cuts go:

1. `POST /api/app/split/upload` (multipart, field `file`) streams the PDF to `<data dir>/temp/split_upload/<upload id>/source.pdf` and renders one thumbnail per page next to it at 900 px on the longest edge — larger than the 400 px document-card preview, because deciding where a document ends means reading the letterhead of a page (a single `pdftoppm` run; `pdftoppm` zero-pads its own output, so the files are renamed to `page-<n>.png`). The response carries `upload_id`, `file_name`, `page_count`, `size_bytes` and `expires_at`.
2. `GET /api/app/split/page?upload_id=...&page=n` serves one cached thumbnail as `image/png`. The endpoint needs the session token, which an `<img src>` cannot carry, so the SPA fetches each page and wraps it in a blob URL.
3. `POST /api/app/split` with `{ "upload_id": "...", "parts": [{ "from": 1, "to": 2 }, …] }` starts the split and returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/split/status?job_id=...` for `progress` (`{ done, total }`) until `status` is `completed` (with `result`) or `failed` (with `error`). The `result` counts `created`, `skipped_duplicates`, `skipped_oversized` and `failed`, plus up to 25 per-part error messages and the `document_ids` created. Each part is saved as `pending`, so it goes through the normal OCR + AI [processing flow](#processing-flow).

`parts` must cover every page exactly once, in order — that is all the cut-marking UI can express, so a gap, an overlap, an unsorted list or a range outside the file comes back as `400` with a message naming the page it went wrong at. A rejected request leaves the upload staged, so a corrected request can follow.

Parts are named after the pages they hold (`scan-page-1.pdf`, `scan-pages-2-5.pdf`) from a sanitized form of the uploaded file name. `pdfseparate` and `pdfunite` copy the original page objects rather than re-rasterizing, so the text layer and image quality survive; both stamp a random trailer `/ID` into what they write, which is rewritten to a fixed value so extracting the same pages twice produces the same bytes. Without that, the exact-duplicate check could never recognize a re-split part and splitting the same scan twice would silently create a second copy of everything. The rewrite only happens when the `/ID` sits past the offset `startxref` names (so no cross-reference offset can shift) and is reverted if the result does not open.

`DELETE /api/app/split/upload?upload_id=...` discards a staged PDF the user chose not to split. Staged uploads expire after 30 minutes and are swept on the next upload, including directories left behind by an earlier process — the staging registry and the job state are in memory, so both are lost on restart. Confirming consumes the upload id: the same PDF cannot be split twice, and one split may run at a time per user (a second start returns `409`).

Rejections come back as `400` at upload time: not a readable PDF (the `%PDF-` header and `pdfinfo` decide, not the declared content type), a one-page PDF (nothing to split), more than 100 pages, or an upload over 100 MiB. A part over the 20 MB `documents.file` limit is not fatal — it is counted as `skipped_oversized`.

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

Because a restore does not run the pipeline, near-duplicate detection does not re-run over the restored documents. Exact duplicates are still rejected on create by checksum, and `duplicate_of` / `text_fingerprint` come back from the archive; **Management → Scan for duplicates** re-derives near-duplicate links across the whole library when you want them recomputed. A restored document also keeps whatever thumbnail the archive carried — an older archive without `.preview.png` sidecars leaves those documents without one until they are reprocessed.

What a restore does *not* preserve: **document ids**. Restored documents get fresh ids, and `duplicate_of` is remapped to the restored copy when the original is in the same archive (dropped when it is not). `created` and `updated` are written back after the save, so the library comes back in its original order. Documents whose file checksum is already in your library are skipped, which makes restoring the same archive twice safe.

Archives exported before manifests existed still restore: their documents are reconstructed from the entry names alone. Only orphan taxonomy — and that one sidecar-lookalike case — cannot be recovered from them.

### API

1. `POST /api/app/import/archive/upload` (multipart, field `file`) stages the zip and returns the preview, including `upload_id` and `has_manifest`.
2. `DELETE /api/app/import/archive/upload?upload_id=...` discards a staged archive. Staged archives also expire on their own after 30 minutes.
3. `POST /api/app/import/archive` with `{ "upload_id": "...", "mode": "restore" | "reprocess" }` returns `202 Accepted` with `{ "job_id", "status": "running" }`.
4. `GET /api/app/import/archive/status?job_id=...` until `status` is `completed` (with `result`) or `failed` (with `error`).

Job state is in memory for the running process only, and one import may run at a time per user. Staging a new archive discards the account's previous one, so an account holds at most one at a time. Uploads are capped by `IMPORT_STAGING_MAX_BYTES` (1 GiB by default) and 5000 documents, each entry at the 20 MB document limit; one budget covers everything the inspection and the restore inflate, so an archive that unpacks far beyond its size is rejected as a zip bomb. A restored document that carries its own OCR sidecar is exempt from [the page ceiling](#the-page-ceiling) — it needs no OCR, so a long document archived before that ceiling existed still restores; an entry without a sidecar takes the ordinary upload path and is subject to it.

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
- **Because you asked for all of them** — Settings → **Always require review for new documents** (off by default). With it on, every document the AI extracted metadata for waits in the Inbox however confident the extraction was, reprocessed documents included: nothing reaches `completed` except by your saying so. This is the setting for the workflow of uploading as things arrive and correcting a month's worth in one sitting.

  It does not apply to paperless-ngx imports in preserve mode. Those run no AI extraction — the metadata is the one curated in paperless — so there is nothing for a review to check, and a migrated archive is not emptied into the Inbox.

  Turning it on also rearranges the two lists around it, because otherwise **Documents** would be mostly a second copy of the Inbox:

  - **Documents** defaults to **Completed** — the archive you have actually read. The status dropdown still offers *All statuses*, and picking it puts `?status=all` in the URL.
  - An upload of several files, an Amazon import and a split all finish in the **Inbox** rather than on **Documents**, which would filter out exactly what was just added. A single-file upload still opens that document.

  The instance announces the setting on `GET /api/app/meta` so the SPA can do both for every user, not only for admins; only an admin can change it.

Two ways out, both of which set the status to `completed`:

- **Save corrections** on the detail page — fixing a misread `document_date` and saving counts as reviewing it.
- **Mark reviewed**, when the metadata is already right — on the detail page, on a card in the list, or on a selection of cards at once from the Inbox. It writes nothing but the status, so `metadata_source` still records that the model wrote the metadata.

A document marked reviewed while `duplicate_of` is set keeps that link: the relationship is still true, and marking it reviewed says you looked and kept both.

### Duplicate detection

- **Exact duplicates** — on create, the uploaded file is hashed (SHA-256) into `documents.checksum`. A second upload with the same checksum for the same user is **rejected**, with an error pointing at the existing document id. Uniqueness is enforced with a per-user unique index on non-empty checksums so concurrent uploads cannot both succeed.
- **Near-duplicates (optional)** — after OCR, a `detect_duplicates` step can compare normalized OCR text (SimHash + Jaccard). This is controlled by Settings → **Enable near-duplicate detection after OCR** (off by default). Matches are marked `needs_review` with `duplicate_of` set to the earlier document (never a newer one), so they show up in the [Inbox](#review-inbox); AI extract/apply steps are skipped.
- **Bulk scan** — Management → **Scan for duplicates** (admin) backfills missing checksums/fingerprints and marks exact (and, if enabled, near) duplicates among existing documents.

Text extraction:

- **PDF and images** — configured OCR provider (Google Vision, Mistral Document OCR, an OpenAI/OpenRouter model that accepts files/images, or the [local Docling sidecar](/local_ocr))
- **TXT, CSV, DOCX, XLSX** — native parsers (no OCR API call); preview is skipped for these formats. A DOCX or XLSX whose text runs past what `ocr_text` can hold fails the document rather than being stored short; see [the page ceiling](#the-page-ceiling)

Cron jobs are visible and manually triggerable in PocketBase Admin → Settings → Crons.

## Full-text search

Archive search uses a [Bleve](https://github.com/blevesearch/bleve) inverted index (not SQLite `LIKE`). The index lives at `{dataDir}/bleve/documents` (Docker: `/app/pb_data/bleve/documents` on the existing `app_data` volume). It is derived data: wiping `pb_data` also wipes the index, and the next boot rebuilds it from documents.

Query behavior:

- Terms are **AND**ed (all must match) and ranked with **BM25**. Quoted `"phrases"` must appear in order.
- Search covers bilingual title/purpose/summary, OCR text, tag/type/correspondent names, and `people_or_organizations`.
- The homepage search box calls `GET /api/app/documents/search`. An empty search box still lists via PocketBase (sort by created).
- Deep Search’s `search_documents` tool (both modes) and paperless-ngx `GET /api/documents/?query=` use the same index. Research’s `read_documents` reads `ocr_text` straight from the database, not the index.
- **The agent’s searches relax that AND; the search box does not.** The prompts ask the model to expand a question into keywords, and requiring every one of them returned nothing for archives that held a document per keyword. So `search_documents` asks for most of the terms (all of 2, n−1 up to 5, then 70%), and if *that* matches nothing at all it retries for any one of them — with one edit of slack on words of five letters or more that carry no digits — capping the retry at 10 hits. A quoted phrase stays mandatory in both attempts. The search box keeps strict AND on purpose: there the query is a filter over documents the user knows, and a hit that dropped a word reads as a bug.
- PocketBase collection filters (`field ~ "..."`) remain available to API clients; the UI no longer uses them for the search box.

With an embedding model bound there is a second index beside it, at
`{dataDir}/bleve/chunks`: one entry per embedded passage, carrying the passage's
text and its vector. Only Deep Search's tools query it. It is derived data like
the first — rebuilt from `data.db` with no calls to the embedding provider — and
it is versioned by model *and* dimension count, so changing either wipes and
refills that index alone while keyword search keeps serving. Clearing the
embedding binding deletes the directory.

Admins can force a rebuild from **Management → Rebuild search index** (`POST /api/app/search/reindex`). It rebuilds both indexes.

## Deep Search

Deep Search uses a tool-calling agent over the Bleve full-text index, in two modes, one per path under `/rag`:

- **Search** (`/rag/search`) — one round of `search_documents`, answered from titles, summaries and short OCR snippets. Results are shown as document cards.
- **Research** (`/rag/research`) — the agent searches, reads the documents it finds (`read_documents`), surveys many at once when the question spans a topic (`survey_documents`), counts when asked how many (`count_documents`), and writes a markdown answer citing each document it used, with the documents it drew on listed under the answer. Progress streams over `POST /api/app/search/stream` (server-sent events), so each search, read, survey and count appears as it happens.

Research has no round or document limit. It keeps searching and reading until it can answer, the model stops making progress, or a completion is rejected because the conversation exceeded the model's context window. Without a language-model provider, Deep Search returns a configuration error — see [AI providers and models](/ai_providers).

### How Deep Search finds documents

Both modes reach the archive through the same `search_documents` tool, and it
runs up to two searches for every query.

- **Keywords (BM25)** over the documents index, [relaxed in two
  rungs](#full-text-search) rather than the strict AND the Documents page keeps:
  there the query is a filter you typed, here it is a guess the model made from
  a question.
- **Meaning (kNN)** over `bleve/chunks`, a second index holding one entry per
  embedded passage with its vector, searched by cosine similarity to the
  embedded question. This half exists only when `AI_EMBEDDING_MODEL` is set and
  documents have actually been embedded. The model can be a hosted one or [one
  you run yourself](/local_embeddings).

The two lists are fused by reciprocal rank fusion: a document scores the sum of
`1/(60 + rank)` over the lists it appears in. Only positions are read, never
scores — BM25 and cosine are not on the same scale, and normalising one against
the other would be guesswork. A document found by either signal survives; one
found by both rises.

The passages the model is shown come from the same chunk index: a keyword search
over the passages of exactly the documents being returned, narrowed to the
sentence around the match. Without a chunk index they are cut from the OCR text
around the query's terms instead, and failing that from the index's own
highlight — the model always gets verbatim text, whatever the retrieval was. A
read of a long document is always an excerpt: its head plus the passages ranked
the same way against the read's `focus`, or against the user's question when the
model named none. The whole text of a long document is never handed to the
research model.

With an embedding model set, the prompt tells the model that one search already
crosses languages and not to repeat a search translated into another one; the
`DEEP_SEARCH_LANGUAGES` list is then only a hint for spelling exact terms.
Without one, the model is asked to search once per configured language, since
translation is the only thing carrying an English question to a German invoice.

Filters — a tag, a type, a correspondent, a date range — are properties of a
document, and the chunk index deliberately carries none of them, so that
renaming a tag never rewrites a vector. They are resolved against the documents
index instead and applied to the passage search as a list of document ids.

What it costs: one embedding request per distinct query string per turn (the
result is reused for the rest of that turn, including a focused read with the
same words), and one or two extra index searches. Everything on the dense path
degrades rather than fails: no model bound, an embedding call that errored, an
index still rebuilding — the search is the keyword search it has always been.
Each call logs one line, `deep search retrieval lexical=… dense=… fused=…
embedded=…`, which is where to look when an answer seems to have missed a
document.

### How Research covers a topic

Reading documents one call at a time is right for a needle question and wrong
for a topic: two hundred documents read into one conversation is two hundred
documents re-sent on every later round. Three things keep a broad question
affordable.

- **Distilled reads.** When a `read_documents` call would put more than about
  32 KB of text, or more than five documents, into the conversation, the
  documents are read by the **Deep Search helper** model instead. The research
  model gets, per document, notes on what it says about the focus, verbatim
  quotes to cite, and any requested values — never the text. Smaller reads pass
  through as excerpts, because on a needle question the exact wording is the
  point. The helper is shown each document whole, up to 400 KB, and several
  short documents share one call.
- **`survey_documents`.** For "everything about X" or "the total of Y over the
  year": the same retrieval as a search, kept to 300 documents by default and
  1000 at most, every document read by the helper for one question, one compact
  row back per document. Number fields (`fields: [{name, type: "number"}]`) are
  summed, averaged and bounded on the server, per currency, and the model is
  told to report those figures rather than add rows itself. Progress streams as
  "Surveyed 120 of 300 documents".
- **`count_documents`.** For "how many" and "how are they distributed": filters
  alone are answered by the database (`COUNT(*)`, optionally `GROUP BY` type,
  correspondent, year, month or tag); with query text the index reports the
  exact strict-match total, and a grouped breakdown takes the index's ids to the
  database (marked approximate past 5000 matches). A filter naming a type,
  correspondent or tag that does not exist counts zero and says which name did
  not resolve, rather than matching everything.

The helper is a separate binding (`AI_SEARCH_HELPER_MODEL`, or **Deep Search
helper** in Settings) because this work is many cheap calls where the research
loop is a few expensive ones; unset, it falls back to the Search model and the
same features run at the Search model's price. Helpers that reject JSON mode are
retried in plain text and parsed leniently. Every completion logs its token usage
(`ai completion usage prompt_tokens=… cached_tokens=… completion_tokens=…`) and a
research run logs its total, which is where to look when checking what a question
cost.

Models that emit tool calls in their content rather than natively (the DSML
path) are told to answer after one round of tool results, so they cannot chain a
count into a survey into a read; they get one tool round and the answer.

Each mode is its own path — `/rag/search` and `/rag/research` — so the mode is carried by the URL and survives a reload, the back button, a bookmark and a shared link. They share the `/rag` parent, which is what lets one navigation entry cover both; `/rag` on its own redirects to Search.

The mode can only be chosen before a chat has a turn. A transcript is a sequence: its answers were produced by one mode, and the next turn replays them to the model as its own prior work, so switching underneath would answer a later question in a way the earlier ones do not support. Once a chat exists the switch shows which mode it is in and stops being a link — starting a new chat is the way to the other one. The server enforces this too: a turn sent under a mode the chat is not in is a 409, and opening `/rag/search/<research-chat>` redirects to the path that matches. A saved chat reopens on the path matching the mode it ran in.

Configure **Search provider/model**, **Deep Search helper** and **Deep search languages** in [Settings](/ai_providers#binding-models-in-settings).

## Chat sessions

Deep Search (`/rag/search`, `/rag/research`) and a document's **Ask AI** page (`/document/<id>/ask`) both save their conversations. Each page lists past chats in a sidebar, gives the open one its own URL (`/rag/search/<chatId>`, `/rag/research/<chatId>`, `/document/<id>/ask/<chatId>`), and lets you rename or delete a chat. One sidebar covers both Deep Search modes: a chat is listed whichever mode you are in, and opens on its own mode's path.

The server owns the transcript. A request carries a session id and one new message — `POST /api/app/search` with `{"session_id": "...", "content": "...", "mode": "search|research"}`, `POST /api/app/documents/<id>/chat` with `{"session_id": "...", "content": "..."}` — and the history is read back from the database rather than replayed by the browser. An omitted `session_id` starts a new chat, titled after its first message.

`POST /api/app/search/stream` takes the same body and saves the same way; because its status line goes out with the first step event, the stored turn arrives as the `saved` event that closes the stream rather than as the response body. Both modes go through it — research for its step events, plain search for the heartbeat underneath, since a response that writes nothing until the answer is ready is indistinguishable from a hung backend to a proxy with a read timeout. Reopening a search chat restores the mode its last turn ran in.

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

- A message may be at most 8,000 characters.
- The model sees the most recent 40 messages, capped at 24,000 characters — older turns drop out of the prompt but stay in the transcript.
- An account may keep 500 chats. Past that, new ones are refused until some are deleted; nothing is pruned automatically.

Deleting a document deletes its Ask AI chats, and deleting an account deletes all of its chats. The `chat_sessions` and `chat_messages` collections carry no API rules, so — like `passkey_credentials` — they are not reachable through `/api/collections` at all and `/api/app/chats` is the only way in. That is deliberate: a client able to write its own `assistant` messages could plant text that the server would then replay to the model as a genuine prior answer.

## Troubleshooting

- **Stuck on setup wizard, OCR fails, AI extraction fails:** see [AI providers → Troubleshooting](/ai_providers#troubleshooting).
- **Upload succeeds but stays pending:** ensure the backend server is running; the worker starts with `serve`.
- **Settings page missing:** log in with the admin email (the account created at setup / `superuser upsert`). Regular non-admin users do not see Settings.
- **Auth errors in frontend:** delete the PocketBase data dir (`backend/pb_data`) and restart to recreate collections, then reload the app. This also deletes the Bleve index (rebuilt on next boot).
- **Search misses a document:** wait for processing to finish, then retry. Admins can use **Management → Rebuild search index**, or delete `backend/pb_data/bleve` and restart.
