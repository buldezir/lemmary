# Lokale OCR {#local-ocr}

Jeder andere OCR-Anbieter, mit dem Lemmary spricht, liest Ihre Dokumente auf der Hardware
von jemand anderem. Dieser nicht: ein zweiter Container neben der App, im selben
Docker-Netzwerk, ohne veröffentlichten Port und ohne API-Schlüssel. Scans verlassen den
Host nie.

Das ist der gesamte Vorteil, und der Preis sollte offen benannt werden: Die Images sind
groß, das erste Dokument ist langsam, und eine Seite dauert Sekunden statt Millisekunden.
Wenn Ihr Archiv nicht sensibel ist und Sie gern einen gehosteten Anbieter bezahlen, ist
[Mistral](/de/ai_providers) schneller und macht weniger Betriebsaufwand.

## Ein Container, mehrere Engines {#one-container-several-engines}

Das SDK ist `docling`, und der Container ist
[docling-serve](https://github.com/docling-project/docling-serve): ein öffentliches
Image auf GHCR, das PDF, Bilder, DOCX, PPTX, XLSX und HTML liest und Markdown mit Layout
und Tabellen zurückgibt.

Es gibt bewusst kein separates PaddleOCR-SDK, weil Sie PaddleOCR bereits haben. Die
Standard-Erkennungs-Engine von Docling ist RapidOCR, das sind die eigenen PP-OCR-Modelle
von PaddleOCR, nach ONNX exportiert – ein frischer Container lädt
`ch_PP-OCRv4_det_mobile.onnx` und `ch_PP-OCRv4_rec_mobile.onnx` herunter und liest damit.
Ein zweiter Container von mehreren Gigabyte, der dieselben Modelle ausführt, würde nichts
bringen, und die eigenen Serving-Images von PaddleOCR werden nur in der Registry von Baidu
veröffentlicht. Wenn Sie eine andere Erkennung möchten, wechseln Sie die Engine statt des
Containers: siehe [Eine Engine wählen](#choosing-an-engine).

## Inbetriebnahme {#bringing-it-up}

```bash
docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml up -d
```

Beide Dienste liegen hinter einem Compose-Profil, daher muss der Befehl eines nennen.
Ohne das würde `up` beide Images von mehreren Gigabyte herunterladen, obwohl jemand nur
eine einzige Engine wollte.

Dann zwei Zeilen in `.env`:

```bash
OCR_SDK=docling
OCR_BASE_URL=http://docling:5001
```

`OCR_BASE_URL` ist optional – dieser Wert ist der Standard für `OCR_SDK=docling`, weil
es der Dienstname ist, den das Overlay dem Container gibt. Setzen Sie ihn nur, wenn Sie den
Sidecar woanders hin verschoben haben.

Es gibt bewusst kein `OCR_API_KEY`. Diese SDKs kommen ohne Schlüssel aus: Die Adresse ist
die gesamte Konfiguration, weshalb der Sidecar keinen Port veröffentlicht und nur aus dem
App-Container erreichbar ist. Wenn Sie ihn weiter freigeben, starten Sie docling-serve mit
`DOCLING_SERVE_API_KEY` und tragen Sie denselben Wert in `OCR_API_KEY` ein – er wird als
`X-Api-Key` gesendet.

Auf einer Instanz, die bereits gestartet wurde, ist `.env` nicht der richtige Ort: Der
KI-Block belegt die Datenbank nur beim **ersten** Start vor. Fügen Sie den Anbieter
stattdessen unter **Einstellungen → Anbieter** hinzu (die Basis-URL ist vorausgefüllt, und
es gibt kein Feld für einen API-Schlüssel) und weisen Sie ihn dann unter
**Einstellungen → Modelle → OCR** zu.

## Was es kostet {#what-it-costs}

Gemessen auf einem amd64-Host mit 10 Kernen, mit dem Overlay im ausgelieferten Zustand. Ihre
Zahlen werden abweichen, das Gesamtbild aber nicht.

**Festplatte.** Das CPU-Image ist auf amd64 heruntergeladen 7,1 GB groß (auf arm64 kleiner).
Beim ersten Start lädt es etwa 1,3 GB Modellgewichte in das benannte Volume; ohne dieses
Volume würde es sie für jeden neuen Container erneut herunterladen.

**Arbeitsspeicher.** Docling pendelte sich bei **1,1 GB** resident ein, gegenüber dem
`mem_limit` des Overlays von 4 GB – der Spielraum ist für größere Dokumente da, nicht für
den Leerlauf. Unter [Verschlüsselung im Ruhezustand](/de/encryption) kommt dies *zusätzlich*
zum tmpfs hinzu, das das entschlüsselte Archiv enthält, sodass ein Host, der zuvor für 3 GB
ausgelegt war, nicht mehr ausreicht.

**Start.** Etwa **90 Sekunden** bei einem kalten Volume, fast ausschließlich für den
Modell-Download; **10 Sekunden** bei einem warmen. Das deckt `start_period: 180s` im
Overlay ab, und deshalb ist das Volume nicht optional.

**Zeit pro Dokument.** Auf diesem Host dauerte ein einseitiges, digital erzeugtes PDF
**2 s** und ein einseitiger Scan mit 150 dpi **4 s** – gegenüber einigen zehn
Millisekunden bei einer gehosteten API. Echte Scans sind dichter als eine Testvorlage,
betrachten Sie diese Werte also als Untergrenze und messen Sie selbst.

**Die Einstellung, die gern übersehen wird.** `OCR_TIMEOUT_SEC` ist standardmäßig
90 Sekunden. Das reicht für ein paar Seiten auf einem schnellen Host und kaum mehr – ein
dichtes mehrseitiges Dokument, eine ausgelastete Maschine oder die erste Anfrage nach einem
Neustart überschreiten es allesamt:

```bash
OCR_TIMEOUT_SEC=300
WORKER_TIMEOUT_SEC=1800
```

Beide werden **nur beim ersten Start vorbelegt**. Auf einer Instanz, die bereits gelaufen
ist, werden die Variablen ignoriert, und die Werte werden stattdessen in den
**Einstellungen** geändert. Das ist der häufigste Grund dafür, dass ein funktionierender
Sidecar fehlgeschlagene Dokumente erzeugt.

Der Sidecar hat eine eigene entsprechende Obergrenze: docling-serve bricht eine synchrone
Konvertierung nach `DOCLING_SERVE_MAX_SYNC_WAIT` Sekunden ab, unabhängig davon, was der
Client angefordert hat. Das Overlay setzt sie auf 900. Halten Sie sie über
`OCR_TIMEOUT_SEC`.

**Die praktische Seitenobergrenze ist das Worker-Timeout, nicht das Seitenlimit.**
Lemmary lehnt Dokumente mit mehr als 1000 Seiten ab, ein Limit, das von Mistral stammt.
Schon bei vier Sekunden pro Seite braucht eine lokale Engine für ein solches Dokument über
eine Stunde, weit mehr als jedes vernünftige `WORKER_TIMEOUT_SEC`. Ermitteln Sie Ihre eigenen
Sekunden pro Seite anhand der ersten Dokumente und bemessen Sie das Timeout danach.

**Nebenläufigkeit.** Die App sendet absichtlich jeweils eine Seite an eine lokale Engine.
Die Teilungserkennung liest normalerweise vier Seiten parallel, was bei einem gehosteten
Anbieter die Netzwerklatenz verdeckt; bei einem Sidecar, der sich die CPUs dieses Hosts
teilt, würde sie sich nur stauen, während das Timeout jeder Seite beim Warten auf einen Kern
abläuft. Eine Teilungserkennung über 40 Seiten dauert also ungefähr vierzigmal so lange wie
eine Seite, nacheinander. Planen Sie Zeit ein.

## Eine Engine wählen {#choosing-an-engine}

Weisen Sie unter **Einstellungen → Modelle** ein **OCR-Modell** zu, um zu ändern, was im
Sidecar läuft. Das ist optional – leer bedeutet den Standard des Containers, und das ist für
die meisten Installationen die richtige Wahl. Bei diesem SDK nennt das Feld eine OCR-Engine
statt eines Modells:

| Wert | Hinweise |
| --- | --- |
| *(leer)* | der Standard des Containers |
| `rapidocr` | der Standard: die PP-OCR-Modelle von PaddleOCR als ONNX |
| `easyocr` | breite Sprachabdeckung |
| `tesseract`, `tesserocr` | der Klassiker; am schnellsten, am schwächsten bei unsauberen Scans |

Lemmary sendet dies als Formularfeld `ocr_engine` von docling. Das festgelegte Image
akzeptiert sowohl dieses als auch seinen Nachfolger `ocr_preset` und markiert `ocr_engine`
als veraltet – es wird weiterhin weitergeleitet, sodass dieselben Werte auch nach einem
Image-Update funktionieren, bis es entfernt wird.

**Ein Name, den es nicht erkennt, wird stillschweigend akzeptiert.** Das Feld ist eine freie
Zeichenkette statt einer Aufzählung: Die Zuweisung von `tesserract` liefert HTTP 200, fällt
auf die Standard-Engine zurück und meldet nichts. Wenn eine zugewiesene Engine keinen
Unterschied zu machen scheint, prüfen Sie die Schreibweise anhand der obigen Tabelle – nichts
anderes wird es Ihnen sagen.

## GPU {#gpu}

Docling veröffentlicht CUDA-Images: `docling-serve-cu128` und `docling-serve-cu130`.
Tauschen Sie die Zeile `image:` im Overlay aus, fügen Sie einen Block
`deploy.resources.reservations.devices` für die GPU hinzu, und die Zeit pro Seite sinkt um
eine Größenordnung. Die App braucht keine Änderung – es ist dieselbe HTTP-API.

Auf einem Mac mit Apple Silicon hat Docker keinen Zugriff auf die GPU; führen Sie docling
stattdessen nativ aus, wie unter [Lokale OCR und Embeddings auf einem Mac](/de/local_ai_macos)
beschrieben.

## Fehlerbehebung {#troubleshooting}

- **Dokumente schlagen mit einem Timeout fehl.** Erhöhen Sie `OCR_TIMEOUT_SEC` in den
  **Einstellungen**, nicht in `.env`, es sei denn, es handelt sich um einen ersten Start.
  Siehe [Was es kostet](#what-it-costs).
- **Das erste Dokument nach einem Neustart schlägt fehl, spätere funktionieren.** Die Modelle
  wurden noch geladen. Prüfen Sie, ob das benannte Volume eingebunden ist –
  `docker compose config` zeigt es an – und geben Sie dem Container nach `up` ein paar Minuten.
- **`connection refused` mit Nennung von `docling`.** Der Sidecar läuft nicht oder läuft ohne
  sein Profil. `docker compose ps` sollte ihn auflisten; der Befehl benötigt
  `--profile docling`.
- **`HTTP 401: Unauthorized`.** Der Sidecar wurde mit `DOCLING_SERVE_API_KEY` gestartet, aber
  der Anbieter hat keinen Schlüssel. Tragen Sie denselben Wert in `OCR_API_KEY` ein oder in das
  Feld für den API-Schlüssel des Anbieters in den **Einstellungen**.
- **TXT, CSV, DOCX und XLSX erreichen den Sidecar nie.** Lemmary parst diese selbst, daher ist
  ein Problem mit einer davon kein OCR-Problem.
- **Probieren Sie es aus, bevor Sie sich darauf verlassen.** Die Seite **OCR-Test** schickt
  eine Datei durch einen Anbieter, ohne die Pipeline zu berühren. Ein lokaler Sidecar erscheint
  dort, sobald er unter **Anbieter** hinzugefügt wurde, noch bevor er der OCR zugewiesen ist.

## Was die Verschlüsselung nicht abdeckt {#one-thing-encryption-does-not-cover}

Mit `VAULT_ENABLED=1` ist das Archiv auf der Festplatte Chiffretext, und die entschlüsselte
Kopie liegt nur in einem tmpfs. Das an den Sidecar gesendete Dokument ist Klartext, über das
Compose-Netzwerk, und der eigene Arbeitsbereich des Sidecars wird vom Tresor nicht erfasst.
Das ist eine weit geringere Angriffsfläche, als es in eine gehostete API hochzuladen – es
verlässt den Host nie –, aber es ist nicht nichts. Siehe
[Verschlüsselung im Ruhezustand](/de/encryption).
