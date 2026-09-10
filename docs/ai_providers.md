# AI providers and models

Lemmary needs two things from an AI provider: **OCR** for PDFs and images, and a
**language model** for metadata extraction, document chat and Deep Search. An
**embedding model** is optional and adds meaning-based retrieval to Deep Search.

One provider can serve all three. Configure them in the admin **Settings** page,
or seed them from `.env` before the first boot so a fresh instance comes up ready
to use.

## Choosing a provider

| Provider | Language model | OCR | Embeddings |
| --- | --- | --- | --- |
| **Opencode Go** — `opencode` | ✅ **a catalogue of models on one subscription** | ✅ models that accept files/images | ❌ |
| **`mistral`** | ✅ chat completions | ✅ dedicated Document OCR API | ✅ `mistral-embed` |
| `openai` | ✅ | ✅ models that accept files/images | ✅ |
| `openrouter` | ✅ many vendors on one key | ✅ models advertising `file` input | ✅ |
| `google_vision` | ❌ | ✅ | ❌ |
| **ChatGPT subscription** — `chatgpt` | ✅ **on a ChatGPT subscription** | ✅ **on the same seat** | ❌ |
| **Local OCR (Docling)** — `docling` | ❌ | ✅ **on your own host** | ❌ |
| **Local Embeddings (huggingface/text-embeddings-inference)** — `local` | ❌ | ❌ | ✅ **on your own hardware** |

The names in bold are what Settings shows. The shorter code values remain the
values used by `OCR_SDK` and `AI_EMBEDDING_SDK`.

**Start with Mistral.** It is the only SDK that covers every job Lemmary has, so
one key and one provider row configure the whole instance — OCR, extraction,
chat, Deep Search and embeddings — with nothing left to bind. Its OCR is a
purpose-built document endpoint rather than a general model asked to read a
scan, and its catalogue advertises per-model capabilities, so the Settings model
pickers show the right models instead of the whole catalogue.

**Or start with Opencode Go**, if a subscription suits you better than metered
keys: `AI_SDK=opencode` and a key is the whole configuration, and it covers
extraction, chat, Deep Search and OCR across a catalogue of models from several
vendors. It does not embed — see the bullet below — so Deep Search matches
keywords until you add a second provider for that.

The alternatives are worth naming:

- **Opencode Go** — one subscription across a catalogue of models (GLM, Kimi,
  DeepSeek, Qwen, MiniMax, Grok and more). Unlike every other SDK here it does
  not serve one API: each model lives on `/chat/completions`, `/responses` or
  Anthropic's `/messages`, and which one is a property of the model that the
  catalogue does not report — so Lemmary carries the table and speaks all three.
  That is also why it is its own SDK rather than `openai` with a base URL: on
  any other SDK every model goes to `/chat/completions`, where two thirds of the
  catalogue is not served, and the `x-opencode-session` header it requires would
  not be sent. Embeddings are refused, as for a ChatGPT seat: the gateway has no
  `/embeddings` and the catalogue has no embedding model, so pair it with
  `AI_EMBEDDING_SDK=local` or a metered key if you want meaning-based retrieval.
  Its SDK value is `opencode`.
- **`openai`** — pick this when you already have a key, or to point
  `AI_BASE_URL` at an OpenAI-compatible gateway or a self-hosted endpoint. Its
  `/v1/models` describes nothing, so the model pickers show the full catalogue
  with a warning to choose a file-capable model for OCR.
- **`openrouter`** — one key across many vendors, and the only provider that
  filters its catalogue server-side (`input_modalities=file` for OCR,
  `output_modalities=embeddings` for embeddings), so both pickers are accurate.
- **`google_vision`** — OCR only; it cannot serve extraction and is refused as
  `AI_SDK`. Worth pairing with an LLM when its free tier (1000 pages a month)
  matters. See [Google Vision API key](/google_vision).
- **ChatGPT subscription** — everything but embeddings, billed against a
  ChatGPT Plus, Pro or Business subscription instead of per token. There is no
  key to paste: you add the provider, sign in with a device code, and bind chat,
  extraction, Deep Search or OCR to it. OCR works the way it does on `openai` —
  the file goes to the model — so an instance whose only AI credential is a
  ChatGPT seat is a complete install. Embeddings are refused: the endpoint has
  no `/embeddings` at all. Off unless `AI_CHATGPT_LOGIN=1`, and refused on a
  managed instance — it reaches OpenAI's own Codex endpoints, so the account you
  sign in with is the one carrying the risk. Its SDK value is `chatgpt`, though
  it is not an `AI_SDK` or `OCR_SDK` value: a sign-in cannot be written into
  `.env`. See [ChatGPT sign-in](/chatgpt_login).
- **Local OCR (Docling)** — OCR only, and the only provider that reads a
  document without sending it anywhere: a sidecar container beside the app,
  with no port published and **no API key at all** — the base URL is the whole
  configuration. Reach for it when the archive is confidential enough that a
  hosted OCR API is not an option, or when the host has no outbound internet;
  pair it with a local OpenAI-compatible endpoint under `AI_BASE_URL` and
  nothing leaves the machine. Its default recognizer is PaddleOCR's PP-OCR
  models, so there is no separate PaddleOCR provider to choose. The price is
  real: a multi-gigabyte image, and seconds rather than milliseconds a page.
  Its SDK value is `docling`. See [Local OCR](/local_ocr).
- **Local Embeddings (huggingface/text-embeddings-inference)** — embeddings
  only, using a model you run yourself. Like Local OCR it takes no API key: the
  base URL is the whole configuration. It is the mirror image of
  `google_vision` — a single job, done off the network. Its SDK value is
  `local`. See [Local Embeddings](/local_embeddings).

Together, Local OCR and Local Embeddings are the two halves of an install where
nothing leaves the host: point `AI_BASE_URL` at Ollama or vLLM, set
`OCR_SDK=docling`, and set `AI_EMBEDDING_SDK=local`.

Without a language-model provider, AI extraction, document chat and Deep Search
return a configuration error.

## Two modes, one build

Which mode an instance is in is a runtime flag, `AI_MANAGED`, and that is the
whole of the difference.

| | self-hosted (default) | managed (`AI_MANAGED=1`) |
| --- | --- | --- |
| when the environment is written to the database | the first boot, when the settings singleton does not exist yet | **every** boot |
| authority afterwards | the **Settings** page | the container's environment |
| Providers, Models and Duplicates in Settings | editable | not rendered, and the API answers `403` |
| an incomplete or invalid block | the setup wizard opens and an admin fills it in | the process **refuses to start**, naming the variable |

Self-hosted is the ordinary install: put a key in `.env` so a fresh volume comes
up ready to use instead of on the wizard, and change your mind later in
**Settings**. Managed is for a hosted fleet, where the operator carries the AI
bill and the tenant must not be able to move it onto their own key. Refusing to
start is the right failure there, because nobody inside a managed instance can
repair a bad key: the Settings page is gone.

Neither mode compares against what was applied last. An earlier release stored a
digest per variable in `app_settings.env_applied` and re-applied a variable only
when it had changed; naming the two modes made that unnecessary, and the column
is dropped.

A provider record is matched by the default alias it is created with. A renamed
or deleted provider is left alone rather than reclaimed — renaming one is an
edit an admin made, and a boot that undid it would undo it again on every boot
after that.

## The provider block

| Variable | Default | Description |
| --- | --- | --- |
| `AI_MANAGED` | `0` | Whether the operator owns AI configuration. See the table above. |
| `AI_CHATGPT_LOGIN` | `0` | Whether **Settings** offers the ChatGPT subscription SDK (`chatgpt`), which bills a ChatGPT subscription instead of a metered key. Off unless set, and refused together with `AI_MANAGED=1`. It is not an `AI_SDK` value: the provider is added and signed in to from Settings, because its credential is minted rather than typed. See [ChatGPT sign-in](/chatgpt_login). |
| `AI_SDK` | `openai` | The language model's SDK: `opencode`, `openai`, `openrouter` or `mistral`. `google_vision`, `docling` (Local OCR) and `local` (Local Embeddings) are refused — none of them can serve extraction. `chatgpt` too: it has no key to seed from the environment. |
| `AI_API_KEY` | empty | Its credential. **One key is usually the whole configuration**: with this and nothing else the app creates one provider and routes extraction, chat, Deep Search *and* OCR to it. |
| `AI_MODEL` | `gpt-5.6-luna` | The model for extraction, chat and Deep Search. Be sure it supports the result language set in **Settings**. |
| `AI_BASE_URL` | the SDK's own endpoint | An OpenAI-compatible base URL, for a gateway or a self-hosted endpoint. Leave it unset for `opencode`, whose own endpoint is `https://opencode.ai/zen/go/v1`. |
| `OCR_SDK` | unset (OCR runs on the `AI_SDK` provider) | A separate provider for OCR: `opencode`, `openai`, `openrouter`, `mistral`, `google_vision` or `docling` (Local OCR). `local` (Local Embeddings) is refused — it serves embeddings only. `chatgpt` reads documents but is refused here too: it is signed in to from Settings rather than given a key, so the environment has nothing to seed it with. Naming the same SDK as `AI_SDK` reuses that key and endpoint and only changes the model. |
| `OCR_API_KEY` | `AI_API_KEY` when the SDKs match | Its credential. Required for an OCR SDK that differs from `AI_SDK` — except Local OCR (`docling`), which has no account behind it. Optional there, and only if you started the sidecar with `DOCLING_SERVE_API_KEY`. |
| `OCR_BASE_URL` | `AI_BASE_URL` when the SDKs match, else the SDK's own endpoint | Where that provider lives. For Local OCR (`docling`) the default is the compose service name, `http://docling:5001`, so `OCR_SDK=docling` alone is a complete configuration under the overlay. |
| `OCR_MODEL` | `AI_MODEL` when the SDKs match | Its model. Not required for `google_vision` or Local OCR (`docling`), which read a document without one; for Local OCR it optionally names the OCR engine instead. See [Choosing an engine](/local_ocr#choosing-an-engine). |
| `AI_EMBEDDING_MODEL` | unset (Deep Search matches keywords only) | An embedding model — on the `AI_SDK` provider, or on the `AI_EMBEDDING_SDK` one when that is set — so Deep Search can also find documents by meaning. Under `AI_SDK=opencode` it requires `AI_EMBEDDING_SDK`: Opencode serves no `/embeddings`, so there is no provider to fall back to, and naming a model without one is refused at boot. Operator-owned under `AI_MANAGED=1`; removing it there turns the feature off. See [what embeddings cost](#what-embeddings-cost). |
| `AI_SEARCH_HELPER_MODEL` | unset (the Search model does this work) | A cheaper model on the `AI_SDK` provider for Deep Search's bulk per-document work: distilling long reads into notes and surveying many documents for one question. Operator-owned under `AI_MANAGED=1`. See [How Research covers a topic](/setup#how-research-covers-a-topic). |

### The embedding provider

An earlier release had no `AI_EMBEDDING_SDK` / `_API_KEY` / `_BASE_URL`, on the
reasoning that pointing embeddings somewhere other than the language model was a
rare enough choice to belong in **Settings**, and that three more variables would
mostly be three more ways to half-configure the feature.

Running the embedding model yourself is the case that reasoning did not
anticipate. A sidecar on the compose network *is* a different endpoint, by
definition and not by preference — so without these an operator could not bring
an instance up on one from `.env` at all, and a managed instance could not use
one at any price. They are here, shaped exactly like the `OCR_*` block above,
which has always been how a second provider for one job is described.

| Variable | Default | Description |
| --- | --- | --- |
| `AI_EMBEDDING_SDK` | unset (embeddings run on the `AI_SDK` provider) | A separate provider for embeddings: `openai`, `openrouter`, `mistral` or `local` (Local Embeddings). Naming the same SDK as `AI_SDK` reuses that key and endpoint and only changes the model. `google_vision`, `docling` (Local OCR), `opencode` and `chatgpt` are refused — none of them has an `/embeddings` endpoint. Required rather than optional when `AI_SDK` is one of the last two and `AI_EMBEDDING_MODEL` is set. |
| `AI_EMBEDDING_API_KEY` | `AI_API_KEY` when the SDKs match | Its credential. Required for an SDK that differs from `AI_SDK`, **except Local Embeddings (`local`)**, which takes none. |
| `AI_EMBEDDING_BASE_URL` | the SDK's own endpoint, or `AI_BASE_URL` when the SDKs match | Where that provider lives. For Local Embeddings (`local`) this defaults to `http://embeddings:80/v1`, the compose overlay's service. |

Unset, all three change nothing: embeddings ride on the `AI_SDK` provider,
exactly as they did before the block existed. Setting `AI_EMBEDDING_SDK` without
`AI_EMBEDDING_MODEL` is refused rather than ignored — it would create a provider
with nothing bound to it, which reads as a configured feature that never embeds
anything.

### Seeded settings

Written to `app_settings` on the first boot and edited from **Settings**
afterwards, in both modes.

| Variable | Default | Description |
| --- | --- | --- |
| `OCR_TIMEOUT_SEC` | `40` | OCR request timeout. Far too low for [Local OCR](/local_ocr), which needs seconds to tens of seconds a page |
| `AI_TIMEOUT_SEC` | `60` | Extraction, chat, search and split-detection request timeout |
| `WORKER_TIMEOUT_SEC` | `300` | Per-job processing timeout |
| `WORKER_MAX_RETRIES` | `0` | Max step retry attempts before a job fails |
| `DEEP_SEARCH_LANGUAGES` | empty | Comma-separated ISO 639-1 codes (e.g. `de,en,uk`) for Deep Search keyword expansion. Only drives per-language searches when no embedding model is set; with one, a single search already crosses languages |
| `EXTRACTION_PROMPT_VERSION` | `v1` | Stored on each processing job step run, with a digest of the Settings extraction rules appended where any are set; bookkeeping only, not offered in the Settings UI |

Two more are seeded the same way but are **operator-owned under
`AI_MANAGED=1`**, because each is a cost rather than a preference — so a hosted
plan can price them per tier:

| Variable | Default | Description |
| --- | --- | --- |
| `NEAR_DUPLICATE_DETECTION_ENABLED` | `false` | Whether the pipeline runs near-duplicate detection |
| `NEAR_DUPLICATE_THRESHOLD` | `0.92` | How similar two documents' text must be to count as near-duplicates |

One more is read from the environment on every boot and never stored, because it
paces spending rather than describing the instance:

| Variable | Default | Description |
| --- | --- | --- |
| `EMBEDDING_BACKFILL_BATCH` | `20` | Documents one backfill tick embeds, on `WORKER_CRON_EXPR`. `0` disables the scheduled backfill, so only newly processed documents are embedded and an existing archive is left alone — **Management → Embeddings** still embeds it on demand. |

The **result language** has no variable at all. It decides what language a
document's title, summary and tags are stored in, which is a reader's preference
rather than an operator's, so it is set in **Settings** and a managed instance
keeps it.

## Binding models in Settings

1. Sign in with the admin account and open **Settings** (shown when
   `/api/app/me` reports `is_admin`).
2. Add a provider — SDK, API key, optional base URL.
3. Under **Models**, bind a provider and model to OCR and to metadata
   extraction; chat and search inherit extraction unless bound separately.
   **Deep Search helper** and **Deep search languages** live here too.

Changes hot-reload the in-process clients — no restart. The OCR picker lists
only file-capable models where the provider says which those are (OpenRouter's
`file` input, Mistral's `ocr` capability); other SDKs show the full catalogue
with a warning. Any list can be typed past with the **Custom model id** field.

## Overriding a model for one chat or one job

The bindings above are the defaults. A single chat or a single reprocess job can
run on a different provider and model without touching them — useful for
retrying one stubborn document on a stronger extractor, or asking a cheap
question of a cheap model. Any signed-in user can do this; the choice is among
the providers an admin has already configured, so it can spend the operator's
credentials but never add a new one.

- **Ask AI** and **Deep Search** offer *Use a different chat/search model*
  under the composer, which also names the model in use when nothing is
  overridden. The choice is fixed for the conversation, like the Search/Research
  mode beside it: the transcript replayed on each turn was produced by one
  model, and answering the next question with another reads that work back as if
  it were its own. Start a new chat to switch. Deep Search's **helper** model is
  not moved by this — it is a separate binding because it does many cheap
  per-document calls where the search model does a few expensive ones.

  **A conversation records the model it opened on, whether or not anyone picked
  it.** So changing the chat or search binding in Settings applies to new chats
  and leaves existing ones where they are, rather than moving every open
  transcript onto a model that did not write it. Conversations from before this
  shipped have nothing recorded and do still follow Settings. If the provider a
  conversation is pinned to is later deleted, the chat keeps working: the next
  turn falls back to the binding in Settings, so rotating a provider does not
  leave open chats stuck.
- **Reprocess** — on a document's own page, on the document list's bulk bar, and
  in **Management → Failed processing** — offers OCR, extraction and embedding.
  The choice is stored on each queued job, so a batch queued to try a different
  extractor runs on it however long the queue takes, rather than on whatever
  Settings holds by the time the worker gets there. The step history
  (**Processing job → step runs**) records the provider and model that actually
  ran.
- Left untouched, every picker sends nothing and the job or chat runs exactly as
  it did before — the same request, byte for byte.

**The embedding override must name the model already bound in Settings.** A
chunk row records the model and dimension count it was produced with, and the
retrieval index only reads the rows matching the configured one, so vectors from
any other model would be paid for, written, and never read — a document silently
dropping out of dense search. The server refuses that rather than letting it
look like success. Re-embedding on the *configured* model is the case worth
having: a fix-up for a document whose vectors are missing or stale. To change
the model itself, change it in Settings, which re-embeds everything (see [What
embeddings cost](#what-embeddings-cost)).

## What embeddings cost

Turning `AI_EMBEDDING_MODEL` on is a commitment to embed the whole archive, not
just the next upload, so it is worth knowing the shape of the bill before you
make it.

- **Tokens.** Each document is cut into ~1100-character passages plus one
  passage rendered from its metadata, and each passage is one embedding input —
  roughly one request per 30 KB of text. Embedding models are cheap per token;
  this is simply every document you have.
- **Re-embedding.** A document is embedded again whenever its OCR text or its
  metadata changes: a re-OCR, an edited title, a renamed tag, a reprocess. And
  *every* document is embedded again when you change the model, because vectors
  from two models cannot be compared — there is no partial migration.
- **Space, which under encryption is RAM.** A 1536-dimension vector is about
  6 KB; a typical document is a handful of passages, so 30–60 KB each inside
  `data.db`. With `VAULT_ENABLED=1` the archive is decrypted into a tmpfs, so
  that space is memory. A model with 1024 dimensions or fewer costs
  proportionally less of it. See [Encryption at rest](/encryption).

Running the model yourself moves the first two costs rather than removing them:
there is no token bill and no per-document price at all, but the same work
happens on your CPU, and the first backfill of a large archive is the one time
that is slow enough to notice. The third cost — space, and under a vault RAM —
is unchanged, and depends only on the dimension count you pick. See [Local
Embeddings](/local_embeddings).

The backfill drains at `EMBEDDING_BACKFILL_BATCH` documents a tick and logs what
it embedded, what failed, and how many are left; **Settings → Models** shows the
same counts, and **Management → Embeddings** both shows them and runs the whole
backlog on demand rather than waiting a tick a minute. A provider failure is
soft: the document keeps its text, its metadata and its place in keyword search,
and is retried later with a backoff.

Running the model on your own hardware has a page of its own: see [Local
Embeddings](/local_embeddings) for the compose overlays, choosing a model, and
what it costs in host memory.

## OCR, per provider

Bind an OCR-capable provider under **Settings → Models**. Text extraction for
TXT, CSV, DOCX and XLSX uses native parsers and calls no OCR API at all.

### Mistral Document OCR

Uses the [Mistral Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr)
when the provider is bound for OCR — not the chat endpoint, which the same
provider can serve for extraction, chat and search at the same time. Local files
are sent as base64 data URLs, up to Mistral's documented 50 MB, which the 20 MB
`documents.file` cap already keeps every upload under.

- **PDFs and office documents** — `document_url` with a base64 data URL
- **Images** — `image_url` with a base64 data URL
- **Output** — page markdown joined into plain text

Mistral is also the only provider that documents a page limit — 1000 pages,
which is where [the page ceiling](/setup#the-page-ceiling) comes from.

### OpenAI / OpenRouter models

Any model that accepts files or images can serve OCR: the document is sent to
the chat endpoint and the text comes back as the completion. OpenRouter lists
only models advertising `file` input; OpenAI's catalogue says nothing, so choose
a file-capable model yourself.

### Google Cloud Vision

Uses the official [Go client library](https://docs.cloud.google.com/vision/docs/detect-labels-image-client-libraries).

- **Images** — `BatchAnnotateImages` with `DOCUMENT_TEXT_DETECTION` via `images:annotate`
- **PDFs** — `BatchAnnotateFiles` via `files:annotate` (base64 upload, no Cloud
  Storage). Pages are processed in batches of up to 5 per request, however many
  the file has.

See [Google Vision API key](/google_vision) for obtaining a key.

### Local OCR (Docling)

A container beside the app rather than an API. It speaks docling-serve's
`POST /v1/convert/file` and gets markdown back, reading PDFs, images and office
documents. It is keyless — the address is the whole configuration — and
publishes no port, so only the app can reach it.

Its default recognizer is RapidOCR, which is PaddleOCR's PP-OCR models as ONNX,
and the OCR model binding switches that to EasyOCR or Tesseract. That is why
there is no second local SDK: the PaddleOCR recognition is already here.

Bring it up with the `docker-compose.local-ocr.yml` overlay and raise
`OCR_TIMEOUT_SEC`, which is the setting people miss. Everything else — the image
size, memory, GPU variants and the per-page cost — is in
[Local OCR](/local_ocr).

## Troubleshooting

- **Stuck on the setup wizard** — add an OCR provider and a language-model
  provider (one Mistral provider is both), or set `AI_API_KEY` in `.env` before
  the first boot. Clearing required keys later brings the config steps back.
- **OCR fails** — check the provider and key in Settings, and the processing job
  error on the document detail page. For Google Vision, make sure the Vision API
  is enabled for the project.
- **Local OCR times out** — raise `OCR_TIMEOUT_SEC`. On an instance that has
  already booted it must be raised in **Settings**, not `.env`: it is a seeded
  setting, so the environment applies only on the first boot. See
  [Local OCR](/local_ocr#what-it-costs).
- **AI extraction fails** — check that an extraction model is bound and that it
  is a chat model, not an embedding or OCR model.
- **An `opencode` model answers every request with a 500 or a 404** — check that
  the SDK really is `opencode` and not `openai` with `AI_BASE_URL` pointed at it.
  Opencode Go serves each model on one of three endpoints, and which one is a
  property of the model that its catalogue does not report, so the `opencode`
  SDK carries the table. On any other SDK every model goes to
  `/chat/completions`, and two thirds of the catalogue is not there.

  An install that predates the SDK needs no edit: `AI_SDK=openai` with a base
  URL addressing `opencode.ai` is *read* as `AI_SDK=opencode`, and the provider
  row it seeded is moved onto that SDK by migration `1730000026`. Both halves
  matter — a managed instance re-applies its environment on every boot, so the
  row alone would be moved straight back. So this only bites a row created by
  hand afterwards, or one pointed at Opencode through a URL that hides the
  host. The same applies to the `x-opencode-session` header, which Opencode
  requires and which only the `opencode` SDK sends.

  The routing, for reference. A model Lemmary has not heard of goes to
  `/chat/completions`, which is the endpoint whose refusal names the problem:

  | Endpoint | Models |
  | --- | --- |
  | `/chat/completions` | `glm-*`, `kimi-*`, `longcat-*`, `deepseek-*`, `mimo-*`, `hy*`, `omen-*` |
  | `/responses` | `grok-*`, `gpt-5.6-luna`, `muse-spark-*` |
  | `/messages` (Anthropic's API) | `minimax-*`, `qwen*` |

- **A `gpt-5`-family model refuses a Deep Search request** — those models set
  `reasoning_effort` themselves and then reject the request because function
  tools are present. Lemmary handles it without configuration: the request is
  translated to the *Responses* API, which keeps both the tools and the
  reasoning, and falls back to `reasoning_effort=none` only if that endpoint
  cannot serve it either. The log line is `model rejected reasoning_effort with
  function tools; retrying on the Responses API`. What is learned is remembered
  per provider, so the same model name behind two providers is discovered
  separately.
- **A managed instance will not start** — the log names the missing or invalid
  variable in the provider block; nothing inside the instance can repair it.
- **Local Embeddings embeds nothing** — `docker compose logs embeddings`. On a
  first boot it is downloading weights and the container is unhealthy until that
  finishes, which is why the app waits on its healthcheck; embed steps fail soft
  meanwhile and are retried, so no document is lost. A `413` in the app's log
  means the endpoint's batch limits are below what Lemmary sends — see the flags
  above.
- **The embedding model picker is empty for Local Embeddings** — the catalogue
  comes from the sidecar's `/info`, which also reports what kind of model it is.
  A reranker or a classifier is deliberately not offered: bound as an embedding
  model it would fail on every document.
