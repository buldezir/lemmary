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

| Tool | What it does |
| --- | --- |
| `search_documents` | Search by meaning and keywords with optional date, type, correspondent and tag filters. Returns matching documents with one to three verbatim passages each. |
| `read_documents` | Read documents by id. Long documents come back as excerpts around the `focus` you give. |
| `count_documents` | Count documents matching filters, optionally grouped by `document_type`, `correspondent`, `year`, `month` or `tag`. Grouping is also how an agent discovers which types and correspondents exist. |
| `list_tags` | The tag names in the archive, for the tag filters above. |

Uploading, tagging and editing are not exposed. Use the [paperless-ngx API](/paperless_ngx) for that.

## What it costs

Each `search_documents` call is one Deep Search retrieval: a full-text query plus, when embeddings are configured, one embedding request to your provider for the query text. An agent in a loop pays per call, on your key. Nothing is spent while the flag is unset.
