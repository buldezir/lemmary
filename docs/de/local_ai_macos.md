# Lemmary Local AI auf einem Mac {#lemmary-local-ai-on-a-mac}

[Lemmary Local AI](https://github.com/buldezir/lemmary-local-ai) ist eine
Menüleisten-App für macOS, die Embeddings, OCR und optional ein Textmodell für
Lemmary auf einem Mac mit Apple Silicon betreibt, sodass Dokumente Ihr Netzwerk
nie verlassen. Lemmary selbst läuft anderswo (auf einer VM, einem Linux-Server,
irgendetwas mit Docker) und erreicht den Mac über das LAN.

**[Neueste Version herunterladen](https://github.com/buldezir/lemmary-local-ai/releases/latest)**

## Was sie bereitstellt {#what-it-serves}

Alle drei antworten auf einem Port, `5001`, hinter einem OpenAI-kompatiblen `/v1`:

| Aufgabe | Modell | Engine |
| --- | --- | --- |
| Embeddings | `BAAI/bge-m3` | llama.cpp auf der GPU des Macs, mitgeliefert |
| OCR | `apple-vision-ocr` | Apple Vision, in macOS eingebaut |
| Extraktion (optional) | Gemma 4 E4B oder ein größeres Modell auf einem Mac mit 24 oder 32 GB | ein zweites llama.cpp |

Sie lädt und prüft die Modelle beim ersten Start, startet bei der Anmeldung und
aktualisiert sich selbst.

Das Textmodell ist für die Extraktion gedacht (Titel, Datum, Typ, Korrespondent,
Tags, Zusammenfassung). Die Extraktion läuft auf dem Modell für **Allgemeine KI**,
das auch „KI fragen“ und die KI-gestützte Suche beantwortet, und ein Modell dieser
Größe ist schwach bei den Werkzeugaufrufen und langen Unterhaltungen, die diese
brauchen. Als Allgemeine KI gewählt, übernimmt es also auch den Chat; bleibt der
Chat bei einem gehosteten Modell, bleibt die Extraktion dort ebenfalls. In beiden
Fällen gehört das **Erweitert**-Modell für Deep Research zu einem gehosteten
Anbieter.

## Voraussetzungen {#requirements}

- Apple Silicon, macOS 26 oder neuer.
- Etwa 1,5 GB Arbeitsspeicher für Embeddings und OCR. Das Textmodell braucht je
  nach Modell 5,7 bis 18,7 GB zusätzlich.
- Sie läuft in einer angemeldeten Sitzung: Schalten Sie auf einem Mac ohne
  Bildschirm die automatische Anmeldung ein und den Ruhezustand aus.

## In Lemmary {#in-lemmary}

Legen Sie unter **Admin → KI → Anbieter** einen Anbieter vom Typ
**Lemmary Local AI** an:

- Basis-URL: die, die das Menüleisten-Fenster anzeigt, z. B. `http://<mac>:5001/v1`;
- kein API-Schlüssel.

Binden Sie dann unter **Admin → KI → Modelle** die Modelle. Jede Auswahl zeigt
nur die Modelle für ihre Aufgabe: `BAAI/bge-m3` für Embeddings,
`apple-vision-ocr` für OCR und das Textmodell, solange es läuft, für
Allgemeine KI.

Auf einer frischen Installation erledigt **Vollständig lokal** im
Einrichtungsassistenten beides: Es fragt nur nach der Adresse und öffnet den
Modellschritt mit allen drei Bindungen.

Die [README](https://github.com/buldezir/lemmary-local-ai#readme) beschreibt die
Einstellungen, vergleicht die Textmodelle und erklärt den Umstieg von einer
selbst gebauten Einrichtung mit llama.cpp und docling.
