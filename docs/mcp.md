# MCP for agents

Lemmary can serve its archive over the [Model Context Protocol](https://modelcontextprotocol.io), so an agent — Claude Code, Claude Desktop, Cursor, or one you wrote — searches and reads your documents directly instead of through the browser. The endpoint is read-only and uses the same retrieval as Deep Search: a token sees exactly what its user sees in the app, and nothing else.

## It is on by default

The endpoint answers only to a bearer token and costs nothing until an agent calls it, so it is on unless an operator turns it off:

```env
MCP_ENABLED=0
```

Off means there is no route at all. Restart after changing it; the flag is read once at startup.

## Get a token

The Account page has an **Agents** section: one click mints a long-lived token for your account and shows the ready-to-paste configuration for Claude Code, Codex CLI, Gemini CLI, Cursor, VS Code, Windsurf, OpenCode and Claude Desktop (as a command where the agent has one, else its config file).

Any Lemmary auth token works too. From a script, the long-lived token from the paperless-compatible endpoint is the convenient one:

```bash
TOKEN=$(curl -s -X POST https://lemmary.example.com/api/token/ \
  -d username=you@example.com -d password=… | jq -r .token)
```

Every token is scoped to one account's documents. An admin's token maps to the admin's own `users` account (the one created alongside it at setup), not to every account on the instance.

## Connect

The server speaks Streamable HTTP at `POST /api/mcp` and expects `Authorization: Bearer <token>`. As the protocol requires, requests carry `Accept: application/json, text/event-stream`; MCP clients do this themselves, a hand-rolled `curl` has to.

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
| `list_documents` | Plain listing by metadata: date range, document type, correspondent, tags, processing status (`unfinished` means everything but completed); sorted by date or by when it was added; paged with `limit` and `offset`. Returns metadata and the total, no text. |
| `get_document` | One document's metadata and its extracted text, unranked and unabridged. A call returns up to 200,000 characters; page through a longer one with `offset` and `max_chars`, and `truncated` says when more follows. |
| `count_documents` | Count documents matching filters, optionally grouped by `document_type`, `correspondent`, `year`, `month` or `tag`. |
| `list_taxonomy` | The tag, document type and correspondent names in the archive, for the filters above. Each list stops at 5,000 names and says so with `truncated`. |

Uploading, tagging and editing are not exposed. Use the [paperless-ngx API](/paperless_ngx) for that.

## What it costs

Each `search_documents` call is one Deep Search retrieval: a full-text query plus, when embeddings are configured, one embedding request to your provider for the query text. A `read_documents` call with a `focus` embeds the focus the same way. Nothing here calls a language model, so no chat tokens are spent. An agent in a loop still pays per call, on your key. Nothing is spent while the flag is unset.
