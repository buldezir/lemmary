# Local OCR and embeddings on an Apple Silicon Mac

[Local OCR](/local_ocr) and [Local embeddings](/local_embeddings) run on your
own hardware, so documents never leave your network. This guide runs both on
an M-series Mac, natively under launchd, using the Mac's GPU and Neural
Engine. Lemmary itself runs on a different host (a VM, a Linux server,
anything with Docker) and reaches the Mac over the LAN.

Tested on a base Mac mini M4 (16 GB): both services run side by side and are
fast — a few seconds per scanned page, and a full batch of embeddings in
about 4 s. See [Measured performance](#measured-performance).

Lemmary needs no change: embeddings use its Local Embeddings SDK (`local`,
any OpenAI-compatible `/v1/embeddings`) and OCR its Local OCR SDK
(`docling`).

## Placeholders

| Placeholder | Meaning | Example |
|---|---|---|
| `<MAC_IP>` | The Mac's stable LAN address (use the wired one if it has several) | `192.168.1.20` |
| `<USER>` | The macOS user the services run as | `alice` |
| `com.example` | Reverse-DNS prefix for the launchd labels | your own domain |

## Overview

| | Embeddings | OCR |
|---|---|---|
| Lemmary SDK | `local` (OpenAI-compatible `/v1/embeddings`) | `docling` |
| Server | llama.cpp `llama-server` (Homebrew), Metal | docling-serve 1.35.0 (uv tool), MPS |
| Model / engine | `BAAI/bge-m3`, F16 GGUF | Apple Vision via `ocrmac`, docling layout/table models on MPS |
| Endpoint | `http://<MAC_IP>:8080/v1` | `http://<MAC_IP>:5001` |
| Auth | none | none |
| LaunchAgent | `com.example.llama-embeddings` | `com.example.docling-serve` |
| Resident memory (measured) | ~4.5 GB | ~3.9 GB |
| Log | `~/ai/logs/llama-embeddings.log` | `~/ai/logs/docling-serve.log` |

## Host requirements

- Apple Silicon Mac. The reference machine is a Mac mini M4 (4P + 6E cores,
  10-core GPU) with 16 GB, on macOS 26. With both services loaded, 16 GB
  leaves room for a CI runner or similar alongside.
- A stable IP. If the Mac has both wired and Wi-Fi addresses, use the wired
  one; the services listen on all interfaces.
- The services are **LaunchAgents**, so they run inside a user's login
  session. For a headless server, enable automatic login for `<USER>`, and
  set the Mac to never sleep and to restart after a power failure (System
  Settings → Energy).
- Homebrew.

## Why this setup

- **Native, not Docker.** Docker on macOS (Docker Desktop, OrbStack, Colima)
  runs a Linux VM with no access to the Apple GPU, so everything in a
  container would run on CPU.
- **llama.cpp, not TEI, for embeddings.** Lemmary's compose files use
  text-embeddings-inference. TEI has a Metal build
  (`brew install text-embeddings-inference`), but on the reference Mac it
  managed about 1k tokens/s against about 4k tokens/s for llama.cpp on the
  same model.
- **docling with Apple Vision for OCR.** It is the SDK Lemmary already
  speaks, so no adapter is needed. docling's `auto` engine picks `ocrmac` on
  macOS by itself.

## Layout

```
~/ai/
  models/bge-m3-f16.gguf        embeddings model (1.1 GB)
  docling/                      docling-serve working directory
  embeddings/embtest.py         embeddings smoke test / benchmark
  logs/                         service logs
~/.local/share/uv/tools/docling-serve/   docling-serve venv (Python 3.12, ~2.1 GB)
~/.cache/huggingface/                     docling layout/table models (~0.5 GB)
~/Library/LaunchAgents/com.example.llama-embeddings.plist
~/Library/LaunchAgents/com.example.docling-serve.plist
```

## Installation

### 1. Packages

```sh
brew install llama.cpp uv poppler     # poppler only for pdfinfo/pdfimages when testing
mkdir -p ~/ai/models ~/ai/logs ~/ai/docling ~/ai/embeddings
```

If Xcode is installed but its license has not been accepted, Homebrew
complains. Bottles still install; to silence it, run
`sudo xcodebuild -license accept` once.

### 2. Embeddings: llama.cpp + bge-m3

```sh
curl -fL -o ~/ai/models/bge-m3-f16.gguf \
  https://huggingface.co/gpustack/bge-m3-GGUF/resolve/main/bge-m3-FP16.gguf
```

Create `~/Library/LaunchAgents/com.example.llama-embeddings.plist`. launchd
does not expand `~`, so replace `<USER>` with the real user name:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.llama-embeddings</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/llama-server</string>
    <string>-m</string><string>/Users/<USER>/ai/models/bge-m3-f16.gguf</string>
    <string>--alias</string><string>BAAI/bge-m3</string>
    <string>--embeddings</string>
    <string>--pooling</string><string>cls</string>
    <string>-ngl</string><string>99</string>
    <string>-fa</string><string>on</string>
    <string>-c</string><string>8192</string>
    <string>-b</string><string>8192</string>
    <string>-ub</string><string>8192</string>
    <string>-np</string><string>1</string>
    <string>--host</string><string>0.0.0.0</string>
    <string>--port</string><string>8080</string>
    <string>--no-webui</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>/Users/<USER>/ai/logs/llama-embeddings.log</string>
  <key>StandardErrorPath</key><string>/Users/<USER>/ai/logs/llama-embeddings.log</string>
</dict>
</plist>
```

Why these flags:

- `--pooling cls`: bge-m3's dense embedding is the CLS token; llama.cpp then
  L2-normalizes it, the same as TEI.
- `-ub 8192`: an encoder model must fit each input in one micro-batch. Lemmary
  caps one input at 8000 characters, which is up to about 5.7k tokens in dense
  CJK text; bge-m3's window is 8192. A smaller `-ub` fails long inputs instead
  of embedding them.
- `-fa on`: flash attention keeps the 8192-token attention matrix from being
  materialized, and on Metal it is also faster (about 4k tokens/s at 512 tokens
  and 2.4k at 4k, against 3.8k and 1.7k without).
- `-np 1`: four slots were only about 5% faster on a 64-chunk batch.
- `--alias BAAI/bge-m3`: this is the model name Lemmary sends and shows.
- F16 rather than Q8_0: the same speed on Metal, and closer to the original
  weights.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.llama-embeddings.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.llama-embeddings.plist
curl -s localhost:8080/health        # {"status":"ok"}
```

### 3. OCR: docling-serve + ocrmac

```sh
uv tool install --python 3.12 docling-serve==1.35.0 --with ocrmac
```

Python 3.12 is used on purpose: there are reports of docling crashing on
Python 3.14 on Apple Silicon. The installed torch should report `mps` as
available:

```sh
~/.local/share/uv/tools/docling-serve/bin/python -c 'import torch; print(torch.backends.mps.is_available())'
```

Create `~/Library/LaunchAgents/com.example.docling-serve.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.docling-serve</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/<USER>/.local/bin/docling-serve</string>
    <string>run</string>
    <string>--host</string><string>0.0.0.0</string>
    <string>--port</string><string>5001</string>
  </array>
  <key>WorkingDirectory</key><string>/Users/<USER>/ai/docling</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>DOCLING_SERVE_MAX_SYNC_WAIT</key><string>900</string>
    <key>DOCLING_SERVE_ENABLE_UI</key><string>false</string>
    <key>UVICORN_WORKERS</key><string>1</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>/Users/<USER>/ai/logs/docling-serve.log</string>
  <key>StandardErrorPath</key><string>/Users/<USER>/ai/logs/docling-serve.log</string>
</dict>
</plist>
```

- `DOCLING_SERVE_MAX_SYNC_WAIT=900`: the synchronous convert endpoint gives up
  after this long regardless of what the client asked for. Keep it above
  Lemmary's `OCR_TIMEOUT_SEC`.
- `UVICORN_WORKERS=1`: a second worker would load a second copy of the
  models for no extra throughput. Lemmary sends docling one request at a time
  anyway.
- A LaunchAgent (logged-in session) rather than a LaunchDaemon: Apple's
  Vision framework is most reliable inside a user session.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.docling-serve.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.docling-serve.plist
curl -s localhost:5001/health        # {"status":"ok"}
curl -s -o /dev/null -w '%{http_code}\n' localhost:5001/ready   # 200 once models are loaded
```

The first conversion downloads docling's layout and table models (about
0.5 GB) into `~/.cache/huggingface` and takes about 40 s. Later ones are warm.
The log should show `Auto OCR model selected ocrmac.` and
`Accelerator device: 'mps'`.

## Lemmary configuration

On an instance that has already booted, the AI settings in `.env` are ignored
(they only seed the first boot). Configure it in the UI instead.

**Settings → AI → Providers**

- **Local Embeddings** (`local`): base URL `http://<MAC_IP>:8080/v1`, no
  API key.
- **Local OCR** (`docling`): base URL `http://<MAC_IP>:5001`, no API key.

**Settings → AI → Models**

- Embeddings: `BAAI/bge-m3`, which the picker fills in for a Local
  Embeddings provider. It matches the `--alias` above.
- OCR: bind the docling provider and leave the OCR model **blank**, which
  means docling's `auto` engine and therefore `ocrmac`. Setting `ocrmac`
  explicitly also works.

**Timeouts:** raise `OCR_TIMEOUT_SEC` from its default of 40 to about 120.
A warm photo page takes 4–8 s, but a long document, or the first request after
a restart, takes longer. Keep it below `DOCLING_SERVE_MAX_SYNC_WAIT` (900).

For a fresh instance the equivalent `.env` is:

```sh
AI_EMBEDDING_SDK=local
AI_EMBEDDING_BASE_URL=http://<MAC_IP>:8080/v1
AI_EMBEDDING_MODEL=BAAI/bge-m3
OCR_SDK=docling
OCR_BASE_URL=http://<MAC_IP>:5001
OCR_TIMEOUT_SEC=120
```

Check reachability from inside the Lemmary container:

```sh
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:8080/health
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:5001/health
```

Changing the embeddings model or server means re-embedding the archive (see
[Changing the model](/local_embeddings#changing-the-model)). Vectors from llama.cpp's bge-m3 are
close to TEI's, but they are not bit-identical.

## Smoke test and benchmark script

Save this as `~/ai/embeddings/embtest.py` and run
`python3 ~/ai/embeddings/embtest.py http://127.0.0.1:8080`:

```python
import json, math, sys, time, urllib.request

URL = sys.argv[1]

def emb(inputs):
    req = urllib.request.Request(URL + "/v1/embeddings",
        data=json.dumps({"model": "BAAI/bge-m3", "input": inputs}).encode(),
        headers={"Content-Type": "application/json"})
    t = time.time()
    return json.load(urllib.request.urlopen(req, timeout=600)), time.time() - t

def cos(a, b):
    return sum(x * y for x, y in zip(a, b)) / math.sqrt(sum(x * x for x in a) * sum(y * y for y in b))

r, _ = emb(["The cat sleeps on the sofa.", "Die Katze schläft auf dem Sofa.",
            "Кошка спит на диване.", "Quarterly VAT return for 2024 filed with the tax office."])
v = [d["embedding"] for d in r["data"]]
print("dim", len(v[0]), "norm", round(math.sqrt(sum(x * x for x in v[0])), 4))
print("en-de", round(cos(v[0], v[1]), 3), "en-ru", round(cos(v[0], v[2]), 3), "en-unrelated", round(cos(v[0], v[3]), 3))

para = "Invoice number 4711 for electricity supply, billing period January to March, total amount 312.45 EUR including VAT, due within fourteen days. " * 6
r, dt = emb([para + str(i) for i in range(64)])
print("64 x ~%d tok: %.2fs" % (r["usage"]["prompt_tokens"] // 64, dt))
```

Expected: `dim 1024 norm 1.0`, en-de about 0.92, en-ru about 0.78, unrelated
about 0.29.

## Measured performance

Warm runs on the reference Mac mini M4 (16 GB).

**Embeddings (bge-m3)**

| Test | llama.cpp, Metal | TEI 1.9.4, Metal |
|---|---|---|
| 64 chunks × ~231 tokens (one Lemmary batch) | **3.9 s** | 15.0 s |
| One 3.2k-token input | **1.2 s** | 3.0 s |

Quality check: English–German similarity 0.917, English–Russian 0.776,
unrelated 0.289; vectors are 1024-dimensional with unit norm.

**OCR (docling + ocrmac)**, on a 2-page phone photo of a German letter
(2402×3586 and 1601×2254 JPEG):

- Page 1: 8.1 s; page 2: 4.1 s; the full 2-page PDF: 14.1 s.
- The first page with cold models: 38 s.
- All amounts matched the source. The text was cleaner than a Tesseract text
  layer (OCRmyPDF) on the same PDF.

## Operations

```sh
# status
launchctl print gui/$(id -u)/com.example.llama-embeddings | grep -E 'state|pid'
launchctl print gui/$(id -u)/com.example.docling-serve   | grep -E 'state|pid'

# restart
launchctl kickstart -k gui/$(id -u)/com.example.llama-embeddings
launchctl kickstart -k gui/$(id -u)/com.example.docling-serve

# stop / remove
launchctl bootout gui/$(id -u)/com.example.docling-serve

# logs
tail -f ~/ai/logs/llama-embeddings.log ~/ai/logs/docling-serve.log

# OCR a file the way Lemmary does
curl -s \
  -F to_formats=md -F do_ocr=true -F image_export_mode=placeholder \
  -F "files=@some.pdf;type=application/pdf" \
  localhost:5001/v1/convert/file | python3 -c 'import json,sys; print(json.load(sys.stdin)["document"]["md_content"])'
```

**Updating**

- llama.cpp: `brew upgrade llama.cpp`, then restart the embeddings agent.
- docling-serve: pinned to 1.35.0. To upgrade, run
  `uv tool install --python 3.12 docling-serve==<new> --with ocrmac --force`,
  restart the agent, and run one test conversion. Lemmary sends `ocr_engine`
  only when an engine is bound, which keeps image bumps from turning into
  422 errors.

## Troubleshooting

- **Lemmary says `connection refused`:** check the agent's status (above),
  whether `<USER>` is logged in (`who` shows `console`).
- **The first OCR document after a restart times out:** the models were still
  loading. Wait for `/ready` to return 200, or raise `OCR_TIMEOUT_SEC`.
- **Embeddings fail on very long inputs:** `-ub` is smaller than the input.
  Keep it at or above 6144; 8192 is the model's maximum.
- **Memory:** together the services use about 8.5 GB. On a 16 GB Mac with
  other heavy workloads, lowering `-ub` to 6144 shrinks llama.cpp's compute
  buffers; on an 8 GB Mac, run only one of the two services.
- **Wrong IP:** a Wi-Fi address can change. Point Lemmary at the wired
  address, or give the Mac a DHCP reservation.

## Upgrade path if Apple's OCR is not good enough

For hard scans, handwriting or complex tables, a vision-language OCR model
(GLM-OCR, PaddleOCR-VL) runs well on Apple Silicon through `mlx-vlm`
(`mlx_vlm.server`, OpenAI-compatible). Lemmary would reach it through its
`openai` OCR SDK. That SDK sends PDFs as raw file parts, which local VLM
servers reject, so it needs a small adapter that turns pages into images
first. Expect tens of seconds per page rather than a few seconds.
