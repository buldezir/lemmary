# Storage

Lemmary stores durable data in three places: SQLite, document file storage, and
SQLite-backed embeddings. The search indexes are disposable copies built from
that durable data.

| Data | Default location | With S3 file storage |
| --- | --- | --- |
| Database | `/app/pb_data/data.db` | Stays local |
| Original files and thumbnails | `/app/pb_data/storage/` | Stored in the configured S3-compatible bucket |
| Embedding vectors and their state | Tables inside `data.db` | Stay local |
| Full-text and vector indexes | `/app/pb_data/bleve/` | Stay local |

## Database

PocketBase uses SQLite. `data.db` contains document metadata, extracted OCR
text, tags and other taxonomy, users, settings, processing jobs, saved chats,
and embedding vectors. Back up the database together with the document files;
neither is a complete archive by itself.

## Document files

By default, PocketBase writes uploaded originals and generated thumbnails under
`pb_data/storage/`. In Docker, the `app_data` volume persists that directory and
the database together.

PocketBase can instead store those files in an S3-compatible service. Sign in
to the PocketBase admin UI at `/_/`, open **Settings → Files storage**, and
enable S3 with the bucket, region or endpoint, access key, and secret for the
service. Only record files move to S3: keep the local `pb_data` volume because
it still holds SQLite and the search indexes.

S3 file storage and [Lemmary's encrypted vault](/encryption) are alternative
configurations. The vault refuses to start with PocketBase S3 storage enabled,
because files written outside the vault would not receive the vault's
encryption.

## Embeddings

When an embedding model is configured, Lemmary splits each document's metadata
and OCR text into passages. The float32 vectors and embedding state are stored
in raw SQLite tables inside `data.db`; they do not go into S3.

Deep Search queries a derived Bleve vector index at `pb_data/bleve/chunks`.
That index can be deleted and rebuilt from the vectors in SQLite without
calling the embedding provider again. The ordinary full-text index beside it,
at `pb_data/bleve/documents`, is derived and rebuildable too.

Embedding storage grows with the number of passages and vector dimensions. See
[what embeddings cost](/ai_providers#what-embeddings-cost) and
[Local embeddings](/local_embeddings) for sizing and provider options.
