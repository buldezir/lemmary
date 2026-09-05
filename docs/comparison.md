# Lemmary vs Paperless-ngx vs Papra

All three products turn a self-hosted collection of files into a searchable
document archive. Lemmary goes further: it is designed to organize the archive
for you and turn it into a source you can question and research, without first
building an elaborate filing system.

- **Lemmary** is an AI-first personal archive. It automatically extracts rich
  metadata, can answer questions about one document, and can search, read, and
  synthesize across the whole archive—with links back to the sources.
- **Paperless-ngx** is a mature, scan-centered document management system. It
  has the broadest filing model, automation, document editing, permissions, and
  client ecosystem of the three, at the cost of more infrastructure and more
  hands-on organization.
- **Papra** is a minimal collaborative archive. Organizations, sharing, a
  public API, and flexible storage are central, while its AI feature is focused
  on automatic tagging rather than finding and answering from the archive.

This is a capability comparison, not a benchmark. “Built in” can still require
configuration, credentials, or an optional service. The snapshot was checked on
**September 5, 2026** against the linked project documentation; check the source
project before choosing on the strength of one feature.

## Feature comparison

| Area | Lemmary | Paperless-ngx | Papra |
| --- | --- | --- | --- |
| Primary fit | A document archive that organizes itself and supports cited, archive-wide research | A traditional document-management workflow for scanned and digital records | Simple document storage and collaboration |
| Self-hosting | A compact default footprint: one application container and one persistent volume; [PocketBase also supports S3-compatible document storage](/storage#document-files), and local OCR and embedding sidecars are optional | Docker Compose or bare metal; requires a Redis-compatible broker in addition to the app and supports SQLite, PostgreSQL, or MariaDB | One Docker image for a basic install; filesystem, S3-compatible, and Azure Blob storage are supported |
| Upload and intake | Web upload and a partial Paperless-ngx-compatible REST API; direct Paperless-ngx migration, dedicated Amazon order export, and multi-document PDF intake | Web/API upload, watched consumption folder, email accounts and rules | Web/API/CLI upload, watched organization folders, and inbound email |
| OCR and text extraction | Mistral OCR, Google Cloud Vision, an OpenAI-compatible file model, or a local Docling/PaddleOCR sidecar; native extraction for text and Office files | Local Tesseract OCR in 100+ languages, with optional remote Azure AI OCR; creates archival PDF/A beside the original | Internal extraction with Tesseract by default; Mistral OCR, Docling, Azure Document Intelligence, and custom HTTP extractors are also supported |
| Automatic organization | Every processed document can receive title, date, type, correspondent, purpose, tags, and a summary from the LLM automatically | Matching rules and a learned classifier assign tags, correspondents, types, and storage paths; an optional LLM suggests metadata that a user reviews | Rules can apply tags; an optional LLM can apply existing tags or create new ones, but does not extract the broader metadata Lemmary does |
| Metadata model | Title, date, type, correspondent, purpose, tags, summary, and editable OCR text | Tags, correspondents, document types, storage paths, archive serial numbers, notes, and typed custom fields | Tags, notes, and typed organization-level custom properties |
| Conventional search | Full-text search over OCR text and metadata, filters, and a date timeline—all available without an embedding model | Ranked full-text and advanced query syntax, filters, saved views, and a configurable dashboard | Full-text search, query filters, and custom-property search |
| AI search and chat | The most research-oriented workflow: **Deep Search** finds documents by keyword and, optionally, embeddings; **Research** reads and synthesizes across many documents with source links; **Ask AI** chats with one document | Optional embeddings enable similar-document retrieval and sourced chat over one document or the documents in the current view, but there is no separate multi-step research mode | No archive chat or semantic search is documented; current AI support is automatic tagging |
| PDF and version tools | Can manually split a multi-document PDF on intake or ask the model to propose cuts | Merge, split, rotate, delete, and rearrange pages; retain multiple file versions under one document | No equivalent PDF editor or document-version history is documented |
| People and sharing | Multiple accounts and per-user libraries; no public document links or shared workspaces | Users, groups, global and object-level permissions, ownership, expiring public links, bundles, and email sharing | Organization owners and members; external links can have an expiry and password |
| Authentication | Password, OAuth2 provider sign-in, and passkeys | Password plus configurable external authentication, including OIDC | Password and configurable OAuth2/OIDC providers |
| Automation and integrations | A complete asynchronous OCR-and-AI processing pipeline plus the compatible subset of the Paperless-ngx API for existing clients | Event-driven workflows, mail rules, webhooks, scripts, a comprehensive REST API, and a large third-party client ecosystem | Tagging rules, API keys with scopes, REST API, TypeScript SDK, CLI, and webhooks |
| Import, export, and recovery | The most direct move from Paperless-ngx, plus a single portable full-library zip containing originals, OCR, metadata, thumbnails, and taxonomy for backup and restore | Document exporter/importer for migration and backup | Storage migration tooling; operational backup requires preserving the configured database and document storage separately |
| Encryption at rest | The broadest built-in protection: the optional vault encrypts the database, document files, previews, and stored vectors; it boots locked and decrypts into RAM. PocketBase S3 file storage and the vault are alternative configurations because the vault refuses unencrypted external storage | No built-in at-rest encryption; upstream warns that documents are stored in clear text and recommends a trusted host | Optional AES-256-GCM encryption protects document files, but the database and its metadata remain outside that encryption boundary |
| License | [PolyForm Noncommercial 1.0.0](https://github.com/buldezir/lemmary/blob/main/LICENSE): source-available; commercial use requires a license | [GPL-3.0](https://github.com/paperless-ngx/paperless-ngx/blob/dev/LICENSE) | [AGPL-3.0](https://github.com/papra-hq/papra/blob/main/LICENSE) |

## Moving between them

Lemmary can pull originals and selected metadata directly from Paperless-ngx.
Its `/api/` surface also implements a useful subset of the Paperless-ngx REST
API, so compatible mobile clients can browse and upload, but Lemmary is **not a
drop-in Paperless-ngx server**: workflows, permissions, saved views, custom
fields, and many endpoints are outside that compatibility layer. See
[Paperless-ngx API compatibility](/setup#paperless-ngx-api-compatibility).

There is no direct Papra importer. Export the original files from Papra and
upload them to Lemmary; expect to recreate tags and other metadata unless you
write a migration against the two APIs.

## Sources

Lemmary capabilities are described in this documentation, especially
[Setup](/setup), [AI providers](/ai_providers), [Self-hosting](/self_hosting),
and [Encryption at rest](/encryption).

For the other projects, the comparison uses their official repositories and
documentation:

- Paperless-ngx: [project and license](https://github.com/paperless-ngx/paperless-ngx),
  [feature overview](https://docs.paperless-ngx.com/),
  [usage, intake, permissions, sharing, and workflows](https://docs.paperless-ngx.com/usage/),
  [AI and matching](https://docs.paperless-ngx.com/advanced_usage/), and
  [REST API](https://docs.paperless-ngx.com/api/).
- Papra: [project and license](https://github.com/papra-hq/papra),
  [configuration and extraction providers](https://docs.papra.app/self-hosting/configuration/),
  [AI auto-tagging](https://docs.papra.app/guides/auto-tagging/),
  [document encryption](https://docs.papra.app/guides/document-encryption/), and
  [API](https://docs.papra.app/resources/api-endpoints/).
