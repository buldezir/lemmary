# MCP for agents

Lemmary can serve its archive over the [Model Context Protocol](https://modelcontextprotocol.io), so an agent — Claude Code, Claude Desktop, Cursor, or one you wrote — searches and reads your documents directly instead of through the browser. The endpoint is read-only and uses the same retrieval as Deep Search: a token sees exactly what its user sees in the app, and nothing else.

## Enable it

```env
MCP_ENABLED=1
```

Unset means there is no route at all. Restart after changing it; the flag is read once at startup.

## Get a token

Any Lemmary auth token works. For an agent that runs unattended, the long-lived token from the paperless-compatible endpoint is the convenient one:

```bash
TOKEN=$(curl -s -X POST https://lemmary.example.com/api/token/ \
  -d username=you@example.com -d password=… | jq -r .token)
```

A superuser (PocketBase admin) token searches every account. A regular user token is scoped to that user's documents.

## Connect

The server speaks Streamable HTTP at `/api/mcp` and expects `Authorization: Bearer <token>`.

Claude Code:

```bash
claude mcp add --transport http lemmary https://lemmary.example.com/api/mcp \
  --header "Authorization: Bearer $TOKEN"
```

Claude Desktop, Cursor and most other clients take the same thing as JSON:

```json
{
  "mcpServers": {
    "lemmary": {
      "type": "http",
      "url": "https://lemmary.example.com/api/mcp",
      "headers": { "Authorization": "Bearer …" }
    }
  }
}
```

## Tools

Two kinds: search over the index, and plain access to the rows. The intelligence lives in your agent, so nothing here calls a language model.

| Tool | What it does |
| --- | --- |
| `search_documents` | Hybrid search by meaning and keywords over the full-text and vector index, with optional date, type, correspondent and tag filters. Returns matching documents with one to three verbatim passages each. |
| `read_documents` | The parts of up to ten documents that matter for a `focus`, ranked by the same index. Long documents come back as passages with gaps marked. |
| `list_documents` | Plain listing by metadata: date range, document type, correspondent, tags, processing status; sorted by date or by when it was added; paged with `limit` and `offset`. Returns metadata and the total, no text. |
| `get_document` | One document's metadata and its whole extracted text, unranked and unabridged. Page through a long one with `offset` and `max_chars`. |
| `count_documents` | Count documents matching filters, optionally grouped by `document_type`, `correspondent`, `year`, `month` or `tag`. |
| `list_taxonomy` | The tag, document type and correspondent names in the archive, for the filters above. |

Uploading, tagging and editing are not exposed. Use the [paperless-ngx API](/paperless_ngx) for that.

## What it costs

Each `search_documents` call is one Deep Search retrieval: a full-text query plus, when embeddings are configured, one embedding request to your provider for the query text. A `read_documents` call with a `focus` embeds the focus the same way. Nothing here calls a language model, so no chat tokens are spent. An agent in a loop still pays per call, on your key. Nothing is spent while the flag is unset.
