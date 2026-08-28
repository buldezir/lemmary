# Setup Guide

## Prerequisites

- Go 1.23+
- Node.js 20+
- npm
- [poppler-utils](https://poppler.freedesktop.org/) for all PDF work: `pdftoppm` (preview and page thumbnails), `pdfinfo` (page count), `pdftotext` (page text), `pdfseparate` and `pdfunite` (page extraction for [document splitting](#document-splitting))

On macOS: `brew install poppler`. On Debian/Ubuntu: `apt install poppler-utils`.

## Running from source

```bash
cp .env.example .env
```

### Start the backend

```bash
cd backend
go run . serve --http=127.0.0.1:8090
```

On first run, migrations create:

- `tags`
- `correspondents`
- `document_types`
- `documents`
- `processing_jobs`
- `app_settings` (singleton; seeded from `.env` on first boot)
- `ai_providers` (named OCR/LLM endpoints; seeded from `.env` on first boot)
- `outbound_emails` (outbound mail log when SMTP is not configured; superuser-only)

### Build the frontend

The Go binary serves the SPA and the compiled docs from `public/`. Build them
once, then restart the backend:

```bash
cd frontend
npm ci
npm run build
```

This writes the app to `public/` and these docs to `public/docs/`, both served
at the backend's address.

## Environment variables

All variables live in `.env` at the project root (see `.env.example`).

### Always env-backed

| Variable | Default | Description |
| --- | --- | --- |
| `WORKER_CRON_EXPR` | `* * * * *` | Cron expression for sweeping stuck pending jobs (registered once at startup) |
| `LOG_LEVEL` | unset (no stdout slog) | Min level for JSON slog lines on stdout (`debug`, `info`, `warn`/`warning`, `error`). Ignored while PocketBase `--dev` is on (that mode already prints to the console, including SQL). PocketBase Admin → Settings → Logs still controls the logs table. |
| `IMPORT_ALLOW_PRIVATE` | unset (blocked) | Set to `1`/`true` to let ngx import reach loopback and RFC1918 hosts. Link-local / cloud-metadata addresses stay blocked. Needed when Paperless-ngx is on the same LAN or Docker network. |
| `UPLOAD_MAX_MB` | `100` | Cap on a staged split-document PDF upload, in megabytes. Read at startup, not from Settings: staging a PDF costs several times its size in memory while pages are rendered, so it protects the host as much as it shapes the product. A malformed or non-positive value falls back to the default rather than failing the boot. Per-file uploads are capped separately by the `documents.file` field (20 MB). |
| `VITE_POCKETBASE_URL` | `http://127.0.0.1:8090` | PocketBase API URL (frontend) |
| `VITE_DEV_USER_EMAIL` | — | Dev auto-login email (`users` collection) |
| `VITE_DEV_USER_PASSWORD` | — | Dev auto-login password |

### Applied when changed

These seed `app_settings` and `ai_providers` on first boot **and** are re-applied on any later boot where their value has changed since the last one that acted on them. An unchanged variable is left alone, so a value edited in the **Settings** page survives restarts; changing `.env` (or a container's environment) and restarting does take effect.

The comparison uses a SHA-256 digest per variable stored in `app_settings.env_applied` — a digest, not the value, because several of these are API keys. Removing a variable is a change like any other: `OPENAI_MODEL` returns to its code default (nothing falls back to the extraction model), while `OPENAI_CHAT_MODEL` and `OPENAI_SEARCH_MODEL` empty out and fall back through the binding chain.

| Variable | Default | Description |
| --- | --- | --- |
| `OCR_PROVIDER` | `google_vision` | Which env-seeded provider to bind for OCR (`google_vision`, `mistral`, `openai`, `openrouter`). Names an SDK, not a record id — an id is generated at seed time and cannot be known in advance. |
| `GOOGLE_VISION_API_KEY` | empty | Google Cloud Vision provider key |
| `MISTRAL_API_KEY` | empty | Mistral provider key (OCR + chat completions) |
| `MISTRAL_API_BASE_URL` | `https://api.mistral.ai/v1` | Mistral API base URL |
| `OPENAI_API_KEY` | empty | OpenAI-compatible provider key (extraction, chat, search, optional LLM OCR). If unset, a seeded Mistral provider is bound for those tasks instead. |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible API base URL |
| `OPENAI_MODEL` | `gpt-5.6-luna` | Model ID for metadata extraction |
| `OPENAI_CHAT_MODEL` | `OPENAI_MODEL` | Optional model ID for document chat |
| `OPENAI_SEARCH_MODEL` | `OPENAI_CHAT_MODEL` | Optional model ID for Deep Search |
| `NEAR_DUPLICATE_DETECTION_ENABLED` | `false` | Whether the pipeline runs near-duplicate detection |

A provider record is matched by the default alias `SeedFromEnv` gives it. A renamed or deleted provider is skipped with a warning rather than recreated — recreating would resurrect a provider an admin deliberately removed, and would do it again on every boot.

### Seed-only (first boot → Settings)

These seed `app_settings` when the singleton record does not exist yet. After that, edit them in the app **Settings** page (requires a PocketBase superuser login). Changing `.env` alone will not update a running install.

| Variable | Default | Description |
| --- | --- | --- |
| `MISTRAL_OCR_MODEL` | `mistral-ocr-latest` | OCR model when seeding Mistral |
| `MISTRAL_MODEL` | `mistral-small-latest` | Chat model when Mistral is the only LLM (no `OPENAI_API_KEY`) |
| `OCR_TIMEOUT_SEC` | `40` | OCR request timeout |
| `PROCESSING_RESULT_LANGUAGE` | empty | ISO 639-1 code (e.g. `en`, `de`). When set, `title`, `summary`, `purpose`, and `document_type` are stored in this language; originals go in `*_original` fields. |
| `OPENAI_TIMEOUT_SEC` | `60` | AI request timeout |
| `DEEP_SEARCH_LANGUAGES` | empty | Comma-separated ISO 639-1 codes (e.g. `de,en,uk`) for Deep Search keyword expansion |
| `WORKER_TIMEOUT_SEC` | `300` | Per-job processing timeout |
| `WORKER_MAX_RETRIES` | `0` | Max step retry attempts before a job fails |
| `EXTRACTION_PROMPT_VERSION` | `v1` | Stored on each processing job step run; bookkeeping only, not offered in the Settings UI |

## First-launch setup wizard

On a fresh install the SPA hard-gates until setup is complete:

1. **Create admin** — email + password. Creates a PocketBase `_superusers` account **and** a matching `users` account (same credentials) so the admin can own documents. Replaces PocketBase’s browser installer UI.
2. **Provider** — add at least one API provider (`openai`, `openrouter`, `mistral`, or `google_vision`).
3. **Models** — pick provider → model for OCR and metadata extraction (chat/search inherit extraction).

You can also create the admin via CLI (`go run . superuser upsert EMAIL PASS` from `backend/`; this also upserts the paired `users` account) and/or seed OCR/AI keys in `.env` before first boot; the wizard skips steps that are already done. Until keys are present, regular users see a “setup incomplete” screen; only an admin can finish configuration.

## Settings (admin UI)

1. Sign in with the **admin** email/password (login prefers the `users` account; legacy `_superusers`-only installs are linked automatically via `/api/app/ensure-user`, which sets a hidden `is_app_admin` flag on the paired `users` record).
2. Open **Settings** in the nav (shown when `/api/app/me` reports `is_admin`). Add providers, then bind OCR / extraction / chat / search to a provider and model. Changes hot-reload the in-process clients (no restart).

`WORKER_CRON_EXPR` is not editable there; change `.env` and restart, or use PocketBase Admin → Settings → Crons.

`EXTRACTION_PROMPT_VERSION` is not offered there either. It is pure bookkeeping — it is copied onto each document's `extract_metadata` step run so metadata can be traced back to a prompt, and never reaches the prompt itself — so there is nothing for an admin to tune. `PATCH /api/app/settings` still accepts `extraction_prompt_version`, and it can be edited in PocketBase Admin → `app_settings`.

## Management (admin UI)

**Management** in the nav (admin only, next to Settings) holds maintenance actions that run over the whole library, not per document:

- **Scan for duplicates** — `POST /api/app/duplicates/scan`, see [Duplicate detection](#duplicate-detection).
- **Clear stale data** — `POST /api/app/taxonomy/prune` deletes every tag, correspondent and document type that no document references any more (left behind by deleted documents, renames, or an aborted import). Documents are never modified. Reference collection and deletion share one transaction, so a document saved concurrently either counts as a reference or fails its own relation check; it cannot keep a dangling id.
  The button is disabled while any processing job is `pending` or `running`, so an entity a job is about to attach cannot be swept up. The count comes from the PocketBase collection API (`GET /api/collections/processing_jobs/records`), polled every 5s and re-checked on click. That list rule is `document.user = @request.auth.id`, so the gate only sees jobs on the admin's own documents — another user's in-flight upload does not block the button.
- **Rebuild search index** — `POST /api/app/search/reindex`, see [Full-text search](#full-text-search).

Admin-only items in the nav menu are prefixed with a shield icon (decorative — the items only render for admins in the first place).

## Outbound mail (SMTP / `outbound_emails`)

Configure SMTP under PocketBase Admin → Settings → Mail. When SMTP is **disabled** (the default), PocketBase would normally fall back to local `sendmail`. This app replaces that fallback: messages are written to the `outbound_emails` collection instead (password reset, verification, OTP, auth alerts, etc.).

Browse them in PocketBase Admin as a superuser. Enable SMTP when you want real delivery; the DB sink is skipped while SMTP is on.

## Upload page

**Upload** (`/upload`) groups the ways documents enter the library into sub-sections, each on its own route so a section can be linked, bookmarked, and reached with the browser back button:

| Section | Route | State |
| --- | --- | --- |
| Files | `/upload` (default) | Implemented — drag-and-drop / file-picker upload, see the processing flow below |
| Amazon orders | `/upload/amazon` | Implemented — imports the invoice PDFs out of an order archive requested from Amazon, see [Amazon order import](#amazon-order-import) |
| Split documents | `/upload/split` | Implemented — splits a PDF holding several joined documents into one document per part, see [Document splitting](#document-splitting) |

Plain file upload stays on `/upload` itself (an index route), so existing links and the **Upload** nav entry keep landing on it.

### Amazon order import

Request the archive from Amazon under Account → Request your data → Your Orders; Amazon emails a download link once the export is ready. The zip holds CSV reports, delivery photos and — under `Additional Data/Retail.TransactionalInvoicing.*` — the invoice PDFs. Only the PDFs are imported; every other entry is counted as ignored and left alone.

Uploading and importing are two steps, so nothing is created before the user has seen what the archive holds:

1. `POST /api/app/import/amazon/upload` (multipart, field `file`) streams the zip to `<data dir>/temp/amazon_import/` — it is never buffered in memory, since real exports run to hundreds of MB. The archive is scanned and every PDF is hashed, then returned as a preview: total PDF count, how many are importable, how many are duplicates or oversized, the ignored-entry count, and the per-file list. Duplicates are PDFs whose checksum already exists among the owner's documents (`duplicate_of` names the existing id) or that repeat earlier in the same archive. Imported documents are named `<parent folder>-<file>`, because Amazon numbers the invoices per folder (`1.pdf`, `2.pdf`, …).
2. `POST /api/app/import/amazon` with `{ "upload_id": "..." }` starts the import and returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/import/amazon/status?job_id=...` for `progress` (`{ done, total }`) until `status` is `completed` (with `result`) or `failed` (with `error`). The `result` counts `imported`, `skipped_duplicates`, `skipped_oversized` and `failed`, plus up to 25 per-file error messages. Each imported document is saved as `pending`, so it goes through the normal OCR + AI [processing flow](#processing-flow).

`DELETE /api/app/import/amazon/upload?upload_id=...` discards a staged archive the user chose not to import. Staged archives expire after 30 minutes and are swept on the next upload, including files left behind by an earlier process — the staging registry and the job state are in memory, so both are lost on restart. Confirming consumes the upload id: the same archive cannot be imported twice, and one import may run at a time per user (a second start returns `409`).

Rejections come back as `400` at preview time rather than mid-import: not a readable zip, no PDFs, more than 5000 PDFs, an upload over 1 GiB, or an archive that decompresses beyond 8 GiB (a zip bomb). A single PDF over the 20 MB `documents.file` limit is not fatal — it is flagged `oversized` in the preview and skipped on import.

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

## Processing flow

1. User uploads a document from `/upload`
2. PocketBase stores the file and creates a `processing_jobs` record via Go hook
3. An `OnRecordAfterCreateSuccess` hook dispatches the job immediately; a cron job (`process_pending_jobs`) sweeps any stuck pending jobs
4. Worker generates a PNG preview from the first PDF page (via `pdftoppm`), then extracts text, optionally checks for near-duplicates, and runs AI metadata extraction
5. Extracted metadata is saved on the document
6. UI shows status on list and detail pages

Metadata extraction sends the current document's OCR text **and** up to 500 of that owner's existing correspondent names and document-type names to the configured LLM provider, so the model can reuse existing labels instead of creating near-duplicates. Names are sent as a JSON array marked as untrusted data. Apply still matches exact names, then a punctuation/accent-insensitive form (`Amazon EU S.à r.l.` vs `Amazon EU S.a.r.l.`). Existing `name` / `name_original` values are not overwritten on reuse.

### Duplicate detection

- **Exact duplicates** — on create, the uploaded file is hashed (SHA-256) into `documents.checksum`. A second upload with the same checksum for the same user is **rejected**, with an error pointing at the existing document id. Uniqueness is enforced with a per-user unique index on non-empty checksums so concurrent uploads cannot both succeed.
- **Near-duplicates (optional)** — after OCR, a `detect_duplicates` step can compare normalized OCR text (SimHash + Jaccard). This is controlled by Settings → **Enable near-duplicate detection after OCR** (off by default). Matches are marked `needs_review` with `duplicate_of` set to the earlier document (never a newer one); AI extract/apply steps are skipped.
- **Bulk scan** — Management → **Scan for duplicates** (admin) backfills missing checksums/fingerprints and marks exact (and, if enabled, near) duplicates among existing documents.

Text extraction:

- **PDF and images** — configured OCR provider (Google Vision, Mistral Document OCR, or an OpenAI/OpenRouter model that accepts files/images)
- **TXT, CSV, DOCX, XLSX** — native parsers (no OCR API call); preview is skipped for these formats

Cron jobs are visible and manually triggerable in PocketBase Admin → Settings → Crons.

## Full-text search

Archive search uses a [Bleve](https://github.com/blevesearch/bleve) inverted index (not SQLite `LIKE`). The index lives at `{dataDir}/bleve/documents` (Docker: `/app/pb_data/bleve/documents` on the existing `app_data` volume). It is derived data: wiping `pb_data` also wipes the index, and the next boot rebuilds it from documents.

Query behavior:

- Terms are **AND**ed (all must match) and ranked with **BM25**. Quoted `"phrases"` must appear in order.
- Search covers bilingual title/purpose/summary, OCR text, tag/type/correspondent names, and `people_or_organizations`.
- The homepage search box calls `GET /api/app/documents/search`. An empty search box still lists via PocketBase (sort by created).
- Deep Search’s `search_documents` tool and paperless-ngx `GET /api/documents/?query=` use the same index.
- PocketBase collection filters (`field ~ "..."`) remain available to API clients; the UI no longer uses them for the search box.

Admins can force a rebuild from **Management → Rebuild search index** (`POST /api/app/search/reindex`).

## LLM setup (OpenAI / OpenRouter / Mistral)

Prefer **Settings** in the UI (admin): add a provider with SDK `openai`, `openrouter`, or `mistral`, then select it for extraction, chat, and search. Mistral uses the same [OpenAI-compatible chat completions](https://docs.mistral.ai/api) endpoint as the other LLMs (`/v1/chat/completions`); OCR still uses Mistral’s dedicated Document OCR API.

For a fresh install you can put `OPENAI_API_KEY` and/or `MISTRAL_API_KEY` in `.env` so they seed `ai_providers` rows on first boot. If both are set, OpenAI is bound for LLM tasks and Mistral can still be chosen in Settings. If only Mistral is set, it is bound for extraction, chat, and search using `MISTRAL_MODEL` (default `mistral-small-latest`).

Without an LLM provider, AI extraction, document chat, and Deep Search return a configuration error.

Deep Search (`/search`) uses a tool-calling agent over the Bleve full-text index. Configure **Search provider/model** and **Deep search languages** in Settings.

## OCR setup

Add an OCR-capable provider in **Settings** (or seed via `.env` on first boot), then bind it under **Models**. OpenRouter OCR lists only models that advertise `file` input. Mistral OCR lists models whose ids contain `ocr`. Other SDKs show the full catalog with a warning to choose a file-capable model.

### Google Cloud Vision

Uses the official [Go client library](https://docs.cloud.google.com/vision/docs/detect-labels-image-client-libraries).

- **Images** — `BatchAnnotateImages` with `DOCUMENT_TEXT_DETECTION` via `images:annotate`
- **PDFs** — `BatchAnnotateFiles` via `files:annotate` (base64 upload, no Cloud Storage). Pages are processed in batches of up to 5 per request.

See [docs/google_vision.md](google_vision.md) for obtaining a Google API key.

### Mistral OCR

Uses the [Mistral Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr) when the provider is bound for OCR. Local files are sent as base64 data URLs (up to 10 MB). The same provider can be bound for extraction, chat, and search (chat completions, not this OCR endpoint).

- **PDFs and office documents** — `document_url` with a base64 data URL
- **Images** — `image_url` with a base64 data URL
- **Output** — page markdown joined into plain text

## Useful commands

```bash
# Frontend production build (SPA -> ../public, docs -> ../public/docs)
cd frontend && npm run build

# Create / update admin (PocketBase superuser + paired users account)
cd backend && go run . superuser upsert admin@example.com 'your-password'
```

## Paperless-ngx API compatibility

Lemmary exposes a paperless-ngx-compatible REST API on the same host as PocketBase (for example `http://127.0.0.1:8090/api/`). The backend implements the endpoints third-party clients expect for authentication, documents, tags, correspondents, document types, and related metadata.

Compatibility is intentionally partial: common read/write flows work, but not every paperless-ngx feature is available (for example, some list endpoints return empty stubs where Lemmary has no equivalent data).

### Connecting external clients

1. Point the client at your Lemmary server URL (scheme + host + port, no `/api` suffix — clients add that themselves).
2. Sign in with a PocketBase user account. The `/api/token/` endpoint accepts the same username and password as the web UI.
3. Clients that send `Authorization: Token <jwt>` (paperless-ngx style) are supported alongside standard Bearer tokens.

API versions 9 and 10 are accepted via the `Accept` header (`application/json; version=9`).

### Importing from Paperless-ngx

Any signed-in user can migrate a Paperless-ngx library into their own Lemmary account. The remote API token authenticates a specific ngx user, so the import runs as the current local user rather than as an admin.

1. Open **Import** in the More menu (or go to `/import`).
2. Enter the remote Paperless-ngx base URL and an API token from that instance’s profile.
3. Choose an import mode:
   - **Keep Paperless-ngx metadata** (`preserve`): upserts tags, correspondents, and document types by name; downloads each document with its OCR `content`, title, date, and taxonomy links. Preview and duplicate detection still run; AI metadata extraction is skipped so remote metadata is kept.
   - **Import files only and reprocess** (`reprocess`): downloads only the original files and queues the full OCR + AI pipeline as for a new upload.
4. Start the import. Exact file duplicates (same checksum) are skipped.

The same flow is available as `POST /api/app/import/ngx` with JSON body `{ "url": "...", "api_key": "...", "mode": "preserve" | "reprocess" }`. The request returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/import/ngx/status?job_id=...` until `status` is `completed` (with `result`) or `failed` (with `error`). Job state is kept in memory for the running process only. One import may run at a time per user. `mode` defaults to `preserve`. The API key is not persisted.

Import fetches only the caller-supplied URL. Private, loopback, and link-local destinations are blocked by default (including after redirects). Set `IMPORT_ALLOW_PRIVATE=1` if the remote Paperless-ngx instance is on a private network; cloud-metadata addresses remain blocked.

### swift-paperless (iOS)

[swift-paperless](https://github.com/paulgessinger/swift-paperless) is the main mobile client exercised against this API. Browsing documents, viewing details, and uploading generally work. Some paperless-ngx-specific settings or advanced features may be missing or no-ops because Lemmary does not implement the full paperless-ngx surface area.

## Troubleshooting

- **Stuck on setup wizard:** create the admin account and add an OCR provider plus an LLM provider (OpenAI, OpenRouter, or Mistral — or seed keys in `.env` before first boot / use `superuser upsert`). Clearing required keys later brings the config steps back for admins.
- **Upload succeeds but stays pending:** ensure the backend server is running; the worker starts with `serve`.
- **OCR fails:** configure the OCR provider and API key in Settings (or seed `.env` before first boot). For Google Vision, ensure the Vision API is enabled for your project.
- **AI extraction fails:** configure an LLM provider (OpenAI, OpenRouter, or Mistral) in Settings. Check the processing job error on the document detail page.
- **Settings page missing:** log in with the admin email (the account created at setup / `superuser upsert`). Regular non-admin users do not see Settings.
- **Auth errors in frontend:** delete PocketBase data dir (`backend/pb_data`) and restart to recreate collections, then reload the app. This also deletes the Bleve index (rebuilt on next boot).
- **Search misses a document:** wait for processing to finish, then retry. Admins can use **Management → Rebuild search index**, or delete `backend/pb_data/bleve` and restart.
