---
description: "Lemmary's Paperless-ngx-compatible REST API lets existing Paperless-ngx clients and apps work with it, and Lemmary can import the library of a running Paperless-ngx."
---

# Paperless-ngx API compatibility

Lemmary exposes a paperless-ngx-compatible REST API on the same host as PocketBase (for example `http://127.0.0.1:8090/api/`). The backend implements the endpoints third-party clients expect for authentication, documents, tags, correspondents, document types, and related metadata.

Compatibility is intentionally partial: common read/write flows work, but not every paperless-ngx feature is available (for example, some list endpoints return empty stubs where Lemmary has no equivalent data). `GET /api/documents/{id}/suggestions/` always answers with empty lists, since Lemmary's AI sets a document's metadata while processing it, and `GET /api/documents/{id}/preview/` serves the original file. A path Lemmary does not serve answers `404 {"detail": "Not found."}`.

## Document ids

Paperless-ngx addresses records by integer id; PocketBase uses 15-character strings. Lemmary stores an `ngx_id` alongside every document, tag, correspondent and document type, seeded from a hash of the PocketBase id so ids issued before this column existed keep pointing at the same records. It is unique per account, assigned on create, and never changes afterwards — clients cache it, and swift-paperless keys its thumbnail cache on a URL containing it.

Upgrading numbers the existing library in one migration. Two of an account's records that seed to the same value are resolved by giving the second one the next free id; before the column, the second was unreachable through the paperless API entirely.

## Document list filters

`GET /api/documents/` understands the filters clients actually send:

| Filter | Parameters |
| --- | --- |
| Full text | `query` |
| Title and content | `text`, `title_search`, `title_content`, `title__icontains`, `content__icontains` |
| Tags | `tags__id`, `tags__id__all`, `tags__id__in`, `tags__id__none`, `is_tagged` |
| Document type | `document_type__id`, `document_type__id__in`, `document_type__id__none`, `document_type__isnull` |
| Correspondent | `correspondent__id`, `correspondent__id__in`, `correspondent__id__none`, `correspondent__isnull` |
| Document date | `created__date__{gt,gte,lt,lte}`, `created__{gt,gte,lt,lte}`, `created__year` |
| Upload date | `added__date__{gt,gte,lt,lte}`, `added__{gt,gte,lt,lte}`, `added__year` |
| Last change | `modified__date__{gt,gte,lt,lte}`, `modified__{gt,gte,lt,lte}`, `modified__year` |
| Owner | `owner__id`, `owner__id__in`, `owner__id__none`, `owner__isnull` |
| Specific documents | `id`, `id__in` |
| Paging and shaping | `page`, `page_size`, `ordering`, `truncate_content`, `fields` |

Filters combine, and `count` always matches the filtered set, so paging through a filtered list is safe.

Two of those groups need a word on granularity and scope:

- **`added__{gt,gte,lt,lte}` and `modified__{gt,gte,lt,lte}` compare the whole instant**, so `added__gt=2025-06-15T10:00:00Z` returns uploads from later that same morning, and `modified__gt` is how clients such as Paperparrot fetch only what changed since their last sync. The `added__date__` forms compare the day, as does every `created` comparator: a document's own date carries no time of day. A document with no date of its own answers on the day it was uploaded, which is the date the client is shown for it.
- **Owner filters are answered, not applied.** Every document this API can return is one the caller owns or one another account has shared with them read-only, so naming them narrows nothing and naming anybody else matches nothing. A shared document is listed, fetched, downloaded and searched like your own, carries its owner's tags, correspondent and type, and refuses `PATCH` and `DELETE` with a `404`.

Three things behave differently from paperless-ngx, deliberately:

- **A filter Lemmary cannot honour is a `400`, not an unfiltered page.** Lemmary has no storage paths or archive serial numbers, and its [custom fields](/setup#custom-fields) are not exposed through this API (`/api/custom_fields/` lists none), so a request that filters on any of them is refused with `{"detail": "Unsupported filter \"…\"."}`. Returning a `200` that ignored the filter would be worse: the client renders it as though the filter had applied, so "documents tagged Invoice" silently becomes the whole archive.
- **Text search matches whole words, not substrings.** All six text filters, including `text` and `title_search`, which Paperless-ngx 3 matches as substrings, run through the same Bleve index as the web UI's search box, which is tokenised. Searching `rechn` matches `Rechnung` only as a *prefix*, and only when no document matches `rechn` as a whole word; `rechnung` matches directly. A term inside a word (`echnung`) finds nothing, and a term containing a digit is never used as a prefix.
- **A filtered text search enumerates at most 5000 matches.** Beyond that the reported `count` under-reports — consistently, so the paging links never point past what can be served.

Results are ranked by relevance when a text filter is present and the `ordering` is absent, `score`, `-score`, or a field this server does not sort on — which is what paperless-ngx does too. Any other `ordering` is served by the database. `ordering=id` sorts by the integer id the client was shown, and `ordering=created` by the same date the response reports. A list with no text filter and no recognised `ordering` comes back newest upload first.

## Connecting external clients

1. Point the client at your Lemmary server URL (scheme + host + port, no `/api` suffix — clients add that themselves). Some clients, Paperparrot among them, check the server with `GET /api/remote_version/` before signing in; it answers without credentials, as on Paperless-ngx.
2. Sign in with a PocketBase user account. The `/api/token/` endpoint accepts the same username and password as the web UI and returns a long-lived JWT (ten years). Paperless-ngx clients store that token and do not refresh it; this is not the five-day web UI session. Changing the account password invalidates it.
3. Clients that send `Authorization: Token <jwt>` (paperless-ngx style) are supported alongside standard Bearer tokens.
4. Clients that ask for a token instead of a password, such as Paperparrot, take one from **Account → Paperless-ngx apps** in the web UI. It is the same ten-year token, and the way in for an account that signs in with a passkey or OAuth and whose user does not know its password. Clients can mint one themselves with `POST /api/profile/generate_auth_token/`, as on paperless-ngx; unlike paperless-ngx, each call returns a new token and the earlier ones stay valid. Changing the password under **Account → Password** revokes every token at once, agent tokens included; there is no way to revoke one alone.

API versions 9 and 10 are accepted via the `Accept` header (`application/json; version=10`), the same pair Paperless-ngx 3 serves. As there, a request that names no version is answered as version 10, and every response carries `X-Api-Version: 10` and `X-Version: 3.0.0`, the release whose API this follows, so clients that gate features on the server version treat Lemmary as Paperless-ngx 3.0.

Documents carry Paperless-ngx 3's version fields, but Lemmary keeps one file per document: each one reports itself as a root (`root_document` is `null`) whose only entry in `versions` is itself. The version endpoints are not served.

## Tasks

`GET /api/tasks/` reports Lemmary's processing jobs as paperless tasks, newest first. Under API version 10 the list is paged and each task has Paperless-ngx 3's fields (`task_type`, `trigger_source`, `status`, `result_data`, `related_document_ids`), filtered by `status`, `is_complete`, `task_type`, `acknowledged` and `task_id`, and sorted by `ordering=date_created` or `date_done`. Under version 9 it is the older unpaged list, capped at 100. Every task is a `consume_file`, and since Lemmary does not record where an upload came from, `trigger_source` is always `web_ui`.

`GET /api/tasks/active/` and `GET /api/tasks/status_counts/` answer as on Paperless-ngx 3; `/api/tasks/summary/` and `POST /api/tasks/run/` are not served. `POST /api/acknowledge_tasks/` (and `/api/tasks/acknowledge/`, its name since paperless-ngx 2.14) dismisses tasks by id, or everything outstanding with `{"all": true}`.

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

Import fetches only the caller-supplied URL. Link-local and cloud-metadata destinations are always blocked, including after redirects. Private and loopback destinations are allowed, so a Paperless-ngx instance on your LAN or Docker network works; set `IMPORT_ALLOW_PRIVATE=0` to block them too when people you do not trust have accounts.

## swift-paperless (iOS)

[swift-paperless](https://github.com/paulgessinger/swift-paperless) is the main mobile client exercised against this API. Browsing documents, viewing details, searching, filtering, and uploading generally work. Some paperless-ngx-specific settings or advanced features may be missing or no-ops because Lemmary does not implement the full paperless-ngx surface area.

Opening the document list fetches 250 documents and then a thumbnail for every one of them, each as its own `GET /api/documents/{id}/thumb` — paperless-ngx has no batch thumbnail endpoint, and swift-paperless prefetches the whole page rather than the visible rows. That burst is expected, and it is a cold-cache cost: thumbnails are served with a 30-day `Cache-Control` and the app keeps its own on-disk cache keyed on the URL.

If the app starts returning 401 after working at add-server time, delete and re-add the server once so it can fetch a new token. Tokens issued before long-lived `/api/token/` JWTs expire after five days and cannot be extended in place.
