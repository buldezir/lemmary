# Lemmary Local AI on Linux

[Lemmary Local AI](https://github.com/buldezir/lemmary-local-ai) for Linux runs
Lemmary's embeddings, OCR and an optional text model on a machine with an
NVIDIA GPU, so documents never leave your network. It is a single program or a
Docker image that answers the same API as [the Mac app](/local_ai_macos), so
Lemmary can move between the two without re-embedding its archive.

**[Download the latest release](https://github.com/buldezir/lemmary-local-ai/releases/latest)**

## What it serves

All three answer on one port, `5001`, behind one OpenAI-compatible `/v1`:

| Job | Model | Engine |
| --- | --- | --- |
| Embeddings | `BAAI/bge-m3` | llama.cpp on the GPU |
| OCR | `lightonocr-2` | LightOnOCR-2 1B on llama.cpp, two pages at a time |
| Extraction (optional) | Gemma 4 E4B on a 16 GB GPU, or a larger model on 24 GB | a third llama.cpp |

It downloads and checks the models on first start, and restarts a llama.cpp
that stops.

The text model is meant for extraction (title, date, type, correspondent, tags,
summary). Extraction runs on the **General AI** model, which also answers Ask AI
and AI assisted search, and a model this size is weak at the tool use and long
conversations those need. So binding it as General AI puts chat on it too, and
keeping chat on a hosted model keeps extraction there as well. Either way, give
Deep Research's **Advanced** model a hosted one.

## Requirements

- An NVIDIA GPU with driver 570 or newer. About 5 GB of VRAM for embeddings and
  OCR; the text model needs a 16 or 24 GB GPU, depending on the model.
- Docker with GPU access, or, without Docker, a distribution with glibc 2.38 or
  newer, such as Ubuntu 24.04 or Debian 13.
- OCR takes about 3.6 s a page, so Lemmary's `OCR_TIMEOUT_SEC` (90 s) covers
  about 25 pages; raise it for longer documents.

```bash
docker run --gpus all -p 5001:5001 -v lemmary-local-ai:/data \
  ghcr.io/buldezir/lemmary-local-ai:latest -text-model gemma-4-e4b
```

## In Lemmary

In **Admin → AI → Providers**, add one provider of type **Lemmary Local AI**:

- base URL: `http://<host>:5001/v1`;
- no API key.

Then bind models in **Admin → AI → Models**. Each picker lists only the models
for its job: `BAAI/bge-m3` for embeddings, `lightonocr-2` for OCR, and the text
model, while it is on, for General AI.

On a fresh install, choosing **Fully local** in the setup wizard does both: it
asks for the address alone and arrives at the models step with all three bound.

Its [README](https://github.com/buldezir/lemmary-local-ai/blob/main/linux/README.md)
covers the settings, running it without Docker or as a systemd service, and the
OCR and text models compared.
