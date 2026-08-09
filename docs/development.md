# Development Guide

## Prerequisites

- Go 1.23+
- Node.js 20+
- npm
- [poppler-utils](https://poppler.freedesktop.org/) (`pdftoppm`) for PDF preview thumbnails

On macOS: `brew install poppler`. On Debian/Ubuntu: `apt install poppler-utils`.

## Running locally

```bash
cp .env.example .env
```

### 1. Start PocketBase backend

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
- `outbound_emails` (outbound mail log when SMTP is not configured; superuser-only)

### 2. Start React frontend

```bash
cd frontend
npm install
npm run dev
```

This starts the app on `http://127.0.0.1:5173` and a VitePress preview of these docs on `http://127.0.0.1:5174/docs/`. After `npm run build`, the same docs are static files under `public/docs/` and available at `/docs/` when PocketBase serves `public/`.

The frontend auto-logs in a regular `users` account when `VITE_DEV_*` is set.

## Environment variables

All variables live in `.env` at the project root (see `.env.example`).

### Always env-backed

| Variable | Default | Description |
| --- | --- | --- |
| `WORKER_CRON_EXPR` | `* * * * *` | Cron expression for sweeping stuck pending jobs (registered once at startup) |
| `VITE_POCKETBASE_URL` | `http://127.0.0.1:8090` | PocketBase API URL (frontend) |
| `VITE_DEV_USER_EMAIL` | — | Dev auto-login email (`users` collection) |
| `VITE_DEV_USER_PASSWORD` | — | Dev auto-login password |

### Seed-only (first boot → Settings)

These seed `app_settings` when the singleton record does not exist yet. After that, edit them in the app **Settings** page (requires a PocketBase superuser login). Changing `.env` alone will not update a running install.

| Variable | Default | Description |
| --- | --- | --- |
| `OCR_PROVIDER` | `google_vision` | OCR provider (`google_vision`, `mistral`) |
| `GOOGLE_VISION_API_KEY` | empty | Google Cloud Vision API key |
| `MISTRAL_API_KEY` | empty | Mistral API key |
| `MISTRAL_OCR_MODEL` | `mistral-ocr-latest` | Mistral OCR model |
| `MISTRAL_API_BASE_URL` | `https://api.mistral.ai/v1` | Mistral API base URL |
| `OCR_TIMEOUT_SEC` | `40` | OCR request timeout |
| `PROCESSING_RESULT_LANGUAGE` | empty | ISO 639-1 code (e.g. `en`, `de`). When set, `title`, `summary`, `purpose`, and `document_type` are stored in this language; originals go in `*_original` fields. |
| `OPENAI_API_KEY` | empty | OpenAI-compatible API key |
| `OPENAI_MODEL` | `gpt-4o-mini` | Model ID for metadata extraction |
| `OPENAI_CHAT_MODEL` | `OPENAI_MODEL` | Optional model ID for document chat |
| `OPENAI_SEARCH_MODEL` | `OPENAI_CHAT_MODEL` | Optional model ID for Deep Search |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible API base URL |
| `OPENAI_TIMEOUT_SEC` | `60` | AI request timeout |
| `DEEP_SEARCH_LANGUAGES` | empty | Comma-separated ISO 639-1 codes (e.g. `de,en,uk`) for Deep Search keyword expansion |
| `WORKER_TIMEOUT_SEC` | `300` | Per-job processing timeout |
| `WORKER_MAX_RETRIES` | `0` | Max step retry attempts before a job fails |
| `EXTRACTION_PROMPT_VERSION` | `v1` | Stored on each processing job step run |

## First-launch setup wizard

On a fresh install the SPA hard-gates until setup is complete:

1. **Create admin** — email + password. Creates a PocketBase `_superusers` account **and** a matching `users` account (same credentials) so the admin can own documents. Replaces PocketBase’s browser installer UI.
2. **OCR** — provider + matching API key.
3. **OpenAI** — API key for extraction, chat, and Deep Search.

You can also create the admin via CLI (`go run . superuser upsert EMAIL PASS` from `backend/`; this also upserts the paired `users` account) and/or seed OCR/AI keys in `.env` before first boot; the wizard skips steps that are already done. Until keys are present, regular users see a “setup incomplete” screen; only an admin can finish configuration.

## Settings (admin UI)

1. Sign in with the **admin** email/password (login prefers the `users` account; legacy `_superusers`-only installs are linked automatically via `/api/app/ensure-user`, which sets a hidden `is_app_admin` flag on the paired `users` record).
2. Open **Settings** in the nav (shown when `/api/app/me` reports `is_admin`). Changes save to `app_settings` and hot-reload the in-process OCR/AI clients (no restart).

`WORKER_CRON_EXPR` is not editable there; change `.env` and restart, or use PocketBase Admin → Settings → Crons.

## Outbound mail (SMTP / `outbound_emails`)

Configure SMTP under PocketBase Admin → Settings → Mail. When SMTP is **disabled** (the default), PocketBase would normally fall back to local `sendmail`. This app replaces that fallback: messages are written to the `outbound_emails` collection instead (password reset, verification, OTP, auth alerts, etc.).

Browse them in PocketBase Admin as a superuser. Enable SMTP when you want real delivery; the DB sink is skipped while SMTP is on.

## Processing flow

1. User uploads a document from `/upload`
2. PocketBase stores the file and creates a `processing_jobs` record via Go hook
3. An `OnRecordAfterCreateSuccess` hook dispatches the job immediately; a cron job (`process_pending_jobs`) sweeps any stuck pending jobs
4. Worker generates a PNG preview from the first PDF page (via `pdftoppm`), then extracts text, optionally checks for near-duplicates, and runs AI metadata extraction
5. Extracted metadata is saved on the document
6. UI shows status on list and detail pages

### Duplicate detection

- **Exact duplicates** — on create, the uploaded file is hashed (SHA-256) into `documents.checksum`. A second upload with the same checksum for the same user is **rejected**, with an error pointing at the existing document id. Uniqueness is enforced with a per-user unique index on non-empty checksums so concurrent uploads cannot both succeed.
- **Near-duplicates (optional)** — after OCR, a `detect_duplicates` step can compare normalized OCR text (SimHash + Jaccard). This is controlled by Settings → **Enable near-duplicate detection after OCR** (off by default). Matches are marked `needs_review` with `duplicate_of` set to the earlier document (never a newer one); AI extract/apply steps are skipped.
- **Bulk scan** — Settings → **Scan for duplicates** (admin) backfills missing checksums/fingerprints and marks exact (and, if enabled, near) duplicates among existing documents.

Text extraction:

- **PDF and images** — configured OCR provider (Google Vision or Mistral)
- **TXT, CSV, DOCX, XLSX** — native parsers (no OCR API call); preview is skipped for these formats

Cron jobs are visible and manually triggerable in PocketBase Admin → Settings → Crons.

## OpenAI setup

Prefer **Settings** in the UI (admin). For a fresh install you can also put `OPENAI_API_KEY` (and optional model/base URL) in `.env` so they seed `app_settings` on first boot.

Without an API key, AI extraction, document chat, and Deep Search return a configuration error.

Deep Search (`/search`) uses a tool-calling agent over keyword search. Configure **Search model** and **Deep search languages** in Settings.

## OCR setup

Set the provider and API keys in **Settings** (or seed via `.env` on first boot).

### Google Cloud Vision

Uses the official [Go client library](https://docs.cloud.google.com/vision/docs/detect-labels-image-client-libraries).

- **Images** — `BatchAnnotateImages` with `DOCUMENT_TEXT_DETECTION` via `images:annotate`
- **PDFs** — `BatchAnnotateFiles` via `files:annotate` (base64 upload, no Cloud Storage). Pages are processed in batches of up to 5 per request.

See [docs/google_vision.md](google_vision.md) for obtaining a Google API key.

### Mistral OCR

Uses the [Mistral Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr). Local files are sent as base64 data URLs (up to 10 MB).

- **PDFs and office documents** — `document_url` with a base64 data URL
- **Images** — `image_url` with a base64 data URL
- **Output** — page markdown joined into plain text

## Useful commands

```bash
# Unit tests (exclude API e2e package)
cd backend && go test $(go list ./... | grep -v /e2e) -count=1

# API e2e — boots a temp PocketBase with mocked OCR/OpenAI
cd backend && go test ./e2e/ -count=1 -timeout 10m

# Browser e2e — builds SPA, starts cmd/e2eserver, runs Playwright
cd frontend && npm run test:e2e
# First time only:
cd frontend && npx playwright install chromium

# Full agent verification stack
./scripts/test-all.sh

# Frontend production build (SPA -> ../public, docs -> ../public/docs)
cd frontend && npm run build

# Create a new migration
cd backend && go run . migrate create "your_migration_name"

# Create / update admin (PocketBase superuser + paired users account)
cd backend && go run . superuser upsert admin@example.com 'your-password'
```

### E2E notes

- API and browser e2e use **mocked** Mistral OCR and OpenAI-compatible APIs (no real keys).
- Browser e2e serves the built SPA from `public/` on `http://127.0.0.1:18090` via [`backend/cmd/e2eserver`](../backend/cmd/e2eserver).
- Seeded accounts: `e2e@paperless.local` / `e2epassword123` (regular user) and `admin@paperless.local` / `adminpassword123` (paired admin: `_superusers` + `users`).
- `go test ./...` from `backend/` includes the e2e package.

## Paperless-ngx API compatibility

Paperless Go exposes a paperless-ngx-compatible REST API on the same host as PocketBase (for example `http://127.0.0.1:8090/api/`). The backend implements the endpoints third-party clients expect for authentication, documents, tags, correspondents, document types, and related metadata.

Compatibility is intentionally partial: common read/write flows work, but not every paperless-ngx feature is available (for example, some list endpoints return empty stubs where the MVP has no equivalent data).

### Connecting external clients

1. Point the client at your Paperless Go server URL (scheme + host + port, no `/api` suffix — clients add that themselves).
2. Sign in with a PocketBase user account. The `/api/token/` endpoint accepts the same username and password as the web UI.
3. Clients that send `Authorization: Token <jwt>` (paperless-ngx style) are supported alongside standard Bearer tokens.

API versions 9 and 10 are accepted via the `Accept` header (`application/json; version=9`).

### swift-paperless (iOS)

[swift-paperless](https://github.com/paulgessinger/swift-paperless) is the main mobile client exercised against this API. Browsing documents, viewing details, and uploading generally work. Some paperless-ngx-specific settings or advanced features may be missing or no-ops because Paperless Go does not implement the full paperless-ngx surface area.

## Troubleshooting

- **Stuck on setup wizard:** create the admin account and provide OCR + OpenAI API keys (or seed keys in `.env` before first boot / use `superuser upsert`). Clearing required keys later brings the config steps back for admins.
- **Upload succeeds but stays pending:** ensure the backend server is running; the worker starts with `serve`.
- **OCR fails:** configure the OCR provider and API key in Settings (or seed `.env` before first boot). For Google Vision, ensure the Vision API is enabled for your project.
- **AI extraction fails:** configure OpenAI settings in Settings. Check the processing job error on the document detail page.
- **Settings page missing:** log in with the admin email (the account created at setup / `superuser upsert`). Regular non-admin users do not see Settings.
- **Auth errors in frontend:** delete PocketBase data dir (`backend/pb_data`) and restart to recreate collections, then reload the app.
