# Lemmary vs Paperless-ngx vs Papra

All three products turn a self-hosted collection of files into a searchable
document archive. Lemmary goes further: it is designed to organize the archive
for you and turn it into a source you can question and research, without first
building an elaborate filing system.

- **Lemmary** is an AI-first personal archive. It automatically extracts rich
  metadata, can answer questions about one document, and can search, read, and
  synthesize across the whole archive—with links back to the sources. Agents
  can search and read the same archive over MCP, and change it where an admin
  allows.
- **Paperless-ngx** is a mature, scan-centered document management system. It
  has the broadest filing model, automation, document editing, permissions, and
  client ecosystem of the three, at the cost of more infrastructure and more
  hands-on organization.
- **Papra** is a minimal collaborative archive. Organizations, sharing, a
  public API, and flexible storage are central, while its AI feature is focused
  on automatic tagging rather than finding and answering from the archive.

This is a capability comparison, not a benchmark. “Built in” can still require
configuration, credentials, or an optional service. All three columns were
checked on **October 7, 2026**: Lemmary against this documentation,
Paperless-ngx against v3.3.0, and Papra against 26.7.0 and the linked project
documentation. Check the source project before choosing on the strength of one
feature.

## Feature comparison

| Area | Lemmary | Paperless-ngx | Papra |
| --- | --- | --- | --- |
| Primary fit | A document archive that organizes itself and supports cited, archive-wide research | A traditional document-management workflow for scanned and digital records | Simple document storage and collaboration |
| Self-hosting | A compact default footprint: one application container and one persistent volume; [PocketBase also supports S3-compatible document storage](/storage#document-files), and local OCR and embedding sidecars are optional, in Docker or [natively on an Apple Silicon Mac](/local_ai_macos) | Docker Compose or bare metal; requires a Redis-compatible broker in addition to the app and supports SQLite, PostgreSQL, or MariaDB | One Docker image for a basic install; filesystem, S3-compatible, and Azure Blob storage are supported |
| Upload and intake | Web upload of files, folders and zip archives; a [watched consume folder](/setup#ingest-folder) whose subfolders become tags; [IMAP mailbox attachments](/setup#ingest-from-imap); [direct scanning from AirScan/eSCL network scanners](/scanning); a partial Paperless-ngx-compatible REST API; direct Paperless-ngx and Papra migration, dedicated Amazon order export, and multi-document PDF intake | Web/API upload, watched consumption folder, email accounts and rules | Web/API/CLI upload, watched organization folders, and inbound email |
| OCR and text extraction | Mistral OCR, Google Cloud Vision, an OpenAI-compatible or Anthropic file model, a [ChatGPT subscription](/chatgpt_login), or a [local Docling sidecar](/local_ocr) (RapidOCR, EasyOCR, Tesseract, or Apple Vision on a Mac); native extraction for text and Office files; with a result language set, the OCR text can be read translated on demand | Local Tesseract OCR in 100+ languages, with optional remote Azure AI OCR; creates an archival PDF/A beside the original, by default skipping born-digital PDFs that already contain text | Internal extraction with Tesseract by default; Mistral OCR, Docling, Azure Document Intelligence, and custom HTTP extractors are also supported |
| Automatic organization | Every processed document can receive title, date, type, correspondent, and a summary from the LLM automatically, plus tags chosen from a list you curate — the LLM assigns them but never invents new ones. Admin-written house rules steer extraction; documents held for review get up to three suggested new tags to accept; a new tag can be assigned with AI across documents processed before it existed; with [related linking](/setup#related-documents) on, documents are linked to others that share a reference number or come close in meaning | Matching rules and a learned classifier assign tags, correspondents, types, and storage paths; an optional LLM suggests metadata for review, or a workflow action applies it automatically and can create missing tags, correspondents, and types | Rules can apply tags; an optional LLM can apply existing tags or create new ones, but does not extract the broader metadata Lemmary does |
| Metadata model | Title, date, type, correspondent, coloured tags, summary, editable OCR text, admin-defined text, number, date and choice [custom fields](/setup#custom-fields) that extraction fills, with one-click presets for common paperwork, and two-way [links between related documents](/setup#related-documents) | Tags, correspondents, document types, storage paths, archive serial numbers, notes, and typed custom fields, including two-way document links | Tags, notes, and typed organization-level custom properties, including links to other documents |
| Conventional search | Full-text search over OCR text, metadata, and custom-field values with prefix matching, quoted exact phrases, filters (including all-of tags, no tags, and no date), a date timeline, and a review inbox—all available without an embedding model | Ranked full-text search with advanced query syntax and optional fuzzy matching, “more like this” similar documents, filters, saved views, and a configurable dashboard | Full-text search, query filters, custom-property search, and saved searches |
| AI search and chat | The most research-oriented workflow: **AI assisted search** finds documents by keyword and, optionally, embeddings, and with an embedding model the Documents search box also finds documents by meaning; **Deep Research** reads and synthesizes across many documents with source links, on an optional separate Advanced model, and its chats can branch; **Ask AI** chats with one document. Both can [search the web](/ai_providers#the-web-search-provider) when a reader turns it on, and a chat can switch model. Language models run on API keys (OpenAI, Anthropic, OpenRouter, Mistral, Opencode Go) or a ChatGPT subscription; embeddings can run on [your own hardware](/local_embeddings) | Optional embeddings ground the LLM's metadata suggestions and a sourced chat over one document or every document a user can see, but there is no separate multi-step research mode | No archive chat or semantic search is documented; current AI support is automatic tagging |
| PDF and version tools | Can manually split a multi-document PDF on intake or ask the model to propose cuts | Merge, split, rotate, delete, and rearrange pages; retain multiple file versions under one document | No equivalent PDF editor or document-version history is documented |
| People and sharing | Multiple accounts and per-user libraries; one document can be shared read-only with other accounts on the same instance; no public document links or shared workspaces | Users, groups, global and object-level permissions, ownership, expiring public links, bundles, and email sharing | Organizations with owner, admin, and member roles; share links can have an expiry and password |
| Authentication | Password, OAuth2 provider sign-in, and passkeys; no TOTP two-factor | Password with optional TOTP two-factor, OIDC and social sign-in, or a reverse-proxy header; password sign-in can be turned off; no passkeys | Password, GitHub and Google sign-in, configurable OAuth2/OIDC providers, and optional TOTP two-factor; no passkeys |
| Interface languages | English, German, and Russian, across the interface, API errors, and this documentation | About 37, translated on Crowdin | 17 in the web app |
| Automation and integrations | A complete asynchronous OCR-and-AI processing pipeline, an [MCP server](/mcp) so agents can search and read the archive, and change it where an admin allows, the compatible subset of the Paperless-ngx API for existing clients, and optional Prometheus metrics | Event-driven workflows, mail rules, webhooks, scripts, a comprehensive REST API, and a large third-party client ecosystem | Tagging rules, API keys with scopes, REST API, TypeScript SDK, CLI, and webhooks |
| Import, export, and recovery | The most direct move from Paperless-ngx or Papra—Lemmary pulls from a running instance over its API—plus a portable zip of the full library or of a filtered selection, containing originals, OCR, metadata, thumbnails, and taxonomy, for backup and restore | Whole-instance document exporter/importer for migration and backup; selected documents download as a zip of files without metadata; a 30-day trash | A CLI importer for Paperless-ngx exports and storage migration tooling; no library export, so backup means preserving the configured database and document storage separately |
| Encryption at rest | The broadest built-in protection: the optional vault encrypts the database, document files, previews, and stored vectors; it boots locked and decrypts into RAM. PocketBase S3 file storage and the vault are alternative configurations because the vault refuses unencrypted external storage | No built-in at-rest encryption; upstream warns that documents are stored in clear text and recommends a trusted host | Optional AES-256-GCM encryption of document files with key rotation, and a separate optional database encryption key; both are off by default, and with file encryption alone the extracted text and metadata stay in plain text |
| License | [PolyForm Noncommercial 1.0.0](https://github.com/buldezir/lemmary/blob/main/LICENSE): source-available; commercial use requires a license | [GPL-3.0](https://github.com/paperless-ngx/paperless-ngx/blob/dev/LICENSE) | [AGPL-3.0](https://github.com/papra-hq/papra/blob/main/LICENSE) |

## Moving between them

Lemmary can pull originals and selected metadata directly from Paperless-ngx.
Its `/api/` surface also implements a useful subset of the Paperless-ngx REST
API, so compatible mobile clients can browse and upload, but Lemmary is **not a
drop-in Paperless-ngx server**: workflows, permissions, saved views, custom
fields, and many endpoints are outside that compatibility layer. See
[Paperless-ngx API compatibility](/paperless_ngx).

Lemmary can also pull originals, tags, dates, and extracted text from every
Papra organization an API key can read; Papra notes and custom properties stay
behind. See [Importing from Papra](/setup#importing-from-papra).

## Sources

Lemmary capabilities are described in this documentation, especially
[Setup](/setup), [AI providers](/ai_providers), [Deep Research](/deep_research),
[Self-hosting](/self_hosting), [MCP](/mcp), [Scanning](/scanning), and
[Encryption at rest](/encryption).

For the other projects, the comparison uses their official repositories and
documentation:

- Paperless-ngx: [project and license](https://github.com/paperless-ngx/paperless-ngx),
  [release notes](https://github.com/paperless-ngx/paperless-ngx/releases),
  [feature overview](https://docs.paperless-ngx.com/),
  [usage, intake, permissions, sharing, and workflows](https://docs.paperless-ngx.com/usage/),
  [AI and matching](https://docs.paperless-ngx.com/advanced_usage/),
  [configuration](https://docs.paperless-ngx.com/configuration/), and
  [REST API](https://docs.paperless-ngx.com/api/).
- Papra: [project and license](https://github.com/papra-hq/papra),
  [release notes](https://github.com/papra-hq/papra/releases),
  [configuration and extraction providers](https://docs.papra.app/self-hosting/configuration/),
  [AI auto-tagging](https://docs.papra.app/guides/auto-tagging/),
  [document encryption](https://docs.papra.app/guides/document-encryption/), and
  [API](https://docs.papra.app/resources/api-endpoints/).
