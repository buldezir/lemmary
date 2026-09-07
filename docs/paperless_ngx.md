# Paperless-ngx API compatibility

Lemmary exposes a paperless-ngx-compatible REST API on the same host as PocketBase (for example `http://127.0.0.1:8090/api/`). The backend implements the endpoints third-party clients expect for authentication, documents, tags, correspondents, document types, and related metadata.

Compatibility is intentionally partial: common read/write flows work, but not every paperless-ngx feature is available (for example, some list endpoints return empty stubs where Lemmary has no equivalent data).

## Document ids

Paperless-ngx addresses records by integer id; PocketBase uses 15-character strings. Lemmary stores an `ngx_id` alongside every document, tag, correspondent and document type, seeded from a hash of the PocketBase id so ids issued before this column existed keep pointing at the same records. It is unique per account, assigned on create, and never changes afterwards — clients cache it, and swift-paperless keys its thumbnail cache on a URL containing it.

Upgrading numbers the existing library in one migration. Two of an account's records that seed to the same value are resolved by giving the second one the next free id; before the column, the second was unreachable through the paperless API entirely.

## Document list filters

`GET /api/documents/` understands the filters clients actually send:

| Filter | Parameters |
| --- | --- |
| Full text | `query` |
| Title and content | `title_content`, `title__icontains`, `content__icontains` |
| Tags | `tags__id`, `tags__id__all`, `tags__id__in`, `tags__id__none`, `is_tagged` |
| Document type | `document_type__id`, `document_type__id__in`, `document_type__id__none`, `document_type__isnull` |
| Correspondent | `correspondent__id`, `correspondent__id__in`, `correspondent__id__none`, `correspondent__isnull` |
| Document date | `created__date__{gt,gte,lt,lte}`, `created__{gt,gte,lt,lte}`, `created__year` |
| Upload date | `added__date__{gt,gte,lt,lte}`, `added__{gt,gte,lt,lte}`, `added__year` |
| Owner | `owner__id`, `owner__id__in`, `owner__id__none`, `owner__isnull` |
| Specific documents | `id`, `id__in` |
| Paging and shaping | `page`, `page_size`, `ordering`, `truncate_content`, `fields` |

Filters combine, and `count` always matches the filtered set, so paging through a filtered list is safe.

Two of those groups need a word on granularity and scope:

- **`added__{gt,gte,lt,lte}` compare the whole instant**, so `added__gt=2025-06-15T10:00:00Z` returns uploads from later that same morning. The `added__date__` forms compare the day, as does every `created` comparator: a document's own date carries no time of day. A document with no date of its own answers on the day it was uploaded, which is the date the client is shown for it.
- **Owner filters are answered, not applied.** Every document this API can return belongs to the caller, so naming them narrows nothing and naming anybody else matches nothing.

Three things behave differently from paperless-ngx, deliberately:

- **A filter Lemmary cannot honour is a `400`, not an unfiltered page.** Lemmary has no storage paths, custom fields, or archive serial numbers, so a request that filters on them is refused with `{"detail": "Unsupported filter \"…\"."}`. Returning a `200` that ignored the filter would be worse: the client renders it as though the filter had applied, so "documents tagged Invoice" silently becomes the whole archive.
- **Text search matches whole words, not substrings.** All four text filters run through the same Bleve index as the web UI's search box, which is tokenised. Searching `rechn` will not find `Rechnung`; searching `rechnung` will.
- **A filtered text search enumerates at most 5000 matches.** Beyond that the reported `count` under-reports — consistently, so the paging links never point past what can be served.

Results are ranked by relevance when a text filter is present and the `ordering` is absent, `score`, `-score`, or a field this server does not sort on — which is what paperless-ngx does too. Any other `ordering` is served by the database. `ordering=id` sorts by the integer id the client was shown, and `ordering=created` by the same date the response reports. A list with no text filter and no recognised `ordering` comes back newest upload first.

## Connecting external clients

1. Point the client at your Lemmary server URL (scheme + host + port, no `/api` suffix — clients add that themselves).
2. Sign in with a PocketBase user account. The `/api/token/` endpoint accepts the same username and password as the web UI and returns a long-lived JWT (ten years). Paperless-ngx clients store that token and do not refresh it; this is not the five-day web UI session. Changing the account password invalidates it.
3. Clients that send `Authorization: Token <jwt>` (paperless-ngx style) are supported alongside standard Bearer tokens.

API versions 9 and 10 are accepted via the `Accept` header (`application/json; version=9`).

## Tasks

`GET /api/tasks/` reports Lemmary's processing jobs as paperless tasks, newest first, capped at 100 per response. `POST /api/acknowledge_tasks/` (and `/api/tasks/acknowledge/`, its name since paperless-ngx 2.14) dismisses them by id.

Acknowledgement is stored in an `ngx_acknowledged` column on `processing_jobs` that only this API reads or writes — Lemmary's own UI shows processing state on the document and has no notion of dismissing it. `acknowledged=true` and `acknowledged=false` filter on it; omitting the parameter returns both.

## Importing from Paperless-ngx

Any signed-in user can migrate a Paperless-ngx library into their own Lemmary account. The remote API token authenticates a specific ngx user, so the import runs as the current local user rather than as an admin.

1. Open **Import** in the More menu, then the **Paperless-ngx** tab (or go to `/import/ngx`).
2. Enter the remote Paperless-ngx base URL and an API token from that instance’s profile.
3. Choose an import mode:
   - **Keep Paperless-ngx metadata** (`preserve`): upserts tags, correspondents, and document types by name; downloads each document with its OCR `content`, title, date, and taxonomy links. Preview and duplicate detection still run; AI metadata extraction is skipped so remote metadata is kept.
   - **Import files only and reprocess** (`reprocess`): downloads only the original files and queues the full OCR + AI pipeline as for a new upload.
4. Start the import. Exact file duplicates (same checksum) are skipped.

The same flow is available as `POST /api/app/import/ngx` with JSON body `{ "url": "...", "api_key": "...", "mode": "preserve" | "reprocess" }`. The request returns `202 Accepted` with `{ "job_id", "status": "running" }`. Poll `GET /api/app/import/ngx/status?job_id=...` until `status` is `completed` (with `result`) or `failed` (with `error`). Job state is kept in memory for the running process only. One import may run at a time per user. `mode` defaults to `preserve`. The API key is not persisted.

Import fetches only the caller-supplied URL. Private, loopback, and link-local destinations are blocked by default (including after redirects). Set `IMPORT_ALLOW_PRIVATE=1` if the remote Paperless-ngx instance is on a private network; cloud-metadata addresses remain blocked.

## swift-paperless (iOS)

[swift-paperless](https://github.com/paulgessinger/swift-paperless) is the main mobile client exercised against this API. Browsing documents, viewing details, searching, filtering, and uploading generally work. Some paperless-ngx-specific settings or advanced features may be missing or no-ops because Lemmary does not implement the full paperless-ngx surface area.

Opening the document list fetches 250 documents and then a thumbnail for every one of them, each as its own `GET /api/documents/{id}/thumb` — paperless-ngx has no batch thumbnail endpoint, and swift-paperless prefetches the whole page rather than the visible rows. That burst is expected, and it is a cold-cache cost: thumbnails are served with a 30-day `Cache-Control` and the app keeps its own on-disk cache keyed on the URL.

If the app starts returning 401 after working at add-server time, delete and re-add the server once so it can fetch a new token. Tokens issued before long-lived `/api/token/` JWTs expire after five days and cannot be extended in place.
