# Lokale Embeddings {#local-embeddings}

Dichte Suche (Dense Retrieval) ist die einzige KI-Aufgabe in Lemmary, die kein
Spitzenmodell braucht. Ein Modell von 500 MB bis 2 GB auf Ihrer eigenen CPU kann mit
`text-embedding-3-small` mithalten, und anders als OCR oder Extraktion läuft es über
jede Passage jedes Dokuments, das Sie besitzen – ein gehosteter Anbieter bedeutet
also, das gesamte Archiv nach außen zu senden, und es erneut zu senden, wann immer
Sie einen Titel bearbeiten oder das Modell wechseln.

Wenn Sie es selbst betreiben, entfallen die Token-Rechnung und der Preis pro Dokument
vollständig. Was nicht entfällt, ist die Arbeit: Dieselben Forward-Passes finden auf
Ihrer Hardware statt, und das erste Nachholen (Backfill) eines großen Archivs ist der
einzige Zeitpunkt, an dem das spürbar langsam ist. Im laufenden Betrieb – eine Handvoll
Dokumente pro Upload – reichen ein paar Kerne bequem aus. Wie die Rechnung in beiden
Fällen insgesamt aussieht, lesen Sie unter
[was Embeddings kosten](/de/ai_providers#what-embeddings-cost).

Das SDK ist `local`: ein beliebiger OpenAI-kompatibler `/v1/embeddings`-Endpunkt, den
Sie selbst betreiben. Es erzeugt Embeddings und sonst nichts – als `AI_SDK` und
`OCR_SDK` wird es abgelehnt – und wie [docling](/de/local_ocr) benötigt es keinen
API-Schlüssel, weil sich ein Dienst in einem privaten Netzwerk bei niemandem
authentifizieren muss. Seine Adresse ist seine gesamte Konfiguration.

Auf einem Mac mit Apple Silicon siehe [Lokale OCR und Embeddings auf einem Mac](/de/local_ai_macos):
llama.cpp auf der GPU des Macs statt eines CPU-Containers.

## Inbetriebnahme {#bringing-it-up}

Zwei Compose-Overlays betreiben [text-embeddings-inference][tei] als Sidecar, und an
der Basisdatei ändert sich nichts.

Sie sind nicht ganz identisch, prüfen Sie also, welches Sie verwenden:

| | CPU (`docker-compose.embeddings.yml`) | GPU (`docker-compose.embeddings-gpu.yml`) |
| --- | --- | --- |
| Bindet die App an den Sidecar | nein – der `app`-Block ist auskommentiert | ja, alle drei Variablen |
| Veröffentlichter Port | `127.0.0.1:8999` zur Fehlersuche | keiner; nur im Compose-Netzwerk erreichbar |

Beim GPU-Overlay ist das auf einem frischen Volume die gesamte Konfiguration: Die
Instanz startet mit einem bereits zugewiesenen Anbieter **Local embeddings**. Beim
CPU-Overlay setzen Sie die drei Variablen selbst – kommentieren Sie den `app`-Block in
der Datei ein oder tragen Sie sie in `.env` ein:

```bash
AI_EMBEDDING_SDK=local
AI_EMBEDDING_BASE_URL=http://embeddings:80/v1
AI_EMBEDDING_MODEL=BAAI/bge-m3
```

Der veröffentlichte Port des CPU-Overlays ist absichtlich an Loopback gebunden. TEI hat
keine Authentifizierung – das ist ja der Sinn eines Dienstes, den nichts außerhalb des
Compose-Netzwerks erreichen kann –, daher würde ein bloßes `8999:80` an `0.0.0.0`
binden und eine nicht authentifizierte Inferenz-API ins LAN stellen. Lemmary selbst
nutzt den Port nicht; er ist für `curl 127.0.0.1:8999/info` da, während Sie das Modell
einrichten, und Sie können die Zeile löschen.

Keines der Images ist Multi-Arch. Die `cpu-*`-Tags sind `linux/amd64`, und es gibt kein
`cpu-arm64-1.9` – die arm64-Linie wird unversioniert als `cpu-arm64-latest`
veröffentlicht. Tauschen Sie auf einem arm64-Host das Tag aus und nehmen Sie in Kauf,
dass es nicht festgelegt ist.

[tei]: https://github.com/huggingface/text-embeddings-inference

```bash
# CPU, runs anywhere
docker compose -f docker-compose.yml -f docker-compose.embeddings.yml up

# NVIDIA GPU -- instead of the CPU file, not alongside it. Pick the image tag
# for your card; the file lists them.
docker compose -f docker-compose.yml -f docker-compose.embeddings-gpu.yml up
```

Beide lassen sich mit der [Verschlüsselung im Ruhezustand](/de/encryption) kombinieren,
die nur den `app`-Dienst betrifft:

```bash
docker compose -f docker-compose.yml \
               -f docker-compose.encrypted.yml \
               -f docker-compose.embeddings.yml up
```

In beiden Fällen zeigt **Einstellungen → Modelle** anschließend das eine Modell an, das
der Sidecar bereitstellt.

## Ein Modell wählen {#choosing-a-model}

`EMBEDDINGS_MODEL` benennt es einmal, sowohl für den Sidecar als auch für die Zuweisung.

| Modell | Dimensionen | Fenster | Gewichte | |
| --- | --- | --- | --- | --- |
| `BAAI/bge-m3` (der Standard) | 1024 | 8192 Tokens | ~2,2 GB | Ausgeprägt mehrsprachig, und genau darum geht es: Sprachübergreifendes Suchen in einer einzigen Suche ist das, was Ihnen dichte Suche bringt. Langsam auf einer kleinen CPU. |
| `intfloat/multilingual-e5-small` | 384 | 512 Tokens | ~470 MB | Schnell, und ein Viertel des Index-RAMs. Schwächere Suche, und unsere Passagen von ~1100 Zeichen liegen nahe an seiner Abschneidegrenze. |

Beide belegen weniger Platz als ein gehostetes Modell mit 1536 Dimensionen, was vor
allem unter `VAULT_ENABLED=1` zählt, wo der Index in einem tmpfs liegt. Alles, was
text-embeddings-inference unterstützt, funktioniert; diese beiden markieren die Enden
des Bereichs, bei dem sich der Einstieg lohnt.

Der Sidecar wird mit `--auto-truncate` und `--max-client-batch-size=64` gestartet, der
Batch-Größe, die Lemmary sendet. Ein Drittanbieter-Endpunkt, der auf dem TEI-Standard
von 32 belassen wurde, antwortet bei einem vollen Batch mit `413`, wodurch das Dokument
deutlich sichtbar fehlschlägt, statt es erneut zu versuchen.

`--max-batch-tokens` ist ein separater Hebel, und zwar derjenige, der bestimmt, wie viel
Speicher der Sidecar braucht. Es ist das Budget von TEI für einen Forward-Pass, keine
Begrenzung der Anfragegröße, und auf der CPU bestimmt es den residenten Speicher. Das
Overlay liefert **6144** aus, gewählt anhand der längsten Eingabe, die Lemmary senden
kann: Ein einzelner Chunk ist auf 8000 Runes begrenzt, was in der dichtesten gemessenen
Schrift 5723 Tokens entspricht. Unterhalb dieses Werts schneidet TEI stillschweigend
ab – bei 4096 wird eine Eingabe mit 5723 Tokens byte-identisch zu ihren ersten 4096
Tokens eingebettet, mit einem `200` und ohne Warnung.

Planen Sie den Speicher des Hosts entsprechend ein, denn eine Überschreitung führt
während des Aufwärmens zu einem OOM-Kill statt zu einem sauberen Fehler. Gemessen für
`bge-m3` auf der CPU:

| `--max-batch-tokens` | Spitzenwert resident | |
| --- | --- | --- |
| 8192 | — | Entspricht dem eigenen Fenster des Modells, sodass nie etwas abgeschnitten wird. Benötigt mehr RAM, als ein Host mit 16 GB frei hat. |
| **6144** | **~8,1 GB** | Der Standard. Deckt die Eingabegrenze von 8000 Runes in jeder getesteten Schrift ab. |
| 4096 | ~6,5 GB | Passt auf einen kleineren Host, schneidet dichte Passagen aber ab, ohne es zu melden. |

Das Overlay setzt außerdem `mem_limit: 10g`. Das Aufwärmen kommt nicht in die Nähe
davon; das Limit ist dazu da, dass eine Überschreitung den Sidecar beendet und nicht
irgendetwas anderes, das auf dem Host läuft, denn der OOM-Killer des Kernels wählt nach
Größe und nicht nach Wichtigkeit. Ein abstürzender Sidecar ist ein weicher Fehler –
Dokumente behalten ihren Text, ihre Metadaten und ihren Platz in der Stichwortsuche und
werden mit einem Backoff erneut versucht. Auf einem Host mit weniger freiem Speicher
wechseln Sie lieber zu einem kleineren Modell, statt `--max-batch-tokens` unter die
Abschneidegrenze zu senken.

## Das Modell wechseln {#changing-the-model}

Vektoren aus zwei Modellen lassen sich nicht vergleichen, daher bedeutet ein Wechsel,
das gesamte Archiv neu einzubetten – und auf einer selbst gehosteten Instanz erfordert
das **zwei** Schritte, weil die Umgebung außerhalb von `AI_MANAGED=1` die Datenbank nur
beim ersten Start vorbelegt:

1. Ändern Sie `EMBEDDINGS_MODEL` und erstellen Sie den Sidecar neu.
2. Ändern Sie das Modell außerdem unter **Einstellungen → Modelle**.

Wenn Sie nur den ersten Schritt ausführen, nennt die Zuweisung weiterhin das alte
Modell, während der Sidecar das neue bereitstellt. Das wird erkannt und ist nicht
stillschweigend falsch – die App speichert die Vektorlänge aus der ersten Antwort des
Anbieters und lehnt eine spätere Abweichung ab –, aber das Archiv erzeugt keine
Embeddings mehr, bis die Zuweisung korrigiert ist. Eine Änderung in den Einstellungen
setzt die gespeicherten Dimensionen zurück und baut den Chunk-Index neu auf, was ein
Modellwechsel tatsächlich erfordert. Unter `AI_MANAGED=1` ist die Umgebung bei jedem
Start maßgeblich, daher genügt Schritt 1.
