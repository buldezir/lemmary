# Geführte Einrichtung der KI-Anbieter {#guided-ai-provider-setup}

Der kurze Weg zu einer funktionierenden Instanz: **Mistral** für OCR und Embeddings im
kostenlosen Tarif, **Opencode Go** für das Sprachmodell. Zwei Schlüssel, fünf Minuten,
und die gesamte Pipeline funktioniert – Extraktion, Chat, Deep Research und
bedeutungsbasierte Suche.

Besorgen Sie die beiden Schlüssel (Schritte 1 und 2) und fügen Sie sie dann in den
**Einrichtungsassistenten** ein, mit dem eine frische Instanz startet, oder in die
**Einstellungen** einer bereits laufenden Instanz (Schritt 3). Nichts muss in eine
Konfigurationsdatei; siehe
[Vorbelegung aus `.env`](#advanced-seeding-it-from-env), wenn eine Instanz lieber
bereits konfiguriert starten soll. Das Gesamtbild und jede Alternative finden Sie unter
[KI-Anbieter und Modelle](/de/ai_providers).

---

## Schritt 1: Ein Mistral-API-Schlüssel, kostenlos {#step-1-a-mistral-api-key-for-free}

Der kostenlose Tarif von Mistral („Experiment“) bietet ratenbegrenzten Zugriff auf den
gesamten Katalog, einschließlich des Document-OCR-Endpunkts und des Embedding-Modells.
Keine Kreditkarte, aber eine Telefonnummer wird verlangt.

1. Rufen Sie [console.mistral.ai](https://console.mistral.ai/) auf und registrieren Sie sich.
2. Bestätigen Sie Ihre Telefonnummer, wenn Sie dazu aufgefordert werden – ohne sie wird
   der kostenlose Tarif nicht aktiviert.
3. Akzeptieren Sie die Bedingungen für den kostenlosen Tarif unter **Billing**, falls Sie
   dazu aufgefordert werden (achten Sie darauf, dass Sie bei *Experiment* landen und nicht
   bei einem kostenpflichtigen Tarif).
4. Öffnen Sie **API Keys** → **Create new key**, benennen Sie ihn und kopieren Sie den
   Wert. Er wird nur einmal angezeigt.
5. Rufen Sie [admin.mistral.ai/plateforme/privacy](https://admin.mistral.ai/plateforme/privacy)
   auf und **deaktivieren Sie die Datenerhebung** – im kostenlosen Tarif sind Sie
   standardmäßig für das Training mit Ihren Daten angemeldet, und gesendet werden Ihre
   Dokumente.

### Die beiden zuzuweisenden Modelle {#the-two-models-to-bind}

| Aufgabe | Modell | Hinweise |
| --- | --- | --- |
| OCR | `mistral-ocr-latest` | Die eigens dafür gebaute [Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr), kein Chat-Modell, das einen Scan liest. Verarbeitet PDFs, Bilder und Office-Dokumente; 1000 Seiten pro Datei. |
| Embeddings | `mistral-embed` | 1024 Dimensionen, belegt also weniger Platz als ein Modell mit 1536 Dimensionen – was bei aktivierter [Verschlüsselung](/de/encryption) zählt, wo das Archiv in den Arbeitsspeicher entschlüsselt wird. |

Mistral kann auch das Sprachmodell bereitstellen (`mistral-small-latest` und größer),
daher ist schon dieser eine Schlüssel eine vollständige Installation – ein Anbieter, jede
Aufgabe. In Schritt 2 geht es darum, ein besseres Sprachmodell zu bekommen.

---

## Schritt 2: Ein Opencode-Go-Schlüssel für das Sprachmodell {#step-2-an-opencode-go-key-for-the-language-model}

[Opencode Go](https://opencode.ai/go?ref=84VDFS18QN) ist ein einziges Abonnement für
einen Katalog von Modellen mehrerer Hersteller statt eines nutzungsbasiert abgerechneten
Schlüssels pro Hersteller. Lemmary hat ein eigenes `opencode`-SDK, daher ist der
Schlüssel die gesamte Konfiguration – es weiß bereits, auf welchem der drei Endpunkte von
Opencode jedes Modell bereitgestellt wird.

1. Schließen Sie das Abonnement unter [opencode.ai/go](https://opencode.ai/go?ref=84VDFS18QN) ab.
2. Melden Sie sich an und öffnen Sie den Bereich **API keys** im Dashboard; erstellen Sie
   einen Schlüssel und kopieren Sie ihn. Mehr braucht Lemmary nicht – keine Basis-URL,
   keine Konfiguration pro Modell.

### Welches Modell {#which-model}

| | Modell | Warum |
| --- | --- | --- |
| **Empfohlen** | `gpt-6-luna` | Das Standard-Extraktionsmodell von Lemmary. Von den dreien das stärkste darin, strukturierte Metadaten aus einem unordentlichen Scan herauszuziehen, und beim mehrstufigen Lesen von Deep Research. |
| Alternative | `deepseek-v4-flash` | Schnell und günstig im selben Abonnement. Ein gutes Modell für **Allgemeine KI**, mit Luna als **Erweitertem Modell** für Deep Research. |
| Alternative | `qwen3.8-flash` | Die andere schnelle Option; probieren Sie sie aus, wenn DeepSeek ratenbegrenzt ist oder Ihre Ergebnissprache ablehnt. |

### Warum nicht auch die OCR über Opencode laufen lassen {#why-not-run-ocr-on-opencode-too}

Das ist möglich – ein Opencode-Modell, das Dateien verarbeiten kann, liest einen Scan so
wie jedes Vision-Modell, und schon ein Opencode-Schlüssel allein ist eine vollständige
Installation. Für die OCR-Zuweisung ist Mistral trotzdem die bessere Wahl:

- **Schneller und günstiger pro Seite.** Ein dedizierter OCR-Endpunkt liefert
  Markdown pro Seite; ein Chat-Modell arbeitet sich schlussfolgernd durch dasselbe Bild.
- **Besser bei großen Dateien.** Die Document OCR von Mistral nimmt ein ganzes PDF an – bis
  zu 50 MB und 1000 Seiten, knapp über den 47 MB, die Lemmary pro Upload akzeptiert –, während
  bei einem Chat-Modell der Kontext die Obergrenze ist und lange Scans abgeschnitten oder
  abgelehnt werden.
- **Den Mistral-Schlüssel brauchen Sie ohnehin.** Opencode bietet überhaupt keinen
  `/embeddings`-Endpunkt, daher muss die bedeutungsbasierte Deep Research über Mistral laufen.
  Da der Schlüssel schon vorhanden ist, kostet es nichts extra, die OCR daran zu binden.

---

## Schritt 3: In Lemmary eintragen {#step-3-enter-them-in-lemmary}

Eine frische Instanz öffnet den **Einrichtungsassistenten**, nachdem Sie das
Administratorkonto erstellt haben; eine bereits laufende Instanz hat dieselben Felder unter
**Einstellungen** (für Administratoren sichtbar). So oder so sind es zwei Anbieter und drei
Zuweisungen.

### Die Anbieter hinzufügen {#add-the-providers}

Der Assistent startet mit **KI-Anbieter verbinden**, wo genau diese beiden Schlüssel
abgefragt und beide Einträge mit einem einzigen Absenden angelegt werden:

| Feld | Wert |
| --- | --- |
| **Mistral-API-Schlüssel** | der Schlüssel aus Schritt 1 |
| **Allgemeiner KI-Anbieter** | Opencode Go und der Schlüssel aus Schritt 2 |

Lassen Sie den zweiten Schlüssel leer, um alles über Mistral laufen zu lassen. Alles, was
dieses Paar nicht abdeckt – eine Anmeldung mit ChatGPT, ein lokaler Sidecar, später ein
zweiter Schlüssel –, finden Sie hinter **Stattdessen einen Anbieter manuell hinzufügen**;
dasselbe bietet **Einstellungen → Anbieter** auf einer bereits laufenden Instanz. Aliasse
und Basis-URLs werden für Sie ausgefüllt.

### Die Modelle zuweisen {#bind-the-models}

Der Schritt **Modelle wählen** des Assistenten kommt mit allen drei Zuweisungen, die bereits
anhand der obigen Schlüssel ausgefüllt sind; **Einstellungen → Modelle** hat dieselben Felder.

| Zuweisung | Anbieter | Modell |
| --- | --- | --- |
| OCR | Mistral | `mistral-ocr-latest` |
| Allgemeine KI | Opencode Go | `gpt-6-luna` |
| Embeddings *(optional)* | Mistral | `mistral-embed` |

Allgemeine KI übernimmt hier alles, was ein Sprachmodell tut, daher sind diese drei Felder
alles, was eine funktionierende Installation braucht. **Einstellungen → Modelle** ergänzt ein
optionales **Erweitertes Modell**, das nur die Denkschleife von Deep Research antreibt. Setzen
Sie den Embedding-Anbieter auf **Keiner**, damit Deep Research nur mit Stichwörtern arbeitet –
das ist die eine Zuweisung, von der nichts anderes abhängt. Wenn ein gewünschtes Modell nicht
in einer Auswahlliste steht, geben Sie es unter **Eigene Modell-ID** ein. Nach
**Einrichtung abschließen** wird der erste Upload per OCR erfasst, getaggt und durchsuchbar.

Alles hier wird im laufenden Betrieb neu geladen: Die Änderung eines Anbieters oder einer
Zuweisung in den Einstellungen wirkt ab der nächsten Anfrage, ohne Neustart.

Zwei Dinge sollten Sie wissen, bevor Sie Embeddings zugewiesen lassen: Damit verpflichten Sie
sich, das gesamte Archiv einzubetten, nicht nur den nächsten Upload – siehe
[was Embeddings kosten](/de/ai_providers#what-embeddings-cost) –, und ein Modellwechsel bettet
alles neu ein, weil sich Vektoren aus zwei Modellen nicht vergleichen lassen.

---

## Fortgeschritten: Vorbelegung aus `.env` {#advanced-seeding-it-from-env}

Nur nützlich, wenn eine *frische* Instanz bereits konfiguriert starten soll – bei einem
skriptgesteuerten Deployment oder einem Volume, das Sie neu anlegen wollen. Die Variablen
werden **nur beim ersten Start** gelesen, wenn der Einstellungsdatensatz noch nicht existiert;
danach sind die **Einstellungen** maßgeblich, und eine Änderung an `.env` bewirkt nichts.

```dotenv
# General AI — extraction, Ask AI, search, Deep Research
AI_SDK=opencode
AI_API_KEY=sk-your-opencode-key
AI_MODEL=gpt-6-luna

# OCR — Mistral's Document OCR API
OCR_SDK=mistral
OCR_API_KEY=your-mistral-key
OCR_MODEL=mistral-ocr-latest

# embeddings — meaning-based Deep Research, same Mistral key
AI_EMBEDDING_SDK=mistral
AI_EMBEDDING_API_KEY=your-mistral-key
AI_EMBEDDING_MODEL=mistral-embed
```

Das ist dieselbe Konfiguration, die Schritt 3 erzeugt, daher fragt der Assistent nur nach dem
Administratorkonto und ist dann fertig. Jede Variable ist unter
[Der Anbieter-Block](/de/ai_providers#the-provider-block) beschrieben.

## Wenn etwas nicht funktioniert {#if-something-does-not-work}

- **Der Assistent fragt immer wieder nach einem Anbieter** – er braucht beide Aufgaben
  abgedeckt: einen Sprachmodell-Anbieter *und* einen OCR-Anbieter. Der Opencode-Go-Eintrag
  erfüllt die erste, der Mistral-Eintrag die zweite.
- **`.env` hat nichts geändert** – diese Variablen gelten nur beim ersten Start eines frischen
  Volumes. Auf einer Instanz, die bereits gestartet wurde, verwenden Sie die **Einstellungen**.
- **Jede Anfrage an Opencode schlägt mit 500 oder 404 fehl** – das SDK muss `opencode` sein,
  nicht `openai` mit einer darauf zeigenden Basis-URL. Siehe
  [die Routing-Tabelle](/de/ai_providers#troubleshooting).
- **Mistral gibt 429 zurück** – der kostenlose Tarif ist ratenbegrenzt; die Pipeline versucht
  es mit einem Backoff erneut, und kein Dokument geht verloren.
