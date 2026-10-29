# Lemmary Local AI unter Linux {#lemmary-local-ai-on-linux}

[Lemmary Local AI](https://github.com/buldezir/lemmary-local-ai) für Linux
betreibt Embeddings, OCR und optional ein Textmodell für Lemmary auf einem
Rechner mit NVIDIA-GPU, sodass Dokumente Ihr Netzwerk nie verlassen. Sie läuft
als einzelnes Programm oder als Docker-Image und beantwortet dieselbe API wie
[die Mac-App](/de/local_ai_macos), sodass Lemmary zwischen beiden wechseln kann,
ohne sein Archiv neu einzubetten.

**[Neueste Version herunterladen](https://github.com/buldezir/lemmary-local-ai/releases/latest)**

## Was sie bereitstellt {#what-it-serves}

Alle drei antworten auf einem Port, `5001`, hinter einem OpenAI-kompatiblen `/v1`:

| Aufgabe | Modell | Engine |
| --- | --- | --- |
| Embeddings | `BAAI/bge-m3` | llama.cpp auf der GPU |
| OCR | `lightonocr-2` | LightOnOCR-2 1B auf llama.cpp, zwei Seiten gleichzeitig |
| Extraktion (optional) | Gemma 4 E4B auf einer GPU mit 16 GB oder ein größeres Modell mit 24 GB | ein drittes llama.cpp |

Sie lädt und prüft die Modelle beim ersten Start und startet ein llama.cpp neu,
das sich beendet.

Das Textmodell ist für die Extraktion gedacht (Titel, Datum, Typ, Korrespondent,
Tags, Zusammenfassung). Die Extraktion läuft auf dem Modell für **Allgemeine KI**,
das auch „KI fragen“ und die KI-gestützte Suche beantwortet, und ein Modell dieser
Größe ist schwach bei den Werkzeugaufrufen und langen Unterhaltungen, die diese
brauchen. Als Allgemeine KI gewählt, übernimmt es also auch den Chat; bleibt der
Chat bei einem gehosteten Modell, bleibt die Extraktion dort ebenfalls. In beiden
Fällen gehört das **Erweitert**-Modell für Deep Research zu einem gehosteten
Anbieter.

## Voraussetzungen {#requirements}

- Eine NVIDIA-GPU mit Treiber 570 oder neuer. Etwa 5 GB VRAM für Embeddings und
  OCR; das Textmodell braucht je nach Modell eine GPU mit 16 oder 24 GB.
- Docker mit GPU-Zugriff oder, ohne Docker, eine Distribution mit glibc 2.38
  oder neuer, etwa Ubuntu 24.04 oder Debian 13.
- OCR braucht etwa 3,6 s pro Seite, daher reicht Lemmarys `OCR_TIMEOUT_SEC`
  (90 s) für etwa 25 Seiten; erhöhen Sie es für längere Dokumente.

```bash
docker run --gpus all -p 5001:5001 -v lemmary-local-ai:/data \
  ghcr.io/buldezir/lemmary-local-ai:latest -text-model gemma-4-e4b
```

## In Lemmary {#in-lemmary}

Legen Sie unter **Admin → KI → Anbieter** einen Anbieter vom Typ
**Lemmary Local AI** an:

- Basis-URL: `http://<host>:5001/v1`;
- kein API-Schlüssel.

Binden Sie dann unter **Admin → KI → Modelle** die Modelle. Jede Auswahl zeigt
nur die Modelle für ihre Aufgabe: `BAAI/bge-m3` für Embeddings, `lightonocr-2`
für OCR und das Textmodell, solange es läuft, für Allgemeine KI.

Auf einer frischen Installation erledigt **Vollständig lokal** im
Einrichtungsassistenten beides: Es fragt nur nach der Adresse und öffnet den
Modellschritt mit allen drei Bindungen.

Die [README](https://github.com/buldezir/lemmary-local-ai/blob/main/linux/README.md)
beschreibt die Einstellungen, den Betrieb ohne Docker oder als systemd-Dienst
und vergleicht die OCR- und Textmodelle.
