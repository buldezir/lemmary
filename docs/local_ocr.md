# Local OCR

Every other OCR provider Lemmary speaks to reads your documents on somebody
else's hardware. This one does not: a second container beside the app, on the
same Docker network, with no port published and no API key. Scans never leave
the host.

That is the whole benefit, and it is worth being plain about the price: the
images are large, the first document is slow, and a page takes seconds rather
than milliseconds. If your archive is not sensitive and you are happy paying a
hosted provider, [Mistral](/ai_providers) is faster and less to run.

## Two engines

| | `docling` | `paddleocr` |
| --- | --- | --- |
| Project | [docling-serve](https://github.com/docling-project/docling-serve) | [PaddleX serving](https://paddlepaddle.github.io/PaddleX/latest/en/pipeline_deploy/serving.html) running PP-StructureV3 |
| Reads | PDF, images, DOCX, PPTX, XLSX, HTML | PDF and images only |
| Output | markdown, with tables and reading order | markdown, with tables and reading order |
| Image | `ghcr.io/docling-project/docling-serve-cpu`, public | Baidu's registry, `ccr-2vdh3abv-pub.cnc.bj.baidubce.com` |
| Strength | the general answer, and the only one that reads office files | dense and CJK scans |

**Start with `docling`.** Its image comes from a public registry, it reads every
format Lemmary accepts, and its API has been stable. Reach for `paddleocr` when
Docling's recognition is not good enough on your particular documents — dense
tabular scans and Chinese, Japanese or Korean text are where it wins.

Docling can also run PaddleOCR's recognition models underneath, via the RapidOCR
engine, without the second container. See [Choosing an engine](#choosing-an-engine).

## Bringing it up

```bash
docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml \
  --profile docling up -d
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

For PaddleOCR, swap `docling` for `paddleocr` in both the profile and the two
variables, and read [the response-shape caveat](#a-note-on-paddlex-versions).

## What it costs

**Disk.** The Docling CPU image is about 4.4 GB on arm64 and 8.7 GB on amd64.
The PaddleX image is comparable. Model weights are downloaded on first use into
a named volume, another gigabyte or so; without that volume they would be
downloaded again after every `docker compose down`.

**Memory.** Budget 4 GB for Docling and 6 GB for PaddleOCR on top of what the
app already uses — the `mem_limit` lines in the overlay say as much. Under
[encryption at rest](/encryption) that is *on top of* the tmpfs holding the
decrypted archive, so a host sized for 3 GB before will not do.

**Time.** A scanned page is seconds to tens of seconds on CPU, against
milliseconds of latency for a hosted API. The first request after a container
start is worse still: the models load then.

**Time, again — this is the setting people miss.** `OCR_TIMEOUT_SEC` defaults
to 40 seconds, which is not enough:

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
ten seconds a page a local engine would need three hours for one of those, far
past any sane `WORKER_TIMEOUT_SEC`. Work out your own seconds-per-page from the
first few documents and size the timeout from that.

**Concurrency.** The app sends one page at a time to a local engine, on purpose.
The split detector normally reads four pages in parallel, which hides network
latency for a hosted provider; against a sidecar sharing this host's CPUs it
would only queue, while each page's timeout ran down waiting for a core. So a
40-page split detection is roughly forty times one page, serially. Give it time.

## Choosing an engine

Bind an **OCR model** in **Settings → Models** to change what runs inside the
sidecar. It is optional — blank uses the container's default, which is the right
answer for most installs.

For `docling` it names the OCR engine:

| value | notes |
| --- | --- |
| *(blank)* | the container's default |
| `rapidocr` | ONNX builds of PaddleOCR's models — PaddleOCR's recognition without the second container |
| `easyocr` | broad language coverage |
| `tesseract`, `tesserocr` | the classic; fastest, weakest on messy scans |

Lemmary sends this as docling's `ocr_engine` form field, which is what the
pinned image accepts. Newer Docling releases renamed it `ocr_preset` and kept
`ocr_engine` working as a deprecated alias that forwards to it, so the same
values keep working across an image bump.

For `paddleocr` it names the served pipeline, which is also the endpoint:
`pp-structurev3` (the default — markdown with tables and reading order) or `ocr`
(faster, plain recognised lines with no layout).

## GPU

Docling publishes CUDA images: `docling-serve-cu128` and `docling-serve-cu130`.
Swap the `image:` line in the overlay, add a `deploy.resources.reservations.devices`
block for the GPU, and per-page time drops by an order of magnitude. The app
needs no change — it is the same HTTP API.

## A note on PaddleX versions

PaddleX's serving response has been spelled several ways across releases: 3.x
returns the text under `layoutParsingResults[].markdown.text`, earlier builds
used `layoutElements[].text`, and the plain `ocr` pipeline returns neither, only
recognised lines. Lemmary reads all of them and takes the first that yields
text, so an image bump is unlikely to break silently — but pin the tag in the
overlay anyway rather than tracking `latest`.

## Troubleshooting

- **Documents fail with a timeout.** Raise `OCR_TIMEOUT_SEC` in **Settings**,
  not in `.env`, unless this is a first boot. See [What it costs](#what-it-costs).
- **The first document after a restart fails, later ones work.** The models were
  still loading. Check that the named volume is mounted — `docker compose config`
  will show it — and give the container a few minutes after `up`.
- **`connection refused` naming `docling`.** The sidecar is not running, or is
  running without its profile. `docker compose ps` should list it; the command
  needs `--profile docling`.
- **A DOCX or PPTX fails on `paddleocr`.** It reads pixels, not office files.
  Use `docling`, which reads both. TXT, CSV, DOCX and XLSX never reach an OCR
  provider at all — Lemmary parses those itself.
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
