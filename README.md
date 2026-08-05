# Paperless Go

Document storage MVP built with Go + PocketBase and a React + TanStack Router frontend. Upload documents, run OCR, extract metadata with an OpenAI-compatible AI provider, and review results in the UI.

## Paperless-ngx compatibility

Paperless Go implements a [paperless-ngx](https://github.com/paperless-ngx/paperless-ngx)-compatible REST API under `/api/`, so you can use third-party clients instead of (or alongside) the built-in web UI. Coverage is partial — document list/upload/download, tags, and metadata generally work, but not every paperless-ngx endpoint or feature is implemented.

The API has been tested with the [swift-paperless](https://github.com/paulgessinger/swift-paperless) iOS app and mostly works for browsing and uploading documents. See [docs/development.md](docs/development.md#paperless-ngx-api-compatibility) for connecting external clients.

## Stack

- **Backend:** Go, [PocketBase as a framework](https://pocketbase.io/docs/use-as-framework/)
- **Frontend:** React, TanStack Router, PocketBase JS SDK
- **OCR:** Google Cloud Vision (`google_vision`) or Mistral AI OCR (`mistral`), configured in Settings
- **AI:** OpenAI-compatible chat completions via the official OpenAI Go SDK
- **Deep Search:** natural-language archive search via a tool-calling agent (keyword expansion across configured languages)

## Project layout

```text
backend/    PocketBase app, migrations, OCR/AI worker
frontend/   React UI
docs/       Development guide
```

## Quick start

```bash
cp .env.example .env
# Optional: seed OCR/AI keys in .env for first boot (skips those wizard steps)
docker compose up --build
```

Open [http://127.0.0.1:8090](http://127.0.0.1:8090). On first launch, the in-app setup wizard creates your admin account and collects OCR + OpenAI API keys (hard gate until both are set). Data is stored in a Docker volume (`app_data`).

For local development without Docker, see [docs/development.md](docs/development.md).

## Environment variables and Settings

See [docs/development.md](docs/development.md) for the full list.

- `WORKER_CRON_EXPR` and frontend `VITE_*` vars stay in `.env`
- OCR/AI keys, models, and worker timeouts live in the DB (`app_settings`); seed from `.env` on first boot, complete via the first-launch wizard, then edit in **Settings** as a PocketBase superuser

## Features

- Upload PDF, image, or plain text documents
- Async processing jobs with status tracking
- OCR text extraction
- AI metadata extraction: title, purpose, date, type, tags, summary
- Document list with search and status filters
- Deep Search chat (`/search`) with optional multi-step refine mode
- Detail page for reviewing OCR text and correcting metadata
- Superuser Settings page for runtime OCR/AI/worker config
- First-launch setup wizard (admin account + required OCR/AI keys)

### Screenshots

Documents list with AI-extracted titles, summaries, and tags:

![Documents](docs/screenshots/documents.png)

Document detail with editable metadata, summary, and OCR text:

![Document detail](docs/screenshots/document-detail.png)

Deep Search with natural-language queries:

![Deep Search](docs/screenshots/deep-search.png)

## Tests

```bash
# Unit only (fast)
cd backend && go test $(go list ./... | grep -v /e2e) -count=1

# API e2e (real PocketBase + mocked OCR/AI)
cd backend && go test ./e2e/ -count=1 -timeout 10m

# Browser e2e (builds SPA, starts e2e server with mocks, Playwright)
cd frontend && npm run test:e2e

# Full verification (agents)
./scripts/test-all.sh
```

First browser run may need Chromium: `cd frontend && npx playwright install chromium`.
