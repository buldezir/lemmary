# Guided AI provider setup

The short path to a working instance: **Mistral** for OCR and embeddings on its
free tier, **Opencode Go** for the language model. Two keys, five minutes, and
the whole pipeline works — extraction, chat, Deep Search and meaning-based
retrieval.

Get the two keys (steps 1 and 2), then paste them into the **setup wizard** a
fresh instance opens on, or into **Settings** on one that is already
running (step 3). Nothing needs to go in a config file; see
[Seeding it from `.env`](#advanced-seeding-it-from-env) if you would rather an
instance came up already configured. The full picture, and every alternative,
is in [AI providers and models](/ai_providers).

---

## Step 1: A Mistral API key, for free

Mistral's free plan ("Experiment") gives rate-limited access to the whole
catalogue, including the Document OCR endpoint and the embedding model. No card,
but it does want a phone number.

1. Go to [console.mistral.ai](https://console.mistral.ai/) and sign up.
2. Verify your phone number when asked — the free tier is not activated without
   it.
3. Accept the terms for the free plan under **Billing** if you are prompted
   (make sure you land on *Experiment*, not a paid tier).
4. Open **API Keys** → **Create new key**, name it, and copy the value. It is
   shown once.
5. Go to [admin.mistral.ai/plateforme/privacy](https://admin.mistral.ai/plateforme/privacy)
   and **disable data collection** — the free tier opts you into training on
   your data by default, and your documents are what gets sent.

### The two models to bind

| Job | Model | Notes |
| --- | --- | --- |
| OCR | `mistral-ocr-latest` | The purpose-built [Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr), not a chat model reading a scan. Handles PDFs, images and office documents; 1000 pages a file. |
| Embeddings | `mistral-embed` | 1024 dimensions, so it costs less space than a 1536-dimension model — which matters under [encryption](/encryption), where the archive is decrypted into RAM. |

Mistral can serve the language model too (`mistral-small-latest` and up), so
this key alone is a complete install — one provider, every job. Step 2 is about
getting a better language model.

---

## Step 2: An Opencode Go key, for the language model

[Opencode Go](https://opencode.ai/go?ref=84VDFS18QN) is one subscription across a
catalogue of models from several vendors, rather than a metered key per vendor.
Lemmary has a dedicated `opencode` SDK, so the key is the whole configuration —
it already knows which of Opencode's three endpoints each model is served on.

1. Subscribe at [opencode.ai/go](https://opencode.ai/go?ref=84VDFS18QN).
2. Sign in and open the dashboard's **API keys** section; create one and copy
   it. That is all Lemmary needs — no base URL, no per-model configuration.

### Which model

| | Model | Why |
| --- | --- | --- |
| **Recommended** | `gpt-5.6-luna` | Lemmary's default extraction model. Strongest of the three at pulling structured metadata out of a messy scan and at Deep Search's multi-step reading. |
| Backup | `deepseek-v4-flash` | Fast and cheap on the same subscription. A good **Deep Search helper** model even when Luna does the answering. |
| Backup | `qwen3.8-flash` | The other fast option; try it if DeepSeek is rate-limited or refuses your result language. |

### Why not run OCR on Opencode too

You can — a file-capable Opencode model reads a scan the way any vision model
does, and an Opencode key alone is a complete install. Mistral is still the
better choice for the OCR binding:

- **Faster and cheaper per page.** A dedicated OCR endpoint returns page
  markdown; a chat model reasons its way through the same image.
- **Better with big files.** Mistral's Document OCR takes a whole PDF — up to
  50 MB and 1000 pages, well past Lemmary's own 20 MB upload cap — where a chat
  model's context is the ceiling, and long scans get truncated or refused.
- **You need the Mistral key anyway.** Opencode serves no `/embeddings`
  endpoint at all, so meaning-based Deep Search has to run on Mistral. Since
  the key is already there, binding OCR to it costs nothing extra.

---

## Step 3: Enter them in Lemmary

A fresh instance opens the **setup wizard** after you create the admin account;
an instance that is already running has the same fields under **Settings**
(visible to admins). Either way it is two providers and three bindings.

### Add the providers

The wizard opens on **Connect your AI providers**, which asks for exactly these
two keys and creates both rows in one submit:

| Field | Value |
| --- | --- |
| **Mistral API key** | the key from step 1 |
| **General AI provider** | Opencode Go, and the key from step 2 |

Leave the second key blank to run everything on Mistral. Anything the pair does
not cover — a ChatGPT sign-in, a local sidecar, a second key later — is behind
**Add a provider manually instead**, which is also what **Settings → Providers**
offers on an instance that is already running. Aliases and base URLs are filled
in for you.

### Bind the models

The wizard's **Choose models** step arrives with all three bindings already
filled from the keys above; **Settings → Models** has the same fields.

| Binding | Provider | Model |
| --- | --- | --- |
| OCR | Mistral | `mistral-ocr-latest` |
| Extraction | Opencode Go | `gpt-5.6-luna` |
| Embeddings *(optional)* | Mistral | `mistral-embed` |

Chat and Deep Search follow the extraction binding unless you set them
separately, so those three fields are the whole of a working install. Set the
embeddings provider to **None** to leave Deep Search on keywords alone — it is
the one binding nothing else depends on. If a model you want is not in a picker,
type it into **Custom model id**. **Finish setup** and the first upload will be
OCR'd, tagged and searchable.

Everything here hot-reloads: changing a provider or a binding in Settings takes
effect on the next request, with no restart.

Two things worth knowing before you leave embeddings bound: it commits to embedding
the whole archive, not just the next upload — see [what embeddings
cost](/ai_providers#what-embeddings-cost) — and a model change re-embeds
everything, because vectors from two models cannot be compared.

---

## Advanced: seeding it from `.env`

Only useful if you want a *fresh* instance to come up already configured — a
scripted deploy, or a volume you expect to recreate. The variables are read on
the **first boot only**, when the settings row does not exist yet; after that
**Settings** is the authority and editing `.env` changes nothing. (The exception
is a managed instance, `AI_MANAGED=1`, where the environment wins on every
boot — see [Two modes, one build](/ai_providers#two-modes-one-build).)

```dotenv
# language model — extraction, chat, Deep Search
AI_SDK=opencode
AI_API_KEY=sk-your-opencode-key
AI_MODEL=gpt-5.6-luna

# OCR — Mistral's Document OCR API
OCR_SDK=mistral
OCR_API_KEY=your-mistral-key
OCR_MODEL=mistral-ocr-latest

# embeddings — meaning-based Deep Search, same Mistral key
AI_EMBEDDING_SDK=mistral
AI_EMBEDDING_API_KEY=your-mistral-key
AI_EMBEDDING_MODEL=mistral-embed
```

That is the same configuration step 3 produces, so the wizard only asks for the
admin account and then finishes. Every variable is described in
[The provider block](/ai_providers#the-provider-block).

## If something does not work

- **The wizard keeps asking for a provider** — it needs both jobs covered: a
  language-model provider *and* an OCR provider. The Opencode Go row satisfies
  the first, the Mistral row the second.
- **`.env` changed nothing** — those variables apply on the first boot of a
  fresh volume only. On an instance that has already booted, use **Settings**.
- **Every Opencode request fails with a 500 or 404** — the SDK must be
  `opencode`, not `openai` with a base URL pointed at it. See
  [the routing table](/ai_providers#troubleshooting).
- **Mistral returns 429** — the free tier is rate-limited; the pipeline retries
  with a backoff and no document is lost.
