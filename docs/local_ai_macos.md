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
  10-core GPU) with 16 GB, on macOS 26 and later 27.0. With both services
  loaded, 16 GB leaves room for a CI runner or similar alongside. macOS 27.0
  needs the [ocrmac patch](#_4-ocr-patch-ocrmac-against-the-vision-hang).
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
  bin/docling-watchdog.py       OCR watchdog
  docling/                      docling-serve working directory
  embeddings/embtest.py         embeddings smoke test / benchmark
  logs/                         service logs
~/.local/share/uv/tools/docling-serve/   docling-serve venv (Python 3.12, ~2.1 GB)
~/.cache/huggingface/                     docling layout/table models (~0.5 GB)
~/Library/LaunchAgents/com.example.llama-embeddings.plist
~/Library/LaunchAgents/com.example.docling-serve.plist
~/Library/LaunchAgents/com.example.docling-watchdog.plist
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

### 4. OCR: patch ocrmac against the Vision hang

On macOS 27.0 (build 26A428), Apple Vision's accurate text recognizer
sometimes never returns. It hangs when explicit recognition languages and
language correction are both set, which is how docling calls it through
`ocrmac`. Nothing is logged and no error comes back. Because Vision runs one
recognition at a time per process, every later OCR request queues behind the
stuck one. docling-serve keeps answering `/health`, so launchd never restarts
it, and Lemmary's OCR jobs fail one after another on `OCR_TIMEOUT_SEC`.

It isn't tied to the document: the image that hung went through on the next
try. Forcing Vision onto the CPU or GPU hangs as well, so it isn't the Neural
Engine either. A stress test calling Vision directly, without docling, hung
within 7 to 431 calls.

Turning off language correction avoids it. With the patch the same stress
test ran 10,000 calls without a hang, and the text from German invoices was
identical. `ocrmac` has no option for it, so patch the installed copy:

```sh
~/.local/share/uv/tools/docling-serve/bin/python - <<'EOF'
import ocrmac.ocrmac as m
p, anchor = m.__file__, "            req.setRecognitionLevel_(0)\n"
s = open(p).read()
if "setUsesLanguageCorrection_(False)" in s:
    print("already patched")
else:
    assert s.count(anchor) == 1, "ocrmac changed upstream, patch it by hand"
    open(p, "w").write(s.replace(anchor, anchor + "\n        req.setUsesLanguageCorrection_(False)\n"))
    print("patched", p)
EOF
launchctl kickstart -k gui/$(id -u)/com.example.docling-serve
```

The patch lives in the venv, so `uv tool install ... --force` removes it. The
watchdog in the next step puts it back. Without the watchdog, rerun the
snippet after every upgrade. The patch does no harm on macOS versions without
the bug.

### 5. OCR: watchdog

The patch prevents the known hang. The watchdog also recovers from any other
one, and keeps the patch in place across upgrades. A LaunchAgent runs it every
minute. On each run it:

- re-applies the ocrmac patch if an upgrade removed it, then restarts
  docling-serve as soon as no conversion is running;
- restarts docling-serve when a conversion has been running for 2 minutes
  while the server used almost no CPU (under 2 s since the previous run), or
  for 20 minutes no matter what. Before restarting a hung server it saves a
  3-second `sample` of its threads to `~/ai/logs/docling-hang-*.sample` and
  keeps the last three.

It reads the in-flight conversions from docling-serve's log, so the plist
above must keep writing to `~/ai/logs/docling-serve.log`. A restart drops the
conversion that was running; Lemmary marks that document failed (see
[Troubleshooting](#troubleshooting)).

Save this as `~/ai/bin/docling-watchdog.py` and make it executable
(`chmod +x`):

```python
#!/usr/bin/python3
"""Watchdog for docling-serve with the ocrmac (Apple Vision) OCR engine.

Run every minute by a LaunchAgent. On each run it:

1. Makes sure ocrmac is patched to disable Vision's language correction (it is
   lost whenever `uv tool install/upgrade docling-serve` rebuilds the venv), and
   restarts docling-serve once it is idle if the running server predates the
   patch.
2. Restarts docling-serve when a task hangs: it has been "processing" for
   >= STALL_SEC while the server burned almost no CPU since the previous run,
   or for >= HARD_SEC regardless.

Why: on macOS 27.0 VNRecognizeTextRequest (accurate, revision 3, explicit
languages, language correction on) intermittently never returns. docling-serve
keeps answering /health, so launchd's KeepAlive never notices.
"""
import glob, json, os, re, subprocess, sys, time

LABEL = os.environ.get("WD_LABEL", "com.example.docling-serve")
HOME = os.path.expanduser("~")
LOG = os.environ.get("WD_LOG", f"{HOME}/ai/logs/docling-serve.log")
OUT = f"{HOME}/ai/logs/docling-watchdog.log"
STATE = os.environ.get("WD_STATE", f"{HOME}/ai/docling/.watchdog-state.json")
OCRMAC_GLOB = os.environ.get("WD_OCRMAC_GLOB") or f"{HOME}/.local/share/uv/tools/docling-serve/lib/python3*/site-packages/ocrmac/ocrmac.py"
STALL_SEC = int(os.environ.get("WD_STALL_SEC", 120))
HARD_SEC = int(os.environ.get("WD_HARD_SEC", 1200))
CPU_IDLE_SEC = float(os.environ.get("WD_CPU_IDLE_SEC", 2.0))
DRY_RUN = os.environ.get("WD_DRY_RUN") == "1"
TAIL_BYTES = 8 * 1024 * 1024

UUID = r"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"
RE_START = re.compile(r"processing task " + UUID)
RE_END = re.compile(r"(?:completed job|failed to process job) " + UUID)

PATCH_ANCHOR = "            req.setRecognitionLevel_(0)\n"
PATCH_LINE = "        req.setUsesLanguageCorrection_(False)\n"
PATCH = (
    "\n"
    "        # Local patch: on macOS 27.0 the accurate revision-3 recognizer with\n"
    "        # explicit languages + language correction intermittently deadlocks\n"
    "        # inside TextRecognition. Re-applied by docling-watchdog.py.\n"
    + PATCH_LINE
)


def say(msg):
    with open(OUT, "a") as f:
        f.write(time.strftime("%Y-%m-%d %H:%M:%S ") + msg + "\n")


def ensure_ocrmac_patch():
    """Patch every ocrmac.py in the docling-serve venv; return their paths."""
    paths = glob.glob(OCRMAC_GLOB)
    for path in paths:
        with open(path) as f:
            src = f.read()
        if PATCH_LINE in src:
            continue
        if src.count(PATCH_ANCHOR) != 1 or "setUsesLanguageCorrection_" in src:
            # ocrmac changed upstream; warn once per file version, never loop.
            marker = path + ".patch-warned"
            stamp = str(os.path.getmtime(path))
            if not os.path.exists(marker) or open(marker).read() != stamp:
                say(f"PATCH FAILED: anchor not found in {path}; patch ocrmac by hand")
                with open(marker, "w") as f:
                    f.write(stamp)
            continue
        say(f"PATCH: re-applying ocrmac language-correction patch to {path}")
        if DRY_RUN:
            continue
        tmp = path + ".wd-tmp"
        with open(tmp, "w") as f:
            f.write(src.replace(PATCH_ANCHOR, PATCH_ANCHOR + PATCH))
        os.chmod(tmp, os.stat(path).st_mode & 0o7777)
        os.replace(tmp, path)
    return paths


def server_pid():
    out = subprocess.run(["launchctl", "list", LABEL], capture_output=True, text=True).stdout
    m = re.search(r'"PID" = (\d+);', out)
    return int(m.group(1)) if m else None


def ps_field(pid, field):
    return subprocess.run(["ps", "-o", f"{field}=", "-p", str(pid)],
                          capture_output=True, text=True, env={"LC_ALL": "C"}).stdout.strip()


def cpu_seconds(pid):
    t = ps_field(pid, "time")
    if not t:
        return None
    days, _, t = t.rpartition("-")
    secs = 0.0
    for part in t.split(":"):
        secs = secs * 60 + float(part)
    return secs + int(days or 0) * 86400


def started_at(pid):
    t = ps_field(pid, "lstart")
    return time.mktime(time.strptime(t, "%a %b %d %H:%M:%S %Y")) if t else None


def in_flight(pid):
    with open(LOG, "rb") as f:
        f.seek(max(0, os.path.getsize(LOG) - TAIL_BYTES))
        text = f.read().decode("utf-8", "replace")
    # Only look at the log of the current server process.
    marker = f"Started server process [{pid}]"
    idx = text.rfind(marker)
    if idx >= 0:
        text = text[idx:]
    started = RE_START.findall(text)
    ended = set(RE_END.findall(text))
    return [t for t in dict.fromkeys(started) if t not in ended]


def restart(pid, kind, reason):
    say(f"{kind}: {reason}; restarting {LABEL} (pid {pid})")
    if DRY_RUN:
        return
    if kind == "HUNG":
        sample = f"{HOME}/ai/logs/docling-hang-{time.strftime('%Y%m%d-%H%M%S')}.sample"
        subprocess.run(["sample", str(pid), "3", "-file", sample], capture_output=True)
        for old in sorted(glob.glob(f"{HOME}/ai/logs/docling-hang-*.sample"))[:-3]:
            os.remove(old)
    subprocess.run(["launchctl", "kickstart", "-k", f"gui/{os.getuid()}/{LABEL}"], check=False)


def main():
    ocrmac_files = ensure_ocrmac_patch()
    pid = server_pid()
    if not pid:
        return
    now, cpu = time.time(), cpu_seconds(pid)
    try:
        with open(STATE) as f:
            state = json.load(f)
    except (OSError, ValueError):
        state = {}
    if state.get("pid") != pid:
        state = {"pid": pid, "seen": {}}

    tasks = in_flight(pid)
    seen = {t: state["seen"].get(t, now) for t in tasks}
    prev_cpu, prev_ts = state.get("cpu"), state.get("ts")
    oldest = max((now - ts for ts in seen.values()), default=0)
    cpu_delta = None if prev_cpu is None or cpu is None else cpu - prev_cpu

    action = None
    if not tasks:
        # Idle: reload if the running server imported ocrmac before the patch.
        start = started_at(pid)
        patched = max((os.path.getmtime(p) for p in ocrmac_files), default=0)
        if start and patched > start:
            action = ("RELOAD", "ocrmac.py changed after the server started")
    elif oldest >= HARD_SEC:
        action = ("HUNG", f"{len(tasks)} task(s) in flight, oldest {oldest:.0f}s >= {HARD_SEC}s")
    elif oldest >= STALL_SEC and cpu_delta is not None and cpu_delta < CPU_IDLE_SEC:
        action = ("HUNG", f"{len(tasks)} task(s) in flight, oldest {oldest:.0f}s, "
                          f"cpu +{cpu_delta:.2f}s over {now - prev_ts:.0f}s")

    if action:
        restart(pid, *action)
        state = {}
    else:
        state.update(seen=seen, cpu=cpu, ts=now)
    with open(STATE, "w") as f:
        json.dump(state, f)


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # never let launchd see a crash loop
        say(f"watchdog error: {e!r}")
        sys.exit(0)
```

Create `~/Library/LaunchAgents/com.example.docling-watchdog.plist`. `WD_LABEL`
is the docling-serve agent's label:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.docling-watchdog</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/<USER>/ai/bin/docling-watchdog.py</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>WD_LABEL</key><string>com.example.docling-serve</string>
  </dict>
  <key>StartInterval</key><integer>60</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>/Users/<USER>/ai/logs/docling-watchdog.log</string>
  <key>StandardErrorPath</key><string>/Users/<USER>/ai/logs/docling-watchdog.log</string>
</dict>
</plist>
```

```sh
plutil -lint ~/Library/LaunchAgents/com.example.docling-watchdog.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.docling-watchdog.plist
```

`~/ai/logs/docling-watchdog.log` gets a line only when the watchdog acts:
`PATCH` when it re-applies the patch, `RELOAD` when it restarts docling-serve
to load it, `HUNG` when it restarts a stuck server, and `PATCH FAILED` once if
a new ocrmac no longer matches the patch. An empty log means nothing has
happened. To try it without restarting anything, run it with `WD_DRY_RUN=1`.

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

**Timeouts:** raise `OCR_TIMEOUT_SEC` from its default of 90 to about 120.
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
launchctl bootout gui/$(id -u)/com.example.llama-embeddings
launchctl bootout gui/$(id -u)/com.example.docling-serve

# logs
tail -f ~/ai/logs/llama-embeddings.log ~/ai/logs/docling-serve.log
cat ~/ai/logs/docling-watchdog.log      # only what the watchdog did

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
  422 errors. The reinstall removes the
  [ocrmac patch](#_4-ocr-patch-ocrmac-against-the-vision-hang). Within a
  minute the watchdog logs `PATCH` and then `RELOAD`; without the watchdog,
  rerun the patch snippet and restart the agent.

## Troubleshooting

- **Lemmary says `connection refused`:** check the agent's status (above),
  whether `<USER>` is logged in (`who` shows `console`).
- **The first OCR document after a restart times out:** the models were still
  loading. Wait for `/ready` to return 200, or raise `OCR_TIMEOUT_SEC`.
- **OCR stops working while `/health` still says `ok`:** every OCR job fails on
  `OCR_TIMEOUT_SEC`, docling-serve sits at 0% CPU, and its log shows
  `Worker N processing task <id>` with no matching `completed job <id>`. That
  is the [Vision hang](#_4-ocr-patch-ocrmac-against-the-vision-hang). To
  confirm, run `sample <pid> 3` on the docling-serve process and look for
  `VNRecognizeTextRequest` waiting in `_dispatch_sema4_wait`. Restart the agent,
  then check that the patch is applied (rerunning the snippet prints `already
  patched`) and that the watchdog is loaded. `WORKER_MAX_RETRIES` defaults to
  0, so documents that timed out stay failed: use **Reprocess all failed** on
  the Documents page.
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
