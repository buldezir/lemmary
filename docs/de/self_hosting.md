# Self-Hosting mit Docker {#self-hosting-with-docker}

Das veröffentlichte Image enthält alles, was Lemmary braucht – das Go-Binary, die
gebaute SPA und diese Dokumentation, poppler für die PDF-Verarbeitung und den
FAISS-Build, gegen den die Vektorsuche gelinkt ist. Auf dem Host muss außer Docker
nichts installiert werden.

Um stattdessen aus dem Quellcode zu starten, siehe
[Entwicklungsumgebung](/de/development).

## Schnellstart {#quick-start}

```bash
cp .env.example .env
# Optional: put an AI key in .env so the first boot skips the wizard.
docker compose up -d
```

Öffnen Sie [http://127.0.0.1:8090](http://127.0.0.1:8090). Auf einem frischen
Volume legt der [Einrichtungsassistent](/de/setup#first-launch-setup-wizard) in
der App das Admin-Konto an und erfasst die Schlüssel für OCR und das Sprachmodell;
ein einziger Mistral-Schlüssel deckt beides ab. Was in `.env` gehört, steht unter
[KI-Anbieter](/de/ai_providers).

`docker-compose.yml` ist bewusst kurz gehalten:

```yaml
services:
  app:
    image: ghcr.io/buldezir/lemmary:latest
    restart: unless-stopped
    env_file: [.env]
    ports: ["8090:${PORT:-80}"]
    volumes: [app_data:/app/pb_data]
```

Fügen Sie `--build` hinzu, um das Image lokal aus dem `Dockerfile` zu bauen, statt
es herunterzuladen. Veröffentlichte Tags sind `latest` (Standard-Branch), die
Release-Version (`1.2.3`, `1.2`, `1`) und der Commit-SHA, jeweils für
`linux/amd64` und `linux/arm64`.

## Was wo liegt {#what-lives-where}

Den kurzen Architekturüberblick, einschließlich S3-Dokumentspeicher und des
Speicherorts der Embedding-Vektoren, finden Sie unter [Speicher](/de/storage).

Mit dem standardmäßigen lokalen Dateispeicher liegt alles Zustandsbehaftete unter
`/app/pb_data`, auf dem Volume `app_data`:

| Pfad | Inhalt |
| --- | --- |
| `data.db` | Dokumente, Metadaten, Einstellungen, Chats – einschließlich des OCR-Texts, der direkt in der Zeile gespeichert wird |
| `storage/` | die hochgeladenen Originaldateien und erzeugten Vorschaubilder |
| `bleve/documents`, `bleve/chunks` | die [Suchindizes](/de/setup#full-text-search) – abgeleitete Daten, die beim nächsten Start neu aufgebaut werden, wenn sie gelöscht wurden |

Planen Sie bei der Größe des Volumes neben den Dateien auch die Datenbank ein:
Extrahierter Text wird in der Zeile gespeichert, und Embeddings kommen mit etwa
30–60 KB pro Dokument hinzu.

Der Container startet nur so lange als root, wie nötig ist, um ein Volume zu
übernehmen, das ein älteres, als root laufendes Image angelegt hat, und wechselt
dann dauerhaft zum unprivilegierten Benutzer `app` (uid/gid 1000) – er rendert
nicht vertrauenswürdige PDFs mit poppler, und ein Exploit dort sollte keine
root-Rechte haben.

## Ports und Reverse-Proxys {#ports-and-reverse-proxies}

Der Server lauscht auf `$PORT` (80 im Image), und die Compose-Datei
veröffentlicht ihn auf `8090`. Der Healthcheck des Images fragt `/api/health` mit
einer großzügigen Startphase von 120 Sekunden ab, denn der erste Start führt
Migrationen aus und baut unter Umständen den Suchindex über ein bereits großes
Archiv neu auf.

Optionale Observability läuft über einen zweiten Port. Setzen Sie
[`METRICS_ADDR`](/de/setup#always-env-backed), um einen Prometheus-Scrape-Endpunkt
bereitzustellen; ungesetzt bleibt er aus. Ein reiner Port (`9464`) lauscht auf
jeder Schnittstelle innerhalb des Containers, was ein veröffentlichter Port oder
ein Scrape aus dem Compose-Netzwerk braucht. Die Portzuordnung steht bewusst nicht
in `docker-compose.yml`: Der Endpunkt hat keine Authentifizierung und sollte nicht
aus dem Internet erreichbar werden, nur weil eine Variable gesetzt wurde. Geben
Sie einen Host an (`127.0.0.1:9464`), um ihn auf Loopback zu halten, wenn Sie das
Binary direkt auf dem Host betreiben.

Die SPA ruft den Origin auf, von dem sie ausgeliefert wurde, daher **muss für den
Container keine URL konfiguriert werden** – `VITE_POCKETBASE_URL` in `.env` wirkt
sich nur auf ein aus dem Quellcode gebautes Frontend aus. Setzen Sie einen
TLS-terminierenden Proxy davor, lohnt es sich, zwei Dinge explizit zu setzen:

- [`PASSKEY_RP_ID` und `PASSKEY_ORIGINS`](/de/setup#always-env-backed), wenn der
  Proxy den ursprünglichen `Host` oder `X-Forwarded-Proto` nicht weiterleitet.
  Jeder registrierte Passkey ist an `PASSKEY_RP_ID` gebunden, eine spätere
  Änderung macht daher alle unbrauchbar.
- `IMPORT_ALLOW_PRIVATE=1`, wenn Sie aus einer Paperless-ngx-Instanz im selben LAN
  oder Docker-Netzwerk importieren. Cloud-Metadaten-Adressen bleiben in jedem Fall
  gesperrt.

### Traefik {#traefik}

Ein Overlay, damit die Basisdatei für sich allein nutzbar bleibt:

```yaml
# docker-compose.traefik.yml
#   docker compose -f docker-compose.yml -f docker-compose.traefik.yml up -d
services:
  app:
    # Traefik reaches the container over the shared network, so nothing needs
    # publishing on the host any more. (!override replaces the base list.)
    ports: !override []
    networks: [proxy]
    labels:
      traefik.enable: "true"
      # Which network to dial when the container is on more than one.
      traefik.docker.network: proxy
      traefik.http.routers.lemmary.rule: "Host(`archive.example.com`)"
      traefik.http.routers.lemmary.entrypoints: websecure
      traefik.http.routers.lemmary.tls.certresolver: letsencrypt
      # The port inside the container, not the 8090 the base file published.
      traefik.http.services.lemmary.loadbalancer.server.port: "${PORT:-80}"

networks:
  proxy:
    external: true
```

Das setzt voraus, dass Traefik bereits mit einem Entrypoint `websecure`, einem
Zertifikats-Resolver `letsencrypt` und dem Docker-Provider läuft, in einem
externen Netzwerk namens `proxy`.

Drei Dinge macht diese Konfiguration ohne weiteres Zutun richtig, und jedes davon
kann ein anderer Proxy falsch machen:

- **Passkeys brauchen keine Variablen.** Traefik leitet den ursprünglichen `Host`
  weiter und setzt `X-Forwarded-Proto`, genau das, woraus sich `PASSKEY_RP_ID` und
  `PASSKEY_ORIGINS` ableiten, wenn sie nicht gesetzt sind.
- **Uploads werden vom Proxy nicht begrenzt.** Traefik puffert standardmäßig
  keinen Request-Body, sodass sowohl ein 47-MB-Dokument als auch ein
  bereitgestelltes Archiv im Gigabyte-Bereich durchgehen – es gibt kein
  `client_max_body_size`, das erhöht werden müsste. Fügen Sie eine
  `buffering`-Middleware hinzu, setzen Sie deren `maxRequestBodyBytes` über
  `IMPORT_STAGING_MAX_BYTES`, sonst schlagen Archiv-Uploads fehl.
- **Deep Research streamt weiter.** `POST /api/app/search/stream` liefert
  Server-Sent Events, und Traefik streamt Antworten, statt sie zu puffern, sodass
  jede Suche, jeder Lesevorgang und jede Sichtung weiterhin erscheint, während sie
  stattfindet. Setzen Sie keine `compress`-Middleware davor, ohne
  `text/event-stream` auszunehmen, sonst kommen die Schritte am Ende auf einen
  Schlag an.
- **Lange Antworten brauchen Spielraum.** Ein Recherchelauf ist minutenlange
  Arbeit, und der Stream sendet alle 15 Sekunden einen Kommentar-Frame, damit die
  Verbindung nie untätig ist. Das genügt `idleTimeout`, aber **nicht**
  `respondingTimeouts.writeTimeout`: Wie Gos `http.Server.WriteTimeout` ist das
  eine absolute Frist ab Beginn der Antwort, die kein Heartbeat verlängert. Lassen
  Sie ihn auf dem Entrypoint, der die App ausliefert, auf 0, oder setzen Sie ihn
  über den längsten Lauf, den Sie erwarten.
  `forwardingTimeouts.responseHeaderTimeout` ist aus demselben Grund wichtig – er
  begrenzt die Wartezeit auf das erste Byte des Backends. Ein Verbindungsabbruch
  kostet nicht mehr die Antwort (der Lauf wird beendet und die Runde in jedem Fall
  gespeichert), aber wer zusieht, verliert den Fortschritt.

Mit [Verschlüsselung im Ruhezustand](/de/encryption) fügen Sie
`VAULT_ALLOW_INSECURE_GATE=1` hinzu: Die Entsperrschranke verweigert eine
Bind-Adresse, die nicht Loopback ist, und innerhalb eines Containers bindet die
App an `0.0.0.0`, egal ob der Port veröffentlicht oder nur über Traefik
erreichbar ist – von dort aus kann sie den Unterschied nicht erkennen. TLS endet
dann bei Traefik, sodass das Entsperrpasswort das Docker-Netzwerk im Klartext
durchquert. Das ist ein anderer Kompromiss als die Loopback-Veröffentlichung, die
`docker-compose.encrypted.yml` vornimmt, und sollte bewusst eingegangen werden.

## Im Alltag {#day-to-day}

```bash
# Create or reset the admin account (never resets a password that exists)
docker compose exec -u app app /app/lemmary superuser upsert admin@example.com 'your-password'

# Logs; LOG_LEVEL=info in .env turns on JSON slog on stdout
docker compose logs -f app

# Upgrade
docker compose pull && docker compose up -d
```

Migrationen laufen beim Start, ein Upgrade besteht also aus einem Pull und einem
Neustart. Explizite Argumente werden unverändert an das Binary durchgereicht,
wodurch `exec` und `docker compose run --rm app <subcommand>` funktionieren.

## Sicherungen {#backups}

Zwei Arten, und sie beantworten unterschiedliche Fragen:

- **Export in der App** – jeder angemeldete Benutzer lädt seine eigene Bibliothek
  als ein Zip herunter (Dateien, OCR-Text, Metadaten, Vorschaubilder, Taxonomie)
  und stellt sie in dieser oder einer anderen Instanz wieder her. Es enthält keine
  Einstellungen und keine API-Schlüssel. Siehe
  [Sicherung und Wiederherstellung](/de/setup#backup-and-restore).
- **Volume-Snapshot** – die gesamte Instanz, einschließlich Einstellungen und
  Schlüsseln. Stoppen Sie zuerst den Container, damit SQLite nicht mitten in einem
  Schreibvorgang kopiert wird:

  ```bash
  docker compose stop
  docker run --rm -v lemmary_app_data:/v -v "$PWD:/out" alpine \
    tar czf /out/lemmary-pb_data.tar.gz -C /v .
  docker compose start
  ```

## Ressourcen {#resources}

Das Image legt `OPENBLAS_NUM_THREADS=1` und `OMP_NUM_THREADS=1` fest: OpenBLAS und
OpenMP starten standardmäßig jeweils einen Thread pro Kern, und das in einem
Prozess, der bereits gleichzeitig Anfragen bedient; je ein Thread verhindert, dass
eine Vektorsuche auf einer kleinen Maschine den Rest des Servers ausbremst.

## Lokale OCR {#local-ocr}

Standardmäßig ist OCR eine gehostete API, was bedeutet, dass jeder Scan zu
jemand anderem hochgeladen wird. Ein zweites Overlay verlegt die OCR-Engine
stattdessen auf diesen Host – ein Sidecar-Container ohne veröffentlichten Port und
ohne API-Schlüssel:

```bash
docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml \
  --profile docling up -d
```

Danach `OCR_SDK=docling` in `.env` auf einem frischen Volume oder ein
Docling-Anbieter unter **Einstellungen → Anbieter** auf einer Instanz, die bereits
gestartet wurde. Umsonst ist das nicht: ein Image von mehreren Gigabyte, mehrere
Gigabyte RAM und Sekunden pro Seite statt Millisekunden. Lesen Sie zuerst
[Lokale OCR](/de/local_ocr), insbesondere den Abschnitt zum Timeout, der erhöht
werden muss.

## Verschlüsselung im Ruhezustand {#encryption-at-rest}

`VAULT_ENABLED=1` sorgt dafür, dass das Volume nur Chiffretext enthält, und startet
die Instanz gesperrt. Dafür wird ein speicherbasiertes Arbeitsverzeichnis
benötigt, das die App nicht selbst einrichten kann; genau das stellt das Overlay
bereit:

```bash
docker compose -f docker-compose.yml -f docker-compose.encrypted.yml up -d
```

Das Overlay legt außerdem die Größe des tmpfs fest, deaktiviert Swap für den
Container und veröffentlicht den Port nur noch auf Loopback. Lesen Sie
[Verschlüsselung im Ruhezustand](/de/encryption), bevor Sie sie aktivieren: Wer
jedes Kontopasswort *und* den Wiederherstellungscode verliert, verliert das
Archiv, und es gibt keine Möglichkeit für den Betreiber, das zu umgehen.

## Lokale Embeddings {#local-embeddings}

Deep Research kann Dokumente nach Bedeutung finden statt nur nach
Schlüsselwörtern, wofür ein Embedding-Modell nötig ist. Zwei weitere Overlays
betreiben eines auf diesem Host, statt jeden Abschnitt jedes Dokuments an einen
gehosteten Anbieter zu senden:

```bash
# CPU, runs anywhere
docker compose -f docker-compose.yml -f docker-compose.embeddings.yml up -d

# NVIDIA GPU -- instead of the CPU file, not alongside it
docker compose -f docker-compose.yml -f docker-compose.embeddings-gpu.yml up -d
```

Sie fügen einen Sidecar ohne veröffentlichten Port hinzu und binden ihn an, ohne
sonst etwas zu ändern: OCR, Extraktion und Chat bleiben, wo sie waren. Overlays
lassen sich stapeln, daher lassen sich Verschlüsselung und lokale Embeddings
kombinieren:

```bash
docker compose -f docker-compose.yml \
               -f docker-compose.encrypted.yml \
               -f docker-compose.embeddings.yml up -d
```

Planen Sie zusätzlich zu den Vektoren das Modell ein – ~2,2 GB Gewichte für das
standardmäßige `BAAI/bge-m3` – und beachten Sie, dass die Vektoren selbst unter
einem Tresor im tmpfs liegen. Siehe [Embeddings auf eigener
Hardware](/de/local_embeddings).
