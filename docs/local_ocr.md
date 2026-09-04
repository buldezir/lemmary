# Local OCR

Every other OCR provider Lemmary speaks to reads your documents on somebody
else's hardware. This one does not: a second container beside the app, on the
same Docker network, with no port published and no API key. Scans never leave
the host.

That is the whole benefit, and it is worth being plain about the price: the
images are large, the first document is slow, and a page takes seconds rather
than milliseconds. If your archive is not sensitive and you are happy paying a
hosted provider, [Mistral](/ai_providers) is faster and less to run.

## One container, several engines

The SDK is `docling`, and the container is
[docling-serve](https://github.com/docling-project/docling-serve): a public
image on GHCR that reads PDF, images, DOCX, PPTX, XLSX and HTML, and returns
markdown with layout and tables.

There is deliberately no separate PaddleOCR SDK, because you already have
PaddleOCR. Docling's default recognition engine is RapidOCR, which is
PaddleOCR's own PP-OCR models exported to ONNX — a fresh container downloads
`ch_PP-OCRv4_det_mobile.onnx` and `ch_PP-OCRv4_rec_mobile.onnx` and reads with
those. A second multi-gigabyte container to run the same models would buy
nothing, and PaddleOCR's own serving images are published only on Baidu's
registry. If you want a different recognizer, change the engine rather than the
container: see [Choosing an engine](#choosing-an-engine).

## Bringing it up

```bash
docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml up -d
```

Both services sit behind a compose profile, so the command has to name one.
Without that, `up` would pull both multi-gigabyte images for someone who wanted
a single engine.

Then two lines in `.env`:

```bash
OCR_SDK=docling
OCR_BASE_URL=http://docling:5001
```

`OCR_BASE_URL` is optional — that value is the default for `OCR_SDK=docling`,
because it is the service name the overlay gives the container. Set it only if
you moved the sidecar somewhere else.

There is deliberately no `OCR_API_KEY`. These SDKs are keyless: the address is
the whole configuration, which is why the sidecar publishes no port and is
reachable only from the app container. If you expose it further, start
docling-serve with `DOCLING_SERVE_API_KEY` and put the same value in
`OCR_API_KEY` — it is sent as `X-Api-Key`.

On an instance that has already booted, `.env` is not the place: the AI block
seeds the database on the **first** boot only. Add the provider under
**Settings → Providers** instead (the base URL is prefilled and there is no API
key field), then bind it under **Settings → Models → OCR**.

## What it costs

Measured on an amd64 host, 10 cores, running the overlay as shipped. Your
numbers will differ, but the shape will not.

**Disk.** The CPU image is 7.1 GB pulled on amd64 (smaller on arm64).
On first boot it downloads about 1.3 GB of model weights into the named volume;
without that volume it would download them again for every new container.

**Memory.** Docling settled at **1.1 GB** resident against the overlay's 4 GB
`mem_limit` — the headroom is for larger documents, not for idling. Under
[encryption at rest](/encryption) this is *on top of* the tmpfs holding the
decrypted archive, so a host sized for 3 GB before will not do.

**Startup.** About **90 seconds** on a cold volume, almost all of it model
download; **10 seconds** on a warm one. That is what the overlay's
`start_period: 180s` covers, and why the volume is not optional.

**Time per document.** On this host, a one-page born-digital PDF took **2 s**
and a one-page 150 dpi scan **4 s** — against tens of milliseconds for a hosted
API. Real scans are denser than a test fixture, so treat those as a floor and
measure your own.

**The setting people miss.** `OCR_TIMEOUT_SEC` defaults to 40 seconds. That
survives a small scan on a fast host and nothing else — a dense multi-page
document, a busy machine, or the first request after a restart will all exceed
it:

```bash
OCR_TIMEOUT_SEC=300
WORKER_TIMEOUT_SEC=1800
```

Both are **seeded on the first boot only**. On an instance that has already run,
the variables are ignored and the values are changed in **Settings** instead.
This is the most common reason a working sidecar produces failed documents.

The sidecar has a matching ceiling of its own: docling-serve abandons a
synchronous conversion after `DOCLING_SERVE_MAX_SYNC_WAIT` seconds regardless of
what the client asked for. The overlay sets it to 900. Keep it above
`OCR_TIMEOUT_SEC`.

**The practical page ceiling is the worker timeout, not the page limit.**
Lemmary refuses documents over 1000 pages, a limit that comes from Mistral. At
even four seconds a page a local engine needs over an hour for one of those,
far past any sane `WORKER_TIMEOUT_SEC`. Work out your own seconds-per-page from
the first few documents and size the timeout from that.

**Concurrency.** The app sends one page at a time to a local engine, on purpose.
The split detector normally reads four pages in parallel, which hides network
latency for a hosted provider; against a sidecar sharing this host's CPUs it
would only queue, while each page's timeout ran down waiting for a core. So a
40-page split detection is roughly forty times one page, serially. Give it time.

## Choosing an engine

Bind an **OCR model** in **Settings → Models** to change what runs inside the
sidecar. It is optional — blank uses the container's default, which is the right
answer for most installs. For this SDK the field names an OCR engine rather than
a model:

| value | notes |
| --- | --- |
| *(blank)* | the container's default |
| `rapidocr` | the default: PaddleOCR's PP-OCR models as ONNX |
| `easyocr` | broad language coverage |
| `tesseract`, `tesserocr` | the classic; fastest, weakest on messy scans |

Lemmary sends this as docling's `ocr_engine` form field. The pinned image
accepts both that and its successor `ocr_preset`, and marks `ocr_engine`
deprecated — it still forwards, so the same values keep working across an image
bump until it is removed.

**A name it does not recognise is accepted silently.** The field is a free
string rather than an enum: binding `tesserract` returns HTTP 200, falls back to
the default engine, and reports nothing. If a bound engine seems to make no
difference, check the spelling against the table above — nothing else will tell
you.

## GPU

Docling publishes CUDA images: `docling-serve-cu128` and `docling-serve-cu130`.
Swap the `image:` line in the overlay, add a `deploy.resources.reservations.devices`
block for the GPU, and per-page time drops by an order of magnitude. The app
needs no change — it is the same HTTP API.

## Troubleshooting

- **Documents fail with a timeout.** Raise `OCR_TIMEOUT_SEC` in **Settings**,
  not in `.env`, unless this is a first boot. See [What it costs](#what-it-costs).
- **The first document after a restart fails, later ones work.** The models were
  still loading. Check that the named volume is mounted — `docker compose config`
  will show it — and give the container a few minutes after `up`.
- **`connection refused` naming `docling`.** The sidecar is not running, or is
  running without its profile. `docker compose ps` should list it; the command
  needs `--profile docling`.
- **`HTTP 401: Unauthorized`.** The sidecar was started with
  `DOCLING_SERVE_API_KEY` but the provider has no key. Put the same value in
  `OCR_API_KEY`, or in the provider's API key field in **Settings**.
- **TXT, CSV, DOCX and XLSX never reach the sidecar.** Lemmary parses those
  itself, so a problem with one of them is not an OCR problem.
- **Try it before trusting it.** The **OCR test** page runs one file through a
  provider without touching the pipeline. A local sidecar appears there as soon
  as it is added under **Providers**, before it is bound to OCR.

## One thing encryption does not cover

With `VAULT_ENABLED=1` the archive on disk is ciphertext, and the decrypted copy
lives only in a tmpfs. The document sent to the sidecar is cleartext, over the
compose network, and the sidecar's own scratch space is not covered by the
vault. That is a much smaller exposure than uploading it to a hosted API — it
never leaves the host — but it is not nothing. See
[Encryption at rest](/encryption).
