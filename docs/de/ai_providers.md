# KI-Anbieter und Modelle {#ai-providers-and-models}

Lemmary braucht von einem KI-Anbieter zwei Dinge: **OCR** für PDFs und Bilder und ein
**Sprachmodell** für Metadatenextraktion, Chat mit Dokumenten und Deep Research. Ein
**Embedding-Modell** ist optional und ergänzt Deep Research und das Suchfeld der
Dokumente um bedeutungsbasierte Suche.

Ein einziger Anbieter kann alle drei bedienen. Konfigurieren Sie sie auf der
Admin-Seite **Einstellungen** oder belegen Sie sie vor dem ersten Start aus `.env` vor,
damit eine frische Instanz sofort einsatzbereit startet.

## Einen Anbieter wählen {#choosing-a-provider}

| Anbieter | Sprachmodell | OCR | Embeddings | Websuche |
| --- | --- | --- | --- | --- |
| **Opencode Go** — `opencode` | ✅ **ein Katalog von Modellen in einem Abonnement** | ✅ Modelle, die Dateien/Bilder annehmen | ❌ | ❌ |
| **`mistral`** | ✅ Chat Completions | ✅ dedizierte Document OCR API | ✅ `mistral-embed` | ❌ |
| `openai` | ✅ | ✅ Modelle, die Dateien/Bilder annehmen | ✅ | ❌ |
| `anthropic` | ✅ Claude, direkt | ✅ Bilder und PDFs | ❌ | ❌ |
| `openrouter` | ✅ viele Hersteller mit einem Schlüssel | ✅ Modelle, die `file`-Eingabe angeben | ✅ | ❌ |
| `google_vision` | ❌ | ✅ | ❌ | ❌ |
| **ChatGPT-Abonnement** — `chatgpt` | ✅ **mit einem ChatGPT-Abonnement** | ✅ **über denselben Platz** | ❌ | ❌ |
| **Local OCR** — `docling` | ❌ | ✅ **auf Ihrem eigenen Host** | ❌ | ❌ |
| **Local Embeddings** — `local` | ❌ | ❌ | ✅ **auf Ihrer eigenen Hardware** | ❌ |
| **Tavily** — `tavily` | ❌ | ❌ | ❌ | ✅ **das einzige SDK, das das kann** |

Die fett gedruckten Namen sind das, was die Einstellungen anzeigen. Die kürzeren
Code-Werte bleiben die Werte, die von `OCR_SDK`, `AI_EMBEDDING_SDK` und `WEB_SEARCH_SDK`
verwendet werden.

**Beginnen Sie mit Mistral.** Es ist das einzige SDK, das jede Aufgabe von Lemmary
abdeckt, sodass ein Schlüssel und ein Anbieter-Eintrag die ganze Instanz konfigurieren –
OCR, Extraktion, Chat, Deep Research und Embeddings –, ohne dass noch etwas zuzuweisen
bleibt. Seine OCR ist ein eigens dafür gebauter Dokument-Endpunkt statt eines
allgemeinen Modells, das einen Scan lesen soll, und sein Katalog gibt die Fähigkeiten
jedes Modells an, sodass die Modellauswahl in den Einstellungen die richtigen Modelle
statt des ganzen Katalogs anzeigt.

**Oder beginnen Sie mit Opencode Go**, wenn Ihnen ein Abonnement besser passt als
nutzungsbasiert abgerechnete Schlüssel: `AI_SDK=opencode` und ein Schlüssel sind die
gesamte Konfiguration, und es deckt Extraktion, Chat, Deep Research und OCR über einen
Katalog von Modellen mehrerer Hersteller ab. Embeddings erzeugt es nicht – siehe den
Punkt unten –, daher gleicht Deep Research nur Stichwörter ab, bis Sie dafür einen
zweiten Anbieter hinzufügen.

Die Alternativen verdienen eine Erwähnung:

- **Opencode Go** – ein Abonnement für einen Katalog von Modellen (GLM, Kimi,
  DeepSeek, Qwen, MiniMax, Grok und weitere). Anders als jedes andere SDK hier
  bedient es nicht eine einzige API: Jedes Modell liegt auf `/chat/completions`,
  `/responses` oder Anthropics `/messages`, und welcher Endpunkt es ist, ist eine
  Eigenschaft des Modells, die der Katalog nicht meldet – daher führt Lemmary die
  Tabelle selbst und spricht alle drei. Das ist auch der Grund, warum es ein eigenes
  SDK ist und nicht `openai` mit einer Basis-URL: Bei jedem anderen SDK geht jedes
  Modell an `/chat/completions`, wo zwei Drittel des Katalogs nicht bedient werden,
  und der erforderliche Header `x-opencode-session` würde nicht gesendet. Embeddings
  werden abgelehnt, wie bei einem ChatGPT-Platz: Das Gateway hat kein `/embeddings`,
  und der Katalog enthält kein Embedding-Modell; kombinieren Sie es also mit
  `AI_EMBEDDING_SDK=local` oder einem nutzungsbasiert abgerechneten Schlüssel, wenn Sie
  bedeutungsbasierte Suche möchten. Sein SDK-Wert ist `opencode`.
- **`openai`** – wählen Sie dies, wenn Sie bereits einen Schlüssel haben, oder um
  `AI_BASE_URL` auf ein OpenAI-kompatibles Gateway oder einen selbst gehosteten
  Endpunkt zu richten. Sein `/v1/models` beschreibt nichts, daher zeigen die
  Modellauswahlen den vollständigen Katalog mit einem Hinweis, für OCR ein Modell zu
  wählen, das Dateien verarbeiten kann.
- **`anthropic`** – Claude direkt von Anthropic, abgerechnet pro Token. Es ist ein
  eigenes SDK und nicht `openai` mit einer Basis-URL, weil es ein anderes
  Übertragungsprotokoll ist: `/v1/messages`, mit dem System-Prompt außerhalb der
  Nachrichtenliste und einem Caching, das angefordert werden muss, statt automatisch
  zu greifen. Es chattet, extrahiert und liest Dokumente – Bilder und PDFs gehen an
  das Modell, wie bei `openai` – und bietet überhaupt keine Embeddings; kombinieren Sie
  es also mit `AI_EMBEDDING_SDK=local` oder einem nutzungsbasiert abgerechneten
  Schlüssel, wenn Sie bedeutungsbasierte Suche möchten. Anfragen verlangen
  `output_config.effort: low`, womit Claude für Arbeit dieser Art am günstigsten ist,
  und schalten das Thinking ab, weil eine Tool-Schleife hier einen Thinking-Block nicht
  wiedergeben kann und ein ohne ihn wiedergegebener Turn abgelehnt wird. Welche dieser
  beiden Einstellungen ein Modell annimmt – und ob es überhaupt eine `temperature`
  annimmt, die Claude nach Opus 4.6 entfernt hat –, hängt von seiner Generation ab;
  daher wird jede auf gut Glück gesendet und bei Ablehnung weggelassen: Die erste
  Anfrage an ein Modell kann einen erneuten Versuch kosten, und die Antwort wird dann
  gespeichert. Eine Einschränkung sollten Sie kennen, bevor Sie ein Modell zuweisen: Ein
  Claude, das sich weigert, das Thinking abzuschalten – die Fable-Familie lässt es
  eingeschaltet –, kann Extraktion und OCR übernehmen, aber nicht KI fragen, Suche oder
  Deep Research, deren Tool-Schleifen im zweiten Turn abgelehnt werden, weil die
  Thinking-Blöcke nicht übernommen werden können; es ist also kein Modell für
  Allgemeine KI in einer Installation, die diese Funktionen nutzt. Alle aktuellen
  Opus-, Sonnet- und Haiku-Modelle sind nicht betroffen. Sein SDK-Wert ist `anthropic`.
- **`openrouter`** – ein Schlüssel für viele Hersteller und der einzige Anbieter, der
  seinen Katalog serverseitig filtert (`input_modalities=file` für OCR,
  `output_modalities=embeddings` für Embeddings), sodass beide Auswahlen stimmen.
- **`google_vision`** – nur OCR; es kann keine Extraktion übernehmen und wird als
  `AI_SDK` abgelehnt. Lohnt sich in Kombination mit einem LLM, wenn sein kostenloses
  Kontingent (1000 Seiten pro Monat) eine Rolle spielt. Siehe
  [Google-Vision-API-Schlüssel](/de/google_vision).
- **ChatGPT-Abonnement** – alles außer Embeddings, abgerechnet über ein ChatGPT-Plus-,
  Pro- oder Business-Abonnement statt pro Token. Es gibt keinen Schlüssel zum
  Einfügen: Sie fügen den Anbieter hinzu, melden sich mit einem Gerätecode an und
  weisen ihm Chat, Extraktion, Deep Research oder OCR zu. OCR funktioniert wie bei
  `openai` – die Datei geht an das Modell –, daher ist eine Instanz, deren einziger
  KI-Zugang ein ChatGPT-Platz ist, eine vollständige Installation. Embeddings werden
  abgelehnt: Der Endpunkt hat überhaupt kein `/embeddings`. Es greift auf die eigenen
  Codex-Endpunkte von OpenAI zu, daher trägt das Konto, mit dem Sie sich anmelden, das
  Risiko. Sein SDK-Wert ist `chatgpt`, allerdings ist es kein Wert für `AI_SDK` oder `OCR_SDK`: Eine Anmeldung lässt sich
  nicht in `.env` schreiben. Siehe [Anmeldung mit ChatGPT](/de/chatgpt_login).
- **Local OCR** – nur OCR, und der einzige Anbieter, der ein Dokument liest, ohne es
  irgendwohin zu senden: ein Sidecar-Container neben der App, ohne veröffentlichten
  Port und **ganz ohne API-Schlüssel** – die Basis-URL ist die gesamte Konfiguration.
  Greifen Sie darauf zurück, wenn das Archiv so vertraulich ist, dass eine gehostete
  OCR-API nicht infrage kommt, oder wenn der Host keinen ausgehenden Internetzugang
  hat; kombinieren Sie es mit einem lokalen OpenAI-kompatiblen Endpunkt unter
  `AI_BASE_URL`, und nichts verlässt die Maschine. Seine Standard-Erkennung sind die
  PP-OCR-Modelle von PaddleOCR, daher gibt es keinen separaten PaddleOCR-Anbieter zur
  Auswahl. Der Preis ist real: ein Image von mehreren Gigabyte und Sekunden statt
  Millisekunden pro Seite. Sein SDK-Wert ist `docling`. Siehe [Lokale OCR](/de/local_ocr).
- **Local Embeddings** – nur Embeddings, mit einem Modell, das Sie selbst betreiben.
  Wie Local OCR benötigt es keinen API-Schlüssel: Die Basis-URL ist die gesamte
  Konfiguration. Es ist das Spiegelbild von `google_vision` – eine einzige Aufgabe,
  erledigt ohne Netzwerk nach außen. Sein SDK-Wert ist `local`. Siehe
  [Lokale Embeddings](/de/local_embeddings).

Zusammen sind Local OCR und Local Embeddings die beiden Hälften einer Installation, bei
der nichts den Host verlässt: Richten Sie `AI_BASE_URL` auf Ollama oder vLLM, setzen Sie
`OCR_SDK=docling` und setzen Sie `AI_EMBEDDING_SDK=local`.

Ohne einen Sprachmodell-Anbieter geben KI-Extraktion, Chat mit Dokumenten und Deep
Research einen Konfigurationsfehler zurück.

## Der Anbieter-Block {#the-provider-block}

Die folgenden Anbieter und Modelle belegen die Datenbank **nur beim ersten Start** vor,
wenn die Einstellungszeile noch nicht existiert, damit ein frisches Volume einsatzbereit
statt im Einrichtungsassistenten startet. Danach sind die **Einstellungen** maßgeblich,
und eine Änderung an `.env` bewirkt nichts. Ein fehlender Schlüssel ist kein Fehler: Der
Einrichtungsassistent fragt danach.

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `AI_SDK` | `openai` | Das SDK des Sprachmodells: `opencode`, `openai`, `anthropic`, `openrouter` oder `mistral`. `google_vision`, `docling` (Local OCR) und `local` (Local Embeddings) werden abgelehnt – keines davon kann Extraktion übernehmen. `chatgpt` ebenfalls: Es hat keinen Schlüssel, der sich aus der Umgebung vorbelegen ließe. |
| `AI_API_KEY` | leer | Sein Zugangsschlüssel. **Ein Schlüssel ist meist die gesamte Konfiguration**: Damit und mit nichts anderem legt die App einen Anbieter an und leitet Extraktion, Chat, Deep Research *und* OCR dorthin. |
| `AI_MODEL` | `gpt-6-luna` | Das Modell für **Allgemeine KI**: Extraktion, KI fragen, KI-gestützte Suche und das massenhafte Lesen von Dokumenten durch Deep Research. Achten Sie darauf, dass es die in den **Einstellungen** festgelegte Ergebnissprache unterstützt. |
| `AI_BASE_URL` | der eigene Endpunkt des SDKs | Eine OpenAI-kompatible Basis-URL, für ein Gateway oder einen selbst gehosteten Endpunkt. Lassen Sie sie für `opencode` ungesetzt, dessen eigener Endpunkt `https://opencode.ai/zen/go/v1` ist. |
| `OCR_SDK` | ungesetzt (OCR läuft über den `AI_SDK`-Anbieter) | Ein separater Anbieter für OCR: `opencode`, `openai`, `anthropic`, `openrouter`, `mistral`, `google_vision` oder `docling` (Local OCR). `local` (Local Embeddings) wird abgelehnt – es bietet nur Embeddings. `chatgpt` liest Dokumente, wird hier aber ebenfalls abgelehnt: Die Anmeldung erfolgt über die Einstellungen statt über einen Schlüssel, daher hat die Umgebung nichts, womit sie es vorbelegen könnte. Wird dasselbe SDK wie bei `AI_SDK` genannt, werden dessen Schlüssel und Endpunkt wiederverwendet, und nur das Modell ändert sich. |
| `OCR_API_KEY` | `AI_API_KEY`, wenn die SDKs übereinstimmen | Sein Zugangsschlüssel. Erforderlich für ein OCR-SDK, das sich von `AI_SDK` unterscheidet – außer Local OCR (`docling`), hinter dem kein Konto steht. Dort optional, und nur, wenn Sie den Sidecar mit `DOCLING_SERVE_API_KEY` gestartet haben. |
| `OCR_BASE_URL` | `AI_BASE_URL`, wenn die SDKs übereinstimmen, sonst der eigene Endpunkt des SDKs | Wo sich dieser Anbieter befindet. Für Local OCR (`docling`) ist der Standard der Name des Compose-Dienstes, `http://docling:5001`, sodass `OCR_SDK=docling` allein unter dem Overlay eine vollständige Konfiguration ist. |
| `OCR_MODEL` | `AI_MODEL`, wenn die SDKs übereinstimmen | Sein Modell. Nicht erforderlich für `google_vision` oder Local OCR (`docling`), die ein Dokument ohne Modell lesen; bei Local OCR nennt es optional stattdessen die OCR-Engine. Siehe [Eine Engine wählen](/de/local_ocr#choosing-an-engine). |
| `AI_EMBEDDING_MODEL` | ungesetzt (Deep Research und das Suchfeld gleichen nur Stichwörter ab) | Ein Embedding-Modell – beim `AI_SDK`-Anbieter oder, wenn gesetzt, beim `AI_EMBEDDING_SDK`-Anbieter –, damit Deep Research und das Suchfeld der Dokumente Dokumente auch nach Bedeutung finden können. Unter `AI_SDK=opencode` oder `AI_SDK=anthropic` erfordert es `AI_EMBEDDING_SDK`: Keines von beiden bietet `/embeddings`, daher gibt es keinen Anbieter, auf den zurückgegriffen werden könnte, und die Angabe eines Modells ohne einen solchen wird beim Start abgelehnt. Siehe [was Embeddings kosten](#what-embeddings-cost). |
| `AI_RESEARCH_MODEL` | ungesetzt (Deep Research läuft auf `AI_MODEL`) | Das **Erweiterte Modell**, beim `AI_SDK`-Anbieter: treibt die Denkschleife von Deep Research an, wenige teure Aufrufe pro Frage, während alles andere aus vielen günstigen besteht. Das massenhafte Lesen von Dokumenten bleibt auf `AI_MODEL`. Siehe [Wie Research ein Thema abdeckt](/de/deep_research#how-research-covers-a-topic). |

### Der Embedding-Anbieter {#the-embedding-provider}

Eine frühere Version hatte kein `AI_EMBEDDING_SDK` / `_API_KEY` / `_BASE_URL`, mit der
Begründung, dass Embeddings auf etwas anderes als das Sprachmodell zu richten eine so
seltene Entscheidung sei, dass sie in die **Einstellungen** gehöre, und dass drei weitere
Variablen vor allem drei weitere Möglichkeiten wären, die Funktion halb zu konfigurieren.

Das Embedding-Modell selbst zu betreiben, ist der Fall, den diese Begründung nicht
vorhergesehen hat. Ein Sidecar im Compose-Netzwerk *ist* ein anderer Endpunkt, per
Definition und nicht aus Vorliebe – ohne diese Variablen könnte ein Betreiber eine Instanz
also überhaupt nicht aus `.env` damit starten. Sie sind hier, genau wie der `OCR_*`-Block oben aufgebaut, der schon
immer beschrieben hat, wie ein zweiter Anbieter für eine Aufgabe angegeben wird.

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `AI_EMBEDDING_SDK` | ungesetzt (Embeddings laufen über den `AI_SDK`-Anbieter) | Ein separater Anbieter für Embeddings: `openai`, `openrouter`, `mistral` oder `local` (Local Embeddings). Wird dasselbe SDK wie bei `AI_SDK` genannt, werden dessen Schlüssel und Endpunkt wiederverwendet, und nur das Modell ändert sich. `google_vision`, `docling` (Local OCR), `anthropic`, `opencode` und `chatgpt` werden abgelehnt – keines davon hat einen `/embeddings`-Endpunkt. Erforderlich statt optional, wenn `AI_SDK` eines der letzten drei ist und `AI_EMBEDDING_MODEL` gesetzt ist. |
| `AI_EMBEDDING_API_KEY` | `AI_API_KEY`, wenn die SDKs übereinstimmen | Sein Zugangsschlüssel. Erforderlich für ein SDK, das sich von `AI_SDK` unterscheidet, **außer Local Embeddings (`local`)**, das keinen benötigt. |
| `AI_EMBEDDING_BASE_URL` | der eigene Endpunkt des SDKs oder `AI_BASE_URL`, wenn die SDKs übereinstimmen | Wo sich dieser Anbieter befindet. Für Local Embeddings (`local`) ist der Standard `http://embeddings:80/v1`, der Dienst des Compose-Overlays. |

Ungesetzt ändern alle drei nichts: Embeddings laufen über den `AI_SDK`-Anbieter, genau
wie vor der Einführung des Blocks. `AI_EMBEDDING_SDK` ohne `AI_EMBEDDING_MODEL` zu setzen,
wird abgelehnt statt ignoriert – es würde einen Anbieter anlegen, dem nichts zugewiesen
ist, was wie eine konfigurierte Funktion aussieht, die nie etwas einbettet.

### Der Websuche-Anbieter {#the-web-search-provider}

Deep Research und KI fragen können nur über Dokumente nachdenken, die das Archiv enthält.
Eine Frage, deren Antwort sich weiterentwickelt hat – ein geänderter Tarif, die aktuelle
Adresse eines Unternehmens, eine Zahl, die nie abgelegt wurde –, kommt als *das Archiv
enthält dies nicht* zurück oder, schlimmer, aus dem Gedächtnis des Modells.

Weisen Sie einen Websuche-Anbieter zu, und beide erhalten zwei Werkzeuge, `web_search` und
`web_fetch`. Es gibt ein SDK, **Tavily** (`tavily`): Die Suche liefert gewichtete
Ergebnisse mit jeweils einem Ausschnitt, und der Abruf liefert eine Seite als Markdown. Das
Abrufen geschieht auf Seiten von Tavily, daher ruft hier nichts eine URL auf, die sich ein
Modell ausgedacht hat.

**Sie bleibt ausgeschaltet, bis zwei verschiedene Personen sie anfordern.** Ein Betreiber
weist den Anbieter zu, unter **Einstellungen → KI → Websuche** oder über die Umgebung
unten; ein Leser schaltet dann *Im Web suchen* für eine Unterhaltung ein. Ohne die
Zuweisung wird der Schalter gar nicht angezeigt, und er ist bei jedem Laden der Seite
zunächst aus – jeder Aufruf wird vom Anbieter abgerechnet, daher ist „aus“ die Richtung,
in die man es getrost vergessen kann. Eine Antwort macht höchstens zehn Aufrufe, auf
beiden Oberflächen.

Das Archiv bleibt die primäre Quelle. Der Prompt weist an, zuerst das Archiv zu
durchsuchen und das Web zu nutzen, um das Gefundene zu prüfen oder zu ergänzen, und eine
Antwort zitiert eine Aussage aus dem Web als gewöhnlichen Link, sodass Sie sehen, welche
Sätze von außen stammen.

**Was Ihre Instanz verlässt, ist eine Anfrage, die das Modell geschrieben hat**, nicht die
Frage, die der Leser eingegeben hat – und das Modell hat beim Schreiben Ihren Dokumenttext
vor sich. Gehen Sie davon aus, dass eine Suche eine Formulierung aus einem Dokument nach
außen tragen kann (einen Namen, eine Adresse, eine Rechnungsnummer), nicht nur das Thema
der Frage. Auch das Abrufen läuft auf Seiten des Anbieters, daher sehen die gelesenen
Seiten dessen Adresse statt Ihrer.

Der einfache Modus **Suche** ist nicht betroffen: Er ist eine einzige Runde gegen das
Archiv, die eine Liste von Karten darstellt, und darin gibt es keinen Platz für ein
Web-Ergebnis.

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `WEB_SEARCH_SDK` | ungesetzt (kein Web-Aufruf wird bedient) | Das SDK des Websuche-Anbieters. `tavily` ist der einzige Wert; jedes andere SDK wird abgelehnt, keines davon durchsucht das Web. |
| `WEB_SEARCH_API_KEY` | leer | Sein Zugangsschlüssel, erforderlich, sobald das SDK genannt ist. Es gibt nichts, wovon er übernommen werden könnte: Ein Websuche-Anbieter ist immer ein eigener Endpunkt. |
| `WEB_SEARCH_BASE_URL` | der eigene Endpunkt des SDKs | Wo sich dieser Anbieter befindet, für ein vorgeschaltetes Gateway. Standard ist `https://api.tavily.com`. |

Ungesetzt ändern alle drei nichts: Keinem Modell wird ein Werkzeug angeboten, und beide
Oberflächen verhalten sich genau wie vor der Einführung des Blocks. Einen Schlüssel oder
eine Basis-URL ohne `WEB_SEARCH_SDK` zu setzen, wird abgelehnt statt ignoriert, genauso wie
beim `OCR_*`-Block – es gibt keinen anderen Anbieter, in den sie einfließen könnten.

### Vorbelegte Einstellungen {#seeded-settings}

Werden beim ersten Start in `app_settings` geschrieben und danach über die
**Einstellungen** bearbeitet.

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `OCR_TIMEOUT_SEC` | `90` | Timeout für OCR-Anfragen. Zu niedrig für [Lokale OCR](/de/local_ocr), die pro Seite Sekunden bis zig Sekunden braucht |
| `AI_TIMEOUT_SEC` | `90` | Timeout für Sprachmodell-Anfragen: Extraktion, KI fragen, Suche, Recherche und Teilungserkennung |
| `WORKER_TIMEOUT_SEC` | `300` | Verarbeitungs-Timeout pro Auftrag |
| `WORKER_MAX_RETRIES` | `0` | Maximale Anzahl von Wiederholungsversuchen pro Schritt, bevor ein Auftrag fehlschlägt |
| `DEEP_SEARCH_LANGUAGES` | leer | Kommagetrennte ISO-639-1-Codes (z. B. `de,en,uk`) für die Stichworterweiterung auf beiden Suchseiten. Steuert sprachspezifische Suchen nur, wenn kein Embedding-Modell gesetzt ist; mit einem solchen überschreitet bereits eine einzige Suche Sprachgrenzen |
| `EXTRACTION_PROMPT_VERSION` | `v1` | Wird bei jedem Schrittlauf eines Verarbeitungsauftrags gespeichert, mit einem angehängten Digest der Extraktionsregeln aus den Einstellungen, sofern welche gesetzt sind; dient nur der Buchführung, wird in der Einstellungsoberfläche nicht angeboten |
| `NEAR_DUPLICATE_DETECTION_ENABLED` | `false` | Ob die Pipeline eine Erkennung von Beinahe-Duplikaten ausführt |
| `NEAR_DUPLICATE_THRESHOLD` | `0.92` | Wie ähnlich sich die Texte zweier Dokumente sein müssen, damit sie als Beinahe-Duplikate gelten |

Eine weitere wird bei jedem Start aus der Umgebung gelesen und nie gespeichert, weil sie
das Ausgabentempo steuert, statt die Instanz zu beschreiben:

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `EMBEDDING_BACKFILL_BATCH` | `20` | Anzahl der Dokumente, die ein Backfill-Durchlauf einbettet, gemäß `WORKER_CRON_EXPR`. `0` deaktiviert den geplanten Backfill, sodass nur neu verarbeitete Dokumente eingebettet werden und ein bestehendes Archiv unangetastet bleibt – **Wartung → Embeddings** bettet es weiterhin auf Anforderung ein. |

Die **Ergebnissprache** hat überhaupt keine Variable. Sie bestimmt, in welcher Sprache Titel,
Zusammenfassung, Typ und Korrespondent eines Dokuments gespeichert werden, und das ist eine
Vorliebe des Lesers, nicht des Betreibers; daher wird sie in den **Einstellungen** festgelegt.
Tags sind ausgenommen: Sie werden aus einer Liste
zugewiesen, die der Benutzer schreibt, und sind daher bereits in der Sprache, die dieser
Benutzer gewählt hat.

## Modelle in den Einstellungen zuweisen {#binding-models-in-settings}

1. Melden Sie sich mit dem Administratorkonto an und öffnen Sie die **Einstellungen**
   (sichtbar, wenn `/api/app/me` `is_admin` meldet).
2. Fügen Sie einen Anbieter hinzu – SDK, API-Schlüssel, optionale Basis-URL und den
   **Modellkatalog**, aus dem seine Kontextfenster gelesen werden (siehe unten).
3. Weisen Sie unter **Modelle** der **OCR** und der **Allgemeinen KI** einen Anbieter und ein
   Modell zu; Letztere erledigt alles andere: Extraktion, KI fragen, KI-gestützte Suche und das
   Lesen von Dokumenten durch Deep Research. Das **Erweiterte Modell** ist optional und treibt
   die Denkschleife von Deep Research an; bleibt es leer, übernimmt das ebenfalls die
   Allgemeine KI. **Embeddings**, **Websuche** und **Deep-Search-Sprachen** finden Sie
   ebenfalls hier.

### Der Modellkatalog {#the-model-catalogue}

Deep Research gibt eine Unterhaltung vollständig wieder und überlässt dem Anbieter, was
hineinpasst: Auf dem Weg nach außen wird nichts gekürzt, und eine Anfrage, die über das
Modell hinauswächst, kommt als eigener Fehler des Anbieters zurück. Damit ein Lauf im
Verhältnis zu seinem Limit beobachtet werden kann, meldet jeder Recherche-Turn die Tokens,
die seine umfangreichste Anfrage verwendet hat – live während des Laufs und gespeichert mit
der Antwort.

Der Nenner stammt aus dem im Anbieter-Eintrag gewählten Katalog, der einmal täglich aus der
Modellliste von [pi.dev](https://pi.dev) gelesen wird. Er wird anhand des SDKs vorbelegt
und sollte korrigiert werden, wenn das `openai`-SDK auf etwas zeigt, das nicht OpenAI ist:
Eine Basis-URL `https://api.groq.com/openai/v1` braucht den Katalog **groq**.

Lassen Sie ihn auf **Keiner**, und es wird nichts abgerufen: Die Recherche läuft genau wie
zuvor, und die Nutzungszeile zeigt eine Token-Anzahl ohne Limit daneben. Diese Auswahl ist
der Ausschalter. `AI_MODEL_CATALOG_URL` verlegt nur die Abfrage an einen anderen Ort als
pi.dev; leer oder ungesetzt fällt sie auf diesen Standard zurück. Wenn ein Anbieter keine
eigene Nutzung meldet, wird die Anzahl aus dem gesendeten Text geschätzt und mit einem `~`
gekennzeichnet.

### Prompt-Caching {#prompt-caching}

Dass die Unterhaltung vollständig wiedergegeben wird, macht sie cachebar: Der unveränderte
Teil ist von einem Turn zum nächsten byte-identisch, sodass ein Anbieter die bereits daran
geleistete Arbeit wiederverwenden kann, statt alles erneut zu lesen. Lemmary fordert das an,
wo der Anbieter danach gefragt werden muss – ein Cache-Schlüssel bei OpenAI, ein
`cache_control`-Breakpoint bei OpenRouter, bei Anthropic und bei den OpenCode-Modellen, die
über die Messages API bedient werden, der Header `x-opencode-session` beim Rest von
OpenCode.
Von den anderen wird nichts verlangt: Ein unbekanntes Feld ist eine abgelehnte Anfrage, keine
verpasste Ersparnis.

Ein Recherche-Turn deklariert bei jedem Aufruf jedes Werkzeugschema, unabhängig davon, was
dahintersteht – die Web-Werkzeuge bei ausgeschaltetem Schalter, `survey_documents` ohne
zugewiesenes Sprachmodell, `count_documents` in jedem Fall – und lehnt den Aufruf ab, wenn
nichts da ist, das ihn bedienen kann. Die Werkzeugliste ist Teil dessen, was gecacht wurde;
eine Liste, die dem Schalter folgte oder sich in dem Moment änderte, in dem ein Administrator
einen Anbieter zuweist, würde das gesamte Transkript verwerfen; ein paar hundert Tokens
Schema bei jedem Aufruf sind die günstigere Seite dieses Tauschs. KI fragen ist nicht
betroffen: Es hat keinen gespeicherten Verlauf, der verloren gehen könnte, und bietet die
Web-Werkzeuge weiterhin nur an, wenn sie funktionieren.

Ein Chat, der vor dieser Änderung geöffnet wurde, behält den System-Prompt, mit dem er
geöffnet wurde und der das Web nicht erwähnt. Das kostet ihn ein erneutes Lesen und danach
nichts mehr; den gespeicherten Prompt umzuschreiben, würde genau das Präfix verschieben, um
das es hier geht.

Was weiterhin ein vollständiges erneutes Lesen kostet, ist eine Leerlaufpause, die länger ist
als die Cache-Lebensdauer des Anbieters – fünf Minuten beim Standard von Anthropic. Sie zeigt
sich als `cached_tokens=0` im Completion-Log.

Änderungen laden die prozessinternen Clients im laufenden Betrieb neu – kein Neustart. Die
OCR-Auswahl listet nur Modelle, die Dateien verarbeiten können, sofern der Anbieter angibt,
welche das sind (OpenRouters `file`-Eingabe, Mistrals `ocr`-Fähigkeit); andere SDKs zeigen den
vollständigen Katalog mit einem Hinweis. Über das Feld **Eigene Modell-ID** lässt sich jede
Liste umgehen.

## Ein Modell für einen Chat oder einen Auftrag überschreiben {#overriding-a-model-for-one-chat-or-one-job}

Die obigen Zuweisungen sind die Standardwerte. Ein einzelner Chat oder ein einzelner
Auftrag zur erneuten Verarbeitung kann mit einem anderen Anbieter und Modell laufen, ohne sie
anzutasten – nützlich, um ein hartnäckiges Dokument mit einem stärkeren Extraktor erneut zu
versuchen oder einem günstigen Modell eine einfache Frage zu stellen. Jeder angemeldete
Benutzer kann das; die Auswahl beschränkt sich auf die Anbieter, die ein Administrator
bereits konfiguriert hat, sodass damit die Zugangsdaten des Betreibers genutzt, aber nie neue
hinzugefügt werden können.

- **KI fragen**, **KI-gestützte Suche** und **Deep Research** bieten unter dem Eingabefeld
  *Anderes Modell verwenden* an, wo auch das verwendete Modell genannt wird, wenn nichts
  überschrieben ist: das Modell für Allgemeine KI bei den ersten beiden, das Erweiterte Modell
  bei der Recherche. Die Auswahl ist für die Unterhaltung festgelegt, wie die Seite, auf der
  sie begonnen wurde: Das in jedem Turn wiedergegebene Transkript wurde von einem Modell
  erzeugt, und wenn ein anderes die nächste Frage beantwortet, liest es diese Arbeit so, als
  wäre es seine eigene. Beginnen Sie einen neuen Chat, um zu wechseln. Das massenhafte Lesen
  von Dokumenten durch Deep Research wird dadurch nicht verlagert – es bleibt auf dem Modell
  für Allgemeine KI, weil diese Arbeit aus vielen günstigen Aufrufen pro Dokument besteht,
  während die Rechercheschleife aus wenigen teuren besteht.

  **Eine Unterhaltung speichert das Modell, mit dem sie eröffnet wurde, unabhängig davon, ob
  es jemand ausgewählt hat.** Eine Änderung einer Zuweisung in den Einstellungen gilt daher für
  neue Chats und lässt bestehende, wo sie sind, statt jedes offene Transkript auf ein Modell zu
  verlagern, das es nicht geschrieben hat. Unterhaltungen aus der Zeit davor haben nichts
  gespeichert und folgen weiterhin den Einstellungen. Wird der Anbieter, an den eine
  Unterhaltung gebunden ist, später gelöscht, funktioniert der Chat weiter: Der nächste Turn
  fällt auf die Zuweisung in den Einstellungen zurück, sodass das Austauschen eines Anbieters
  offene Chats nicht blockiert.
- **Erneut verarbeiten** – auf der eigenen Seite eines Dokuments, in der Sammelleiste der
  Dokumentliste und unter **Wartung → Fehlgeschlagene Verarbeitung** – bietet OCR, Extraktion
  und Embedding an. Die Auswahl wird bei jedem eingereihten Auftrag gespeichert, sodass ein
  Stapel, der für einen anderen Extraktor eingereiht wurde, auch mit diesem läuft, wie lange
  die Warteschlange auch dauert, statt mit dem, was in den Einstellungen steht, wenn der Worker
  so weit ist. Der Schrittverlauf (**Verarbeitungsauftrag → Schrittläufe**) zeichnet den
  Anbieter und das Modell auf, die tatsächlich gelaufen sind.
- Bleibt eine Auswahl unberührt, sendet sie nichts, und der Auftrag oder Chat läuft genau wie
  zuvor – dieselbe Anfrage, Byte für Byte.

**Die Embedding-Überschreibung muss das Modell nennen, das in den Einstellungen bereits
zugewiesen ist.** Ein Chunk-Datensatz speichert das Modell und die Dimensionszahl, mit denen er
erzeugt wurde, und der Suchindex liest nur die Datensätze, die zum konfigurierten Modell
passen; Vektoren aus einem anderen Modell würden also bezahlt, geschrieben und nie gelesen – ein
Dokument fiele stillschweigend aus der dichten Suche heraus. Der Server lehnt das ab, statt es
wie einen Erfolg aussehen zu lassen. Das erneute Einbetten mit dem *konfigurierten* Modell ist
der sinnvolle Fall: eine Korrektur für ein Dokument, dessen Vektoren fehlen oder veraltet sind.
Um das Modell selbst zu ändern, ändern Sie es in den Einstellungen, wodurch alles neu
eingebettet wird (siehe [Was Embeddings kosten](#what-embeddings-cost)).

## Was Embeddings kosten {#what-embeddings-cost}

`AI_EMBEDDING_MODEL` einzuschalten, ist eine Verpflichtung, das gesamte Archiv einzubetten,
nicht nur den nächsten Upload; es lohnt sich also, die Struktur der Rechnung zu kennen, bevor
Sie sich darauf einlassen.

- **Tokens.** Der OCR-Text jedes Dokuments wird in Passagen von ~1100 Zeichen zerlegt, und
  jede Passage ist eine Embedding-Eingabe – ungefähr eine Anfrage pro 30 KB Text.
  Die Auffüllung, die OCR in Tabellen einfügt (Folgen von Leerzeichen, lange
  `----`-Linien), wird weder gezählt noch gesendet.
  Embedding-Modelle sind pro Token günstig; es handelt sich einfach um jedes Dokument, das
  Sie haben.
- **Erneutes Einbetten.** Nur der OCR-Text wird eingebettet, daher kann nur der OCR-Text die
  Vektoren eines Dokuments veralten lassen: eine erneute OCR, eine korrigierte Seite, eine
  erneute Verarbeitung. Einen Titel zu bearbeiten, ein Dokument neu zu taggen oder einen Tag
  im gesamten Archiv umzubenennen, kostet nichts. *Jedes* Dokument wird erneut eingebettet,
  wenn Sie das Modell wechseln, weil sich Vektoren aus zwei Modellen nicht vergleichen lassen –
  eine teilweise Migration gibt es nicht. Dasselbe geschieht einmalig nach einem Update, das
  ändert, wie Passagen geschnitten werden.
- **Platz, der unter Verschlüsselung Arbeitsspeicher ist.** Ein Vektor mit 1536 Dimensionen
  ist etwa 6 KB groß; ein typisches Dokument besteht aus einer Handvoll Passagen, also 30–60 KB
  pro Dokument in `data.db`. Mit `VAULT_ENABLED=1` wird das Archiv in ein tmpfs entschlüsselt,
  dieser Platz ist also Arbeitsspeicher. Ein Modell mit 1024 oder weniger Dimensionen
  verbraucht entsprechend weniger davon. Siehe [Verschlüsselung im Ruhezustand](/de/encryption).
- **Suchen.** Jede Abfrage im Suchfeld der Dokumente und jede Seite ihrer Ergebnisse bettet den
  Abfragetext ein: jeweils eine kurze Anfrage, wenige Tokens.

Das Modell selbst zu betreiben, verlagert die ersten beiden Kosten, statt sie zu beseitigen:
Es gibt keine Token-Rechnung und überhaupt keinen Preis pro Dokument, aber dieselbe Arbeit
findet auf Ihrer CPU statt, und der erste Backfill eines großen Archivs ist der einzige
Zeitpunkt, an dem das spürbar langsam ist. Die dritten Kosten – Platz und unter einem Tresor
Arbeitsspeicher – bleiben unverändert und hängen nur von der gewählten Dimensionszahl ab. Siehe
[Lokale Embeddings](/de/local_embeddings).

Der Backfill arbeitet pro Durchlauf `EMBEDDING_BACKFILL_BATCH` Dokumente ab und protokolliert,
was er eingebettet hat, was fehlgeschlagen ist und wie viele noch übrig sind; **Einstellungen →
Modelle** zeigt dieselben Zahlen, und **Wartung → Embeddings** zeigt sie ebenfalls an und
arbeitet den gesamten Rückstand auf Anforderung ab, statt minütlich auf einen Durchlauf zu
warten. Ein Anbieterfehler ist ein weicher Fehler: Das Dokument behält seinen Text, seine
Metadaten und seinen Platz in der Stichwortsuche und wird später mit einem Backoff erneut
versucht.

Das Modell auf Ihrer eigenen Hardware zu betreiben, hat eine eigene Seite: Siehe
[Lokale Embeddings](/de/local_embeddings) zu den Compose-Overlays, zur Modellwahl und dazu, was
es an Arbeitsspeicher auf dem Host kostet.

## OCR, nach Anbieter {#ocr-per-provider}

Weisen Sie unter **Einstellungen → Modelle** einen OCR-fähigen Anbieter zu. Die
Textextraktion für TXT, CSV, DOCX und XLSX verwendet native Parser und ruft überhaupt keine
OCR-API auf.

### Mistral Document OCR {#mistral-document-ocr}

Verwendet die [Mistral Document OCR API](https://docs.mistral.ai/en/studio-api/document-processing/basic_ocr),
wenn der Anbieter für OCR zugewiesen ist – nicht den Chat-Endpunkt, den derselbe Anbieter
gleichzeitig für Extraktion, Chat und Suche bedienen kann. Lokale Dateien werden als
Base64-Daten-URLs gesendet, bis zu den von Mistral dokumentierten 50 MB; die Obergrenze von
47 MB für `documents.file` hält jeden Upload darunter.

- **PDFs und Office-Dokumente** – `document_url` mit einer Base64-Daten-URL
- **Bilder** – `image_url` mit einer Base64-Daten-URL
- **Ausgabe** – Markdown der Seiten, zu reinem Text zusammengefügt

Mistral ist außerdem der einzige Anbieter, der ein Seitenlimit dokumentiert – 1000 Seiten,
und daher stammt [die Seitenobergrenze](/de/setup#the-page-ceiling).

### OpenAI- / OpenRouter-Modelle {#openai-openrouter-models}

Jedes Modell, das Dateien oder Bilder annimmt, kann OCR übernehmen: Das Dokument wird an den
Chat-Endpunkt gesendet, und der Text kommt als Completion zurück. OpenRouter listet nur Modelle
auf, die `file`-Eingabe angeben; der Katalog von OpenAI sagt nichts dazu, wählen Sie also
selbst ein Modell, das Dateien verarbeiten kann.

### Anthropic-Modelle {#anthropic-models}

Dasselbe Prinzip wie bei OpenAI: Das Dokument geht an das Modell, und der Text kommt als
Antwort zurück. Bilder kommen als Image-Blöcke und PDFs als Document-Blöcke bei
`/v1/messages` an; alles andere wird mit einer Meldung abgelehnt, die das Format nennt, da
Claude keine anderen Dateitypen liest. Jedes aktuelle Claude-Modell akzeptiert beides, daher
ist die Modellauswahl nicht gefiltert.

### Google Cloud Vision {#google-cloud-vision}

Verwendet die offizielle [Go-Client-Bibliothek](https://docs.cloud.google.com/vision/docs/detect-labels-image-client-libraries).

- **Bilder** – `BatchAnnotateImages` mit `DOCUMENT_TEXT_DETECTION` über `images:annotate`
- **PDFs** – `BatchAnnotateFiles` über `files:annotate` (Base64-Upload, kein Cloud
  Storage). Seiten werden in Stapeln von bis zu 5 pro Anfrage verarbeitet, egal wie viele die
  Datei hat.

Wie Sie einen Schlüssel erhalten, lesen Sie unter [Google-Vision-API-Schlüssel](/de/google_vision).

### Lokale OCR {#local-ocr}

Ein Container neben der App statt einer API. Er spricht `POST /v1/convert/file` von
docling-serve und erhält Markdown zurück; er liest PDFs, Bilder und Office-Dokumente. Er
kommt ohne Schlüssel aus – die Adresse ist die gesamte Konfiguration – und veröffentlicht
keinen Port, sodass nur die App ihn erreichen kann.

Seine Standard-Erkennung ist RapidOCR, das sind die PP-OCR-Modelle von PaddleOCR als ONNX,
und die Zuweisung des OCR-Modells schaltet auf EasyOCR oder Tesseract um. Deshalb gibt es
kein zweites lokales SDK: Die Erkennung von PaddleOCR ist bereits hier.

Starten Sie ihn mit dem Overlay `docker-compose.local-ocr.yml` und erhöhen Sie
`OCR_TIMEOUT_SEC` – das ist die Einstellung, die gern übersehen wird. Alles andere – die
Image-Größe, Arbeitsspeicher, GPU-Varianten und die Kosten pro Seite – steht unter
[Lokale OCR](/de/local_ocr).

## Fehlerbehebung {#troubleshooting}

- **Der Einrichtungsassistent kommt nicht weiter** – fügen Sie einen OCR-Anbieter und einen
  Sprachmodell-Anbieter hinzu (ein einziger Mistral-Anbieter ist beides), oder setzen Sie
  `AI_API_KEY` in `.env` vor dem ersten Start. Werden erforderliche Schlüssel später gelöscht,
  erscheinen die Konfigurationsschritte wieder.
- **OCR schlägt fehl** – prüfen Sie den Anbieter und den Schlüssel in den Einstellungen sowie
  den Fehler des Verarbeitungsauftrags auf der Detailseite des Dokuments. Stellen Sie bei
  Google Vision sicher, dass die Vision API für das Projekt aktiviert ist.
- **Lokale OCR läuft in ein Timeout** – erhöhen Sie `OCR_TIMEOUT_SEC`. Auf einer Instanz, die
  bereits gestartet wurde, muss der Wert in den **Einstellungen** erhöht werden, nicht in
  `.env`: Es ist eine vorbelegte Einstellung, daher wirkt die Umgebung nur beim ersten Start.
  Siehe [Lokale OCR](/de/local_ocr#what-it-costs).
- **KI-Extraktion schlägt fehl** – prüfen Sie, ob ein Extraktionsmodell zugewiesen ist und ob
  es ein Chat-Modell ist, kein Embedding- oder OCR-Modell.
- **Ein `opencode`-Modell beantwortet jede Anfrage mit 500 oder 404** – prüfen Sie, ob das SDK
  wirklich `opencode` ist und nicht `openai` mit einem darauf gerichteten `AI_BASE_URL`.
  Opencode Go bedient jedes Modell auf einem von drei Endpunkten, und welcher es ist, ist eine
  Eigenschaft des Modells, die sein Katalog nicht meldet; daher führt das `opencode`-SDK die
  Tabelle. Bei jedem anderen SDK geht jedes Modell an `/chat/completions`, und zwei Drittel
  des Katalogs sind dort nicht vorhanden.

  Eine Installation, die älter als das SDK ist, muss nicht angepasst werden: `AI_SDK=openai`
  mit einer Basis-URL, die `opencode.ai` adressiert, wird als `AI_SDK=opencode` *gelesen*, und
  der Anbieter-Eintrag, den sie vorbelegt hat, wird durch die Migration `1730000026` auf dieses
  SDK umgestellt. Das betrifft also nur einen Eintrag, der danach von Hand angelegt wurde, oder einen, der über eine URL auf
  Opencode zeigt, die den Host verbirgt. Dasselbe gilt für den Header `x-opencode-session`, den
  Opencode verlangt und den nur das `opencode`-SDK sendet.

  Das Routing zur Referenz. Ein Modell, das Lemmary nicht kennt, geht an
  `/chat/completions`, den Endpunkt, dessen Ablehnung das Problem benennt:

  | Endpunkt | Modelle |
  | --- | --- |
  | `/chat/completions` | `glm-*`, `kimi-*`, `longcat-*`, `deepseek-*`, `mimo-*`, `hy*`, `omen-*` |
  | `/responses` | `grok-*`, `gpt-*`, `muse-spark-*` |
  | `/messages` (Anthropics API) | `minimax-*`, `qwen*` |

- **Ein Modell der `gpt-5`-Familie lehnt eine Deep-Research-Anfrage ab** – diese Modelle setzen
  `reasoning_effort` selbst und lehnen die Anfrage dann ab, weil Funktionswerkzeuge vorhanden
  sind. Lemmary behandelt das ohne Konfiguration: Die Anfrage wird in die *Responses*-API
  übersetzt, die sowohl die Werkzeuge als auch das Reasoning beibehält, und fällt nur dann auf
  `reasoning_effort=none` zurück, wenn auch dieser Endpunkt sie nicht bedienen kann. Die
  Log-Zeile lautet `model rejected reasoning_effort with function tools; retrying on the
  Responses API`. Das Gelernte wird pro Anbieter gespeichert, daher wird derselbe Modellname
  hinter zwei Anbietern getrennt ermittelt.
- **Local Embeddings bettet nichts ein** – `docker compose logs embeddings`. Beim ersten Start
  lädt er Gewichte herunter, und der Container ist bis zum Abschluss unhealthy, weshalb die App
  auf seinen Healthcheck wartet; Embedding-Schritte schlagen währenddessen weich fehl und werden
  erneut versucht, sodass kein Dokument verloren geht. Ein `413` im Log der App bedeutet, dass
  die Batch-Limits des Endpunkts unter dem liegen, was Lemmary sendet – siehe die Flags oben.
- **Die Auswahl der Embedding-Modelle ist bei Local Embeddings leer** – der Katalog stammt aus
  dem `/info` des Sidecars, das auch meldet, welche Art von Modell es ist. Ein Reranker oder
  ein Klassifikator wird bewusst nicht angeboten: Als Embedding-Modell zugewiesen, würde er bei
  jedem Dokument fehlschlagen.
