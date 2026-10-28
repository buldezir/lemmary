# Lemmary Local AI on a Mac

[Lemmary Local AI](https://github.com/buldezir/lemmary-local-ai) is a macOS menu
bar app that runs Lemmary's embeddings, OCR and an optional text model on an
Apple Silicon Mac, so documents never leave your network. Lemmary itself runs
elsewhere (a VM, a Linux server, anything with Docker) and reaches the Mac over
the LAN.

**[Download the latest release](https://github.com/buldezir/lemmary-local-ai/releases/latest)**

## What it serves

All three answer on one port, `5001`, behind one OpenAI-compatible `/v1`:

| Job | Model | Engine |
| --- | --- | --- |
| Embeddings | `BAAI/bge-m3` | llama.cpp on the Mac's GPU, bundled |
| OCR | `apple-vision-ocr` | Apple Vision, built into macOS |
| Extraction (optional) | Gemma 4 E4B, or a larger model on a Mac with 24 or 32 GB | a second llama.cpp |

It downloads and checks the models on first run, starts at login and updates
itself.

The text model is meant for extraction (title, date, type, correspondent, tags,
summary). Extraction runs on the **General AI** model, which also answers Ask AI
and AI assisted search, and a model this size is weak at the tool use and long
conversations those need. So binding it as General AI puts chat on it too, and
keeping chat on a hosted model keeps extraction there as well. Either way, give
Deep Research's **Advanced** model a hosted one.

## Requirements

- Apple Silicon, macOS 26 or newer.
- About 1.5 GB of memory for embeddings and OCR. The text model adds 5.7 to
  18.7 GB, depending on the model.
- It runs in a logged-in session: on a headless Mac, turn on automatic login
  and never sleep.

## In Lemmary

In **Admin → AI → Providers**, add one provider of type **Lemmary Local AI**:

- base URL: the one its menu bar panel shows, e.g. `http://<mac>:5001/v1`;
- no API key.

Then bind models in **Admin → AI → Models**. Each picker lists only the models
for its job: `BAAI/bge-m3` for embeddings, `apple-vision-ocr` for OCR, and the
text model, while it is on, for General AI.

Its [README](https://github.com/buldezir/lemmary-local-ai#readme) covers the
settings, the text models compared, and moving from a hand-built llama.cpp and
docling setup.
