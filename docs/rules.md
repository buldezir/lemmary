# Rules and webhooks

Rules are if-then automation for your own documents. Each rule has a trigger, conditions and
actions: when the trigger fires and every condition holds, the actions run. Typical uses:

- **Invoices** — if the document type is *Invoice*, have the AI fill *Amount*, *Due date* and
  *IBAN* with instructions that only apply to invoices, and add the tag *to pay*.
- **Large amounts** — if *Amount* is more than 1000, send the document to the Inbox for review.
- **Automation** — if the correspondent is your landlord, call an n8n, Home Assistant or Zapier
  webhook with the document.

Rules live under **More → Rules**. Every user has their own; a rule only ever acts on its
owner's documents.

---

## Triggers

- **A document is processed** — after AI extraction wrote its metadata, a reprocess included.
  Documents imported with their metadata preserved (Paperless-ngx, Papra) are not processed,
  so this trigger does not fire for them.
- **I change its tags or fields** — after you change a document's tags, document type,
  correspondent or another field in the app. A title or summary edit does not count. The
  rules run as a short job on the document; a document in the Inbox stays in the Inbox.

A rule can have both.

## Conditions

All conditions of a rule must hold. A rule without conditions runs on every document.

| Field | Comparisons |
| --- | --- |
| Document type, correspondent, choice fields | is, is not, is filled in, is empty |
| Tags | includes, does not include |
| Title, document text, text fields | contains, does not contain, matches the pattern, is filled in, is empty |
| Number fields, AI confidence | is, is more than, is less than, is filled in, is empty |
| Date fields | is, is after, is before, is filled in, is empty |

Text comparisons ignore case. A pattern is a [Go regular
expression](https://github.com/google/re2/wiki/Syntax) and is case-sensitive unless it starts
with `(?i)`. AI confidence runs from 0 to 1; extraction below 0.5 already goes to the Inbox.

## Actions

- **Add tag / Remove tag**
- **Set** a document type, a correspondent or another field to a value.
- **Send to the Inbox for review** — the document waits until someone marks it reviewed.
- **Fill fields with AI** — one extra request to the extraction model, asking only for the
  fields you tick, with your instructions. Fields that already have a value keep it. The
  fields themselves are defined by your administrator under **Admin → Document fields**.
- **Call webhook** — see below.

## Order {#order}

Rules run top to bottom, once each. A rule sees what the rules above it changed: a rule that
fills *Amount* with AI can be followed by one that checks *Amount*. A change never makes a
rule run a second time. Actions run in the order they are listed, except webhooks, which are
sent after everything else is saved.

When an action cannot run — the model does not answer, a webhook is down, a tag the rule
names was deleted — the other actions still apply, and the document's processing steps show
**Run rules** as failed with the reason. The steps also name the rules that matched.

## Webhooks {#webhooks}

A webhook is a `POST` with this JSON body:

```json
{
  "event": "rule.matched",
  "trigger": "processed",
  "rule": { "id": "…", "name": "Invoices" },
  "document": {
    "id": "…",
    "title": "Invoice R-2026-001",
    "summary": "…",
    "document_date": "2026-10-01",
    "document_type": "Invoice",
    "correspondent": "ACME GmbH",
    "tags": ["to pay"],
    "fields": { "Amount": 129.9, "Due date": "2026-10-31" },
    "processing_status": "completed"
  }
}
```

With a signing secret, the `X-Lemmary-Signature` header carries `sha256=` followed by the hex
HMAC-SHA256 of the body under the secret. Compare it in constant time before trusting the
call. Any 2xx answer is success; a webhook has 10 seconds and is not retried.

Webhooks go through the same address check as remote imports: link-local and cloud metadata
addresses are always refused, and private and loopback addresses are refused when the server
runs with `IMPORT_ALLOW_PRIVATE=0`.
