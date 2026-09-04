# Local embeddings

Dense retrieval is the one AI job in Lemmary that does not need a frontier
model. A 500 MB – 2 GB model on your own CPU is competitive with
`text-embedding-3-small`, and unlike OCR or extraction it runs on every passage
of every document you own — so a hosted provider means sending the whole
archive out, and sending it again whenever you edit a title or change the
model.

Running it yourself removes the token bill and the per-document price entirely.
What it does not remove is the work: the same forward passes happen on your
hardware, and the first backfill of a large archive is the one time that is
slow enough to notice. Steady state — a handful of documents an upload — is
comfortably within a couple of cores. For the full shape of the bill either
way, see [what embeddings cost](/ai_providers#what-embeddings-cost).

The SDK is `local`: any OpenAI-compatible `/v1/embeddings` endpoint you run
yourself. It embeds and nothing else — it is refused as `AI_SDK` and `OCR_SDK` —
and like [docling](/local_ocr) it takes no API key, because a service on a
private network has nobody to authenticate to. Its address is its whole
configuration.

## Bringing it up

Two compose overlays run [text-embeddings-inference][tei] as a sidecar, and
nothing in the base file changes.

They are not quite twins, so check which one you are using:

| | CPU (`docker-compose.embeddings.yml`) | GPU (`docker-compose.embeddings-gpu.yml`) |
| --- | --- | --- |
| Binds the app to the sidecar | no — the `app` block is commented out | yes, all three variables |
| Published port | `127.0.0.1:8999` for debugging | none; reachable only on the compose network |

On the GPU overlay that is the whole configuration on a fresh volume: the
instance comes up with a **Local embeddings** provider already bound. On the CPU
overlay you set the three variables yourself — uncomment the `app` block in the
file, or put them in `.env`:

```bash
AI_EMBEDDING_SDK=local
AI_EMBEDDING_BASE_URL=http://embeddings:80/v1
AI_EMBEDDING_MODEL=BAAI/bge-m3
```

The CPU overlay's published port is bound to loopback on purpose. TEI has no
authentication — that is the point of a service nothing outside the compose
network can reach — so a bare `8999:80` would bind `0.0.0.0` and put an
unauthenticated inference API on the LAN. Nothing in Lemmary uses the port; it
is there for `curl 127.0.0.1:8999/info` while you are setting the model up, and
you can delete the line.

Neither image is multi-arch. The `cpu-*` tags are `linux/amd64`, and there is no
`cpu-arm64-1.9` — the arm64 line is published unversioned as
`cpu-arm64-latest`. On an arm64 host swap the tag and accept that it is not
pinned.

[tei]: https://github.com/huggingface/text-embeddings-inference

```bash
# CPU, runs anywhere
docker compose -f docker-compose.yml -f docker-compose.embeddings.yml up

# NVIDIA GPU -- instead of the CPU file, not alongside it. Pick the image tag
# for your card; the file lists them.
docker compose -f docker-compose.yml -f docker-compose.embeddings-gpu.yml up
```

Both compose with [encryption at rest](/encryption), which only touches the
`app` service:

```bash
docker compose -f docker-compose.yml \
               -f docker-compose.encrypted.yml \
               -f docker-compose.embeddings.yml up
```

Either way, **Settings → Models** then shows the one model the sidecar is
serving.

## Choosing a model

`EMBEDDINGS_MODEL` names it once, for both the sidecar and the binding.

| Model | Dimensions | Window | Weights | |
| --- | --- | --- | --- | --- |
| `BAAI/bge-m3` (the default) | 1024 | 8192 tokens | ~2.2 GB | Strongly multilingual, which is the point: crossing languages in one search is what dense retrieval buys you. Slow on a small CPU. |
| `intfloat/multilingual-e5-small` | 384 | 512 tokens | ~470 MB | Fast, and a quarter of the index RAM. Weaker retrieval, and our ~1100-character passages sit near its truncation limit. |

Both cost less space than a hosted 1536-dimension model, which matters most
under `VAULT_ENABLED=1` where the index lives in a tmpfs. Anything
text-embeddings-inference supports will work; those two are the ends of the
range worth starting from.

The sidecar is started with `--auto-truncate` and `--max-client-batch-size=64`,
which is the batch size Lemmary sends. A third-party endpoint left at TEI's
default of 32 answers `413` on a full batch, which fails the document loudly
rather than retrying.

`--max-batch-tokens` is a separate lever and the one that decides how much
memory the sidecar needs. It is TEI's forward-pass budget, not a request-size
guard, and on CPU it dominates resident memory. The overlay ships **6144**,
chosen against the longest input Lemmary can send: a single chunk is capped at
8000 runes, which is 5723 tokens in the densest script measured. Below that
figure TEI truncates silently — at 4096 a 5723-token input embeds
byte-identically to its first 4096 tokens, with a `200` and no warning.

Budget host memory accordingly, because exceeding it is an OOM kill during
warmup rather than a clean error. Measured for `bge-m3` on CPU:

| `--max-batch-tokens` | Peak resident | |
| --- | --- | --- |
| 8192 | — | Matches the model's own window, so nothing is ever truncated. Needs more RAM than a 16 GB host has free. |
| **6144** | **~8.1 GB** | The default. Covers the 8000-rune input cap in every script tested. |
| 4096 | ~6.5 GB | Fits a smaller host, but truncates dense passages without saying so. |

The overlay also sets `mem_limit: 10g`. Warmup does not come near it; it is
there so that an overshoot kills the sidecar instead of whatever else the host
is running, since the kernel's own OOM killer picks by size and not by
importance. A sidecar that dies is a soft failure — documents keep their text,
their metadata and their place in keyword search, and are retried with a
backoff. On a host with less memory to spare, move to a smaller model rather
than lowering `--max-batch-tokens` past the truncation floor.

## Changing the model

Vectors from two models cannot be compared, so switching is a re-embed of the
whole archive — and on a self-hosted instance it takes **two** steps, because
outside `AI_MANAGED=1` the environment seeds the database on the first boot only:

1. Change `EMBEDDINGS_MODEL` and recreate the sidecar.
2. Change the model in **Settings → Models** as well.

Doing only the first leaves the binding naming the old model against a sidecar
serving the new one. That is caught rather than silently wrong — the app records
the vector length from the provider's first response and refuses a later
disagreement — but the archive stops embedding until the binding is corrected.
Changing it in Settings resets the recorded dimensions and rebuilds the chunk
index, which is what a model switch actually requires. Under `AI_MANAGED=1` the
environment is authoritative on every boot, so step 1 is enough.
