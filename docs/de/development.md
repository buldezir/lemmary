# Entwicklungsumgebung {#development-environment}

Der unterstützte Installationsweg ist das veröffentlichte [Docker-Image](/de/self_hosting).
Diese Anleitung richtet sich nur an Mitwirkende und an alle, die Lemmary bewusst
direkt auf dem Host bauen oder ausführen möchten.

## Voraussetzungen {#prerequisites}

- Go 1.27+ mit aktiviertem cgo (eine C-Toolchain: `gcc` oder `clang`)
- Node.js 20+
- [pnpm](https://pnpm.io/installation) 11+ (`npm install -g pnpm`)
- [poppler-utils](https://poppler.freedesktop.org/) für alle PDF-Arbeiten: `pdftoppm`
  (Vorschauen und Seitenminiaturen), `pdfinfo` (Seitenzahlen), `pdftotext`
  (Seitentext), `pdfseparate` und `pdfunite` (Aufteilen von Dokumenten)

Führen Sie unter macOS `brew install poppler` aus. Unter Debian oder Ubuntu führen Sie
`apt install poppler-utils` aus.

Diese Host-Abhängigkeiten sind bewusst keine Voraussetzungen für das gewöhnliche
Self-Hosting: Sie sind bereits im Docker-Image enthalten.

## FAISS {#faiss}

FAISS wird zum Bauen des Backends benötigt. Die Suche basiert auf
[Bleve](https://github.com/blevesearch/bleve), dessen Vektorunterstützung eine
cgo-Anbindung an FAISS ist. Bleve kompiliert diese API heraus, sofern das Build-Tag
`vectors` nicht gesetzt ist, und Lemmary wird immer damit gebaut: ein Binary und ein
Image, mit Vektorsuche. Ein Build ohne das Tag bricht sofort in
`backend/internal/fulltext/vectors_required.go` ab.

Die Bibliothek muss **Bleves Fork** von FAISS sein. Ein `libfaiss`-Paket der
Distribution genügt nicht, weil die Go-Anbindung C-Einstiegspunkte aufruft, die es
nur im Fork gibt. `scripts/faiss-build.sh` verwaltet den festgelegten Commit.
Es ist die einzige maßgebliche Quelle und ändert sich nur, wenn sich Bleve ändert,
gemäß der Kompatibilitätstabelle in Bleves `docs/vectors.md`.

Jeder der folgenden Wege benötigt außerdem OpenBLAS und libgomp, sowohl beim Linken
als auch beim Ausführen des Backends (`apt install libopenblas0-pthread libgomp1`;
`libopenblas-dev` bringt sie mit).

```bash
# Option 1 — install system-wide. One sudo, and nothing to set afterwards.
sudo apt install cmake ninja-build g++ libopenblas-dev
sudo scripts/faiss-build.sh --prefix /usr/local && sudo ldconfig

# Option 2 — install in your home directory. Same build, no root.
scripts/faiss-build.sh --prefix "$HOME/.local/faiss"

# Option 3 — export the artifacts from the Docker build. This needs no local
# cmake or compiler; the exported FAISS stage is only about 10 MB.
docker buildx build --target faiss --output type=local,dest=./.faiss .
mkdir -p "$HOME/.local/faiss" && cp -a .faiss/lib .faiss/include "$HOME/.local/faiss/"
```

Die Optionen 2 und 3 legen FAISS außerhalb der normalen Pfade von Compiler und Loader ab:

```bash
export CGO_CFLAGS=-I$HOME/.local/faiss/include
export CGO_LDFLAGS=-L$HOME/.local/faiss/lib
export LD_LIBRARY_PATH=$HOME/.local/faiss/lib
```

Die `.envrc` des Repositorys setzt alle drei, wenn `~/.local/faiss` existiert. Mit
[direnv](https://direnv.net) führen Sie `direnv allow` aus; es exportiert außerdem
`GOFLAGS=-tags=vectors`, sodass ein einfaches `go build`, `go test` und gopls in
diesem Verzeichnisbaum funktionieren. Ohne direnv übergeben Sie `-tags vectors` oder
setzen es einmalig mit `go env -w GOFLAGS=-tags=vectors`.

FAISS muss nur für Go-Befehle auf dem Host vorhanden sein, die dort ausgeführt
werden: ein einfaches `go build`/`go test` oder die Verifikation im Host-Modus. Die
normale Verifikationssuite läuft in Docker, wo es bereits installiert ist.

Führen Sie unter macOS `brew install cmake ninja libomp openblas` aus und verwenden Sie
dann Option 1 oder 2; das Build-Skript findet die libomp von Homebrew automatisch.

## Aus dem Quellcode ausführen {#run-from-source}

Kopieren Sie zuerst die Beispielumgebung. Laufzeitoptionen werden im
[Konfigurationsleitfaden](/de/setup) erklärt, KI-spezifische Optionen unter
[KI-Anbieter und Modelle](/de/ai_providers).

```bash
cp .env.example .env
```

Starten Sie das Backend:

```bash
cd backend
go run . serve --http=127.0.0.1:8090
```

Beim ersten Start legen Migrationen die PocketBase-Collections an:

- `tags`
- `correspondents`
- `document_types`
- `documents`
- `processing_jobs`
- `app_settings`
- `ai_providers`
- `outbound_emails`

Die App öffnet dann denselben
[Assistenten für den ersten Start](/de/setup#first-launch-setup-wizard) wie eine
Docker-Installation. `app_settings` und `ai_providers` werden beim ersten Start aus
`.env` vorbelegt.

Bauen Sie Frontend und Dokumentation einmal und starten Sie dann das Backend neu:

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm run build
```

Dadurch wird die App nach `public/` und die Dokumentation nach `public/docs/`
geschrieben; das Backend liefert beides unter seiner eigenen Adresse aus.

## Nützliche Befehle {#useful-commands}

```bash
# Frontend production build (SPA -> ../public, docs -> ../public/docs)
cd frontend && pnpm run build

# Create or update an admin (PocketBase superuser + paired users account)
cd backend && go run . superuser upsert admin@example.com 'your-password'
```
