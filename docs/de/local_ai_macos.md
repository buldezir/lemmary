# Lokale OCR und Embeddings auf einem Mac mit Apple Silicon {#local-ocr-and-embeddings-on-an-apple-silicon-mac}

[Lokale OCR](/de/local_ocr) und [lokale Embeddings](/de/local_embeddings) laufen
auf Ihrer eigenen Hardware, sodass Dokumente Ihr Netzwerk nie verlassen. Diese
Anleitung betreibt beides auf einem Mac mit M-Chip, nativ unter launchd, unter
Nutzung der GPU und der Neural Engine des Mac. Lemmary selbst läuft auf einem
anderen Host (einer VM, einem Linux-Server, irgendetwas mit Docker) und erreicht
den Mac über das LAN.

Getestet auf einem Mac mini M4 in der Grundausstattung (16 GB): Beide Dienste
laufen nebeneinander und sind schnell – ein paar Sekunden pro gescannter Seite und
ein vollständiger Embedding-Stapel in etwa 4 s. Siehe
[Gemessene Leistung](#measured-performance).

Lemmary muss nicht geändert werden: Embeddings nutzen das Local-Embeddings-SDK
(`local`, jeder OpenAI-kompatible Endpunkt `/v1/embeddings`) und OCR das
Local-OCR-SDK (`docling`).

## Platzhalter {#placeholders}

| Platzhalter | Bedeutung | Beispiel |
|---|---|---|
| `<MAC_IP>` | Die feste LAN-Adresse des Mac (bei mehreren die kabelgebundene verwenden) | `192.168.1.20` |
| `<USER>` | Der macOS-Benutzer, unter dem die Dienste laufen | `alice` |
| `com.example` | Reverse-DNS-Präfix für die launchd-Labels | Ihre eigene Domain |

## Überblick {#overview}

| | Embeddings | OCR |
|---|---|---|
| Lemmary-SDK | `local` (OpenAI-kompatibles `/v1/embeddings`) | `docling` |
| Server | llama.cpp `llama-server` (Homebrew), Metal | docling-serve 1.35.0 (uv tool), MPS |
| Modell / Engine | `BAAI/bge-m3`, F16 GGUF | Apple Vision über `ocrmac`, docling-Layout-/Tabellenmodelle auf MPS |
| Endpunkt | `http://<MAC_IP>:8080/v1` | `http://<MAC_IP>:5001` |
| Authentifizierung | keine | keine |
| LaunchAgent | `com.example.llama-embeddings` | `com.example.docling-serve` |
| Residenter Speicher (gemessen) | ~4,5 GB | ~3,9 GB |
| Log | `~/ai/logs/llama-embeddings.log` | `~/ai/logs/docling-serve.log` |

## Anforderungen an den Host {#host-requirements}

- Mac mit Apple Silicon. Die Referenzmaschine ist ein Mac mini M4 (4P- + 6E-Kerne,
  10-Kern-GPU) mit 16 GB unter macOS 26 und später 27.0. Sind beide Dienste
  geladen, lassen 16 GB noch Platz für einen CI-Runner oder Ähnliches daneben.
  macOS 27.0 braucht den [ocrmac-Patch](#_4-ocr-patch-ocrmac-against-the-vision-hang).
- Eine feste IP. Hat der Mac sowohl eine kabelgebundene als auch eine WLAN-Adresse,
  verwenden Sie die kabelgebundene; die Dienste lauschen auf allen Schnittstellen.
- Die Dienste sind **LaunchAgents** und laufen daher innerhalb der
  Anmeldesitzung eines Benutzers. Für einen Server ohne Bildschirm aktivieren Sie
  die automatische Anmeldung für `<USER>` und stellen den Mac so ein, dass er nie
  in den Ruhezustand geht und nach einem Stromausfall neu startet
  (Systemeinstellungen → Energie).
- Homebrew.

## Warum diese Konfiguration {#why-this-setup}

- **Nativ, nicht Docker.** Docker unter macOS (Docker Desktop, OrbStack, Colima)
  betreibt eine Linux-VM ohne Zugriff auf die Apple-GPU, sodass alles in einem
  Container auf der CPU laufen würde.
- **llama.cpp statt TEI für Embeddings.** Die Compose-Dateien von Lemmary
  verwenden text-embeddings-inference. TEI gibt es als Metal-Build
  (`brew install text-embeddings-inference`), aber auf dem Referenz-Mac schaffte
  es etwa 1k Tokens/s gegenüber etwa 4k Tokens/s bei llama.cpp mit demselben
  Modell.
- **docling mit Apple Vision für OCR.** Das ist das SDK, das Lemmary bereits
  spricht, sodass kein Adapter nötig ist. Die `auto`-Engine von docling wählt
  unter macOS von selbst `ocrmac`.

## Verzeichnisstruktur {#layout}

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

## Installation {#installation}

### 1. Pakete {#_1-packages}

```sh
brew install llama.cpp uv poppler     # poppler only for pdfinfo/pdfimages when testing
mkdir -p ~/ai/models ~/ai/logs ~/ai/docling ~/ai/embeddings
```

Ist Xcode installiert, seine Lizenz aber noch nicht akzeptiert, beschwert sich
Homebrew. Bottles werden trotzdem installiert; um die Meldung abzustellen, führen
Sie einmalig `sudo xcodebuild -license accept` aus.

### 2. Embeddings: llama.cpp + bge-m3 {#_2-embeddings-llama-cpp-bge-m3}

```sh
curl -fL -o ~/ai/models/bge-m3-f16.gguf \
  https://huggingface.co/gpustack/bge-m3-GGUF/resolve/main/bge-m3-FP16.gguf
```

Legen Sie `~/Library/LaunchAgents/com.example.llama-embeddings.plist` an. launchd
expandiert `~` nicht, ersetzen Sie daher `<USER>` durch den echten Benutzernamen:

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

Warum diese Flags:

- `--pooling cls`: Das Dense-Embedding von bge-m3 ist das CLS-Token; llama.cpp
  normalisiert es anschließend per L2, genau wie TEI.
- `-ub 8192`: Ein Encoder-Modell muss jede Eingabe in einem einzigen Micro-Batch
  unterbringen. Lemmary begrenzt eine Eingabe auf 8000 Zeichen, was bei dichtem
  CJK-Text bis zu etwa 5,7k Tokens sind; das Fenster von bge-m3 beträgt 8192. Mit
  einem kleineren `-ub` schlagen lange Eingaben fehl, statt eingebettet zu werden.
- `-fa on`: Flash Attention verhindert, dass die Attention-Matrix für 8192 Tokens
  vollständig materialisiert wird, und ist auf Metal zudem schneller (etwa
  4k Tokens/s bei 512 Tokens und 2,4k bei 4k, gegenüber 3,8k und 1,7k ohne).
- `-np 1`: Vier Slots waren bei einem Stapel von 64 Chunks nur etwa 5 % schneller.
- `--alias BAAI/bge-m3`: Das ist der Modellname, den Lemmary sendet und anzeigt.
- F16 statt Q8_0: gleiche Geschwindigkeit auf Metal und näher an den
  Originalgewichten.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.llama-embeddings.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.llama-embeddings.plist
curl -s localhost:8080/health        # {"status":"ok"}
```

### 3. OCR: docling-serve + ocrmac {#_3-ocr-docling-serve-ocrmac}

```sh
uv tool install --python 3.12 docling-serve==1.35.0 --with ocrmac
```

Python 3.12 wird absichtlich verwendet: Es gibt Berichte, dass docling unter
Python 3.14 auf Apple Silicon abstürzt. Das installierte torch sollte `mps` als
verfügbar melden:

```sh
~/.local/share/uv/tools/docling-serve/bin/python -c 'import torch; print(torch.backends.mps.is_available())'
```

Legen Sie `~/Library/LaunchAgents/com.example.docling-serve.plist` an:

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

- `DOCLING_SERVE_MAX_SYNC_WAIT=900`: Der synchrone Konvertierungsendpunkt gibt nach
  dieser Zeit auf, unabhängig davon, was der Client angefordert hat. Halten Sie
  den Wert über Lemmarys `OCR_TIMEOUT_SEC`.
- `UVICORN_WORKERS=1`: Ein zweiter Worker würde eine zweite Kopie der Modelle
  laden, ohne zusätzlichen Durchsatz zu bringen. Lemmary sendet docling ohnehin
  jeweils nur eine Anfrage.
- Ein LaunchAgent (angemeldete Sitzung) statt eines LaunchDaemon: Apples
  Vision-Framework arbeitet innerhalb einer Benutzersitzung am zuverlässigsten.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.docling-serve.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.docling-serve.plist
curl -s localhost:5001/health        # {"status":"ok"}
curl -s -o /dev/null -w '%{http_code}\n' localhost:5001/ready   # 200 once models are loaded
```

Die erste Konvertierung lädt die Layout- und Tabellenmodelle von docling (etwa
0,5 GB) nach `~/.cache/huggingface` herunter und dauert etwa 40 s. Spätere laufen
warm. Im Log sollten `Auto OCR model selected ocrmac.` und
`Accelerator device: 'mps'` erscheinen.

### 4. OCR: ocrmac gegen das Hängen von Vision patchen {#_4-ocr-patch-ocrmac-against-the-vision-hang}

Unter macOS 27.0 (Build 26A428) kehrt die genaue Texterkennung von Apple Vision
manchmal nie zurück. Sie hängt, wenn sowohl explizite Erkennungssprachen als auch
die Sprachkorrektur gesetzt sind, und genau so ruft docling sie über `ocrmac` auf.
Es wird nichts protokolliert, und es kommt kein Fehler zurück. Weil Vision pro
Prozess nur eine Erkennung gleichzeitig ausführt, stauen sich alle folgenden
OCR-Anfragen hinter der hängenden. docling-serve beantwortet `/health` weiterhin,
daher startet launchd es nie neu, und die OCR-Jobs von Lemmary scheitern einer nach
dem anderen an `OCR_TIMEOUT_SEC`.

Es hängt nicht am Dokument: Das Bild, das hängen blieb, ging beim nächsten Versuch
durch. Auch auf CPU oder GPU gezwungen hängt Vision, an der Neural Engine liegt es
also ebenfalls nicht. Ein Stresstest, der Vision direkt ohne docling aufrief, hing
nach 7 bis 431 Aufrufen.

Das Abschalten der Sprachkorrektur vermeidet es. Mit dem Patch lief derselbe
Stresstest 10.000 Aufrufe ohne Hängen, und der Text deutscher Rechnungen war
identisch. `ocrmac` hat dafür keine Option, also patchen Sie die installierte
Kopie:

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

Der Patch liegt im venv, daher entfernt ihn `uv tool install ... --force`. Der
Watchdog im nächsten Schritt setzt ihn wieder ein. Ohne Watchdog führen Sie das
Snippet nach jedem Update erneut aus. Auf macOS-Versionen ohne den Fehler schadet
der Patch nicht.

### 5. OCR: Watchdog {#_5-ocr-watchdog}

Der Patch verhindert das bekannte Hängen. Der Watchdog behebt zusätzlich jedes
andere und hält den Patch über Updates hinweg aktuell. Ein LaunchAgent startet ihn
jede Minute. Bei jedem Lauf:

- setzt er den ocrmac-Patch wieder ein, falls ein Update ihn entfernt hat, und
  startet docling-serve neu, sobald keine Konvertierung läuft;
- startet er docling-serve neu, wenn eine Konvertierung seit 2 Minuten läuft,
  während der Server kaum CPU verbraucht hat (unter 2 s seit dem letzten Lauf),
  oder seit 20 Minuten, egal was. Vor dem Neustart eines hängenden Servers sichert
  er ein 3-sekündiges `sample` seiner Threads nach
  `~/ai/logs/docling-hang-*.sample` und behält die letzten drei.

Die laufenden Konvertierungen liest er aus dem Log von docling-serve, daher muss die
plist oben weiter nach `~/ai/logs/docling-serve.log` schreiben. Ein Neustart
verwirft die gerade laufende Konvertierung; Lemmary markiert das Dokument als
fehlgeschlagen (siehe [Fehlerbehebung](#troubleshooting)).

Speichern Sie dies als `~/ai/bin/docling-watchdog.py` und machen Sie es ausführbar
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

Legen Sie `~/Library/LaunchAgents/com.example.docling-watchdog.plist` an.
`WD_LABEL` ist das Label des docling-serve-Agents:

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

`~/ai/logs/docling-watchdog.log` bekommt nur dann eine Zeile, wenn der Watchdog
eingreift: `PATCH`, wenn er den Patch wieder einsetzt, `RELOAD`, wenn er
docling-serve neu startet, um ihn zu laden, `HUNG`, wenn er einen hängenden Server
neu startet, und einmalig `PATCH FAILED`, wenn ein neues ocrmac nicht mehr zum
Patch passt. Ein leeres Log heißt, dass nichts passiert ist. Zum Ausprobieren, ohne
etwas neu zu starten, führen Sie ihn mit `WD_DRY_RUN=1` aus.

## Konfiguration von Lemmary {#lemmary-configuration}

Auf einer Instanz, die bereits gestartet wurde, werden die KI-Einstellungen in
`.env` ignoriert (sie befüllen nur den ersten Start). Konfigurieren Sie sie
stattdessen in der Oberfläche.

**Einstellungen → KI → Anbieter**

- **Lokale Embeddings** (`local`): Basis-URL `http://<MAC_IP>:8080/v1`, kein
  API-Schlüssel.
- **Lokale OCR** (`docling`): Basis-URL `http://<MAC_IP>:5001`, kein
  API-Schlüssel.

**Einstellungen → KI → Modelle**

- Embeddings: `BAAI/bge-m3`, das die Auswahl bei einem Anbieter vom Typ Lokale
  Embeddings automatisch einträgt. Es entspricht dem `--alias` oben.
- OCR: Ordnen Sie den docling-Anbieter zu und lassen Sie das OCR-Modell **leer**,
  was die `auto`-Engine von docling und damit `ocrmac` bedeutet. `ocrmac`
  explizit zu setzen funktioniert ebenfalls.

**Timeouts:** Erhöhen Sie `OCR_TIMEOUT_SEC` vom Standardwert 90 auf etwa 120. Eine
fotografierte Seite dauert im warmen Zustand 4–8 s, ein langes Dokument oder die
erste Anfrage nach einem Neustart jedoch länger. Halten Sie den Wert unter
`DOCLING_SERVE_MAX_SYNC_WAIT` (900).

Für eine frische Instanz lautet die entsprechende `.env`:

```sh
AI_EMBEDDING_SDK=local
AI_EMBEDDING_BASE_URL=http://<MAC_IP>:8080/v1
AI_EMBEDDING_MODEL=BAAI/bge-m3
OCR_SDK=docling
OCR_BASE_URL=http://<MAC_IP>:5001
OCR_TIMEOUT_SEC=120
```

Prüfen Sie die Erreichbarkeit aus dem Lemmary-Container heraus:

```sh
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:8080/health
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:5001/health
```

Wer das Embedding-Modell oder den Server wechselt, muss das Archiv neu einbetten
(siehe [Das Modell wechseln](/de/local_embeddings#changing-the-model)). Die
Vektoren von bge-m3 unter llama.cpp liegen nah an denen von TEI, sind aber nicht
bitidentisch.

## Smoke-Test- und Benchmark-Skript {#smoke-test-and-benchmark-script}

Speichern Sie dies als `~/ai/embeddings/embtest.py` und führen Sie
`python3 ~/ai/embeddings/embtest.py http://127.0.0.1:8080` aus:

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

Erwartet: `dim 1024 norm 1.0`, en-de etwa 0,92, en-ru etwa 0,78, unzusammenhängend
etwa 0,29.

## Gemessene Leistung {#measured-performance}

Warme Durchläufe auf dem Referenz-Mac mini M4 (16 GB).

**Embeddings (bge-m3)**

| Test | llama.cpp, Metal | TEI 1.9.4, Metal |
|---|---|---|
| 64 Chunks × ~231 Tokens (ein Lemmary-Stapel) | **3,9 s** | 15,0 s |
| Eine Eingabe mit 3,2k Tokens | **1,2 s** | 3,0 s |

Qualitätsprüfung: Ähnlichkeit Englisch–Deutsch 0,917, Englisch–Russisch 0,776,
unzusammenhängend 0,289; die Vektoren sind 1024-dimensional mit Einheitsnorm.

**OCR (docling + ocrmac)**, an einem zweiseitigen Handyfoto eines deutschen
Briefs (JPEG mit 2402×3586 und 1601×2254):

- Seite 1: 8,1 s; Seite 2: 4,1 s; das vollständige zweiseitige PDF: 14,1 s.
- Die erste Seite mit kalten Modellen: 38 s.
- Alle Beträge stimmten mit der Vorlage überein. Der Text war sauberer als eine
  Tesseract-Textebene (OCRmyPDF) auf demselben PDF.

## Betrieb {#operations}

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

**Aktualisieren**

- llama.cpp: `brew upgrade llama.cpp`, dann den Embeddings-Agent neu starten.
- docling-serve: auf 1.35.0 festgelegt. Zum Aktualisieren führen Sie
  `uv tool install --python 3.12 docling-serve==<new> --with ocrmac --force` aus,
  starten den Agent neu und führen eine Testkonvertierung durch. Lemmary sendet
  `ocr_engine` nur, wenn eine Engine zugeordnet ist, wodurch Image-Updates nicht
  zu 422-Fehlern führen. Die Neuinstallation entfernt den
  [ocrmac-Patch](#_4-ocr-patch-ocrmac-against-the-vision-hang). Innerhalb einer Minute protokolliert der Watchdog
  `PATCH` und dann `RELOAD`; ohne Watchdog führen Sie das Patch-Snippet erneut aus
  und starten den Agent neu.

## Fehlerbehebung {#troubleshooting}

- **Lemmary meldet `connection refused`:** Prüfen Sie den Status des Agents (siehe
  oben) und ob `<USER>` angemeldet ist (`who` zeigt `console`).
- **Das erste OCR-Dokument nach einem Neustart läuft in einen Timeout:** Die
  Modelle wurden noch geladen. Warten Sie, bis `/ready` 200 zurückgibt, oder
  erhöhen Sie `OCR_TIMEOUT_SEC`.
- **OCR funktioniert nicht mehr, obwohl `/health` noch `ok` meldet:** Jeder
  OCR-Job scheitert an `OCR_TIMEOUT_SEC`, docling-serve steht bei 0 % CPU, und sein
  Log zeigt `Worker N processing task <id>` ohne passendes `completed job <id>`.
  Das ist das [Hängen von Vision](#_4-ocr-patch-ocrmac-against-the-vision-hang). Zur Bestätigung führen Sie
  `sample <pid> 3` für den docling-serve-Prozess aus und suchen nach
  `VNRecognizeTextRequest`, das in `_dispatch_sema4_wait` wartet. Starten Sie den
  Agent neu und prüfen Sie dann, ob der Patch angewendet ist (das Snippet gibt
  erneut ausgeführt `already patched` aus) und ob der Watchdog geladen ist.
  `WORKER_MAX_RETRIES` ist standardmäßig 0, daher bleiben Dokumente mit Timeout
  fehlgeschlagen: Nutzen Sie **Alle fehlgeschlagenen erneut verarbeiten** auf der
  Dokumentenseite.
- **Embeddings schlagen bei sehr langen Eingaben fehl:** `-ub` ist kleiner als die
  Eingabe. Halten Sie den Wert bei 6144 oder darüber; 8192 ist das Maximum des
  Modells.
- **Speicher:** Zusammen belegen die Dienste etwa 8,5 GB. Auf einem Mac mit 16 GB
  und anderen anspruchsvollen Lasten verkleinert ein auf 6144 gesenktes `-ub` die
  Rechenpuffer von llama.cpp; auf einem Mac mit 8 GB betreiben Sie nur einen der
  beiden Dienste.
- **Falsche IP:** Eine WLAN-Adresse kann sich ändern. Richten Sie Lemmary auf die
  kabelgebundene Adresse aus oder geben Sie dem Mac eine DHCP-Reservierung.

## Upgrade-Pfad, falls Apples OCR nicht gut genug ist {#upgrade-path-if-apple-s-ocr-is-not-good-enough}

Für schwierige Scans, Handschrift oder komplexe Tabellen läuft ein
Vision-Language-OCR-Modell (GLM-OCR, PaddleOCR-VL) über `mlx-vlm`
(`mlx_vlm.server`, OpenAI-kompatibel) gut auf Apple Silicon. Lemmary würde es über
sein `openai`-OCR-SDK ansprechen. Dieses SDK sendet PDFs als rohe Dateiteile, die
lokale VLM-Server ablehnen, daher braucht es einen kleinen Adapter, der Seiten
zuerst in Bilder umwandelt. Rechnen Sie mit mehreren zehn Sekunden pro Seite
statt einigen wenigen Sekunden.
