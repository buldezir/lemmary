# Deep Research {#deep-research}

Deep Research beantwortet Fragen zum Archiv, statt Ihnen Dokumente zum Lesen
aufzulisten. Fragen Sie etwas, das ein Ordner nicht beantworten kann – *wie viel
habe ich 2024 für das Auto ausgegeben*, *was hat die Versicherung zum Wasserschaden
geschrieben*, *wie viele Rechnungen sind noch offen* –, und es sucht, liest, was es
findet, und schreibt eine Antwort, die jedes verwendete Dokument als Quelle angibt.
Suchen, Lesevorgänge und Sichtungen erscheinen, während sie stattfinden, sodass man
einem langen Lauf zusehen kann, statt auf einen Ladekreisel zu starren.

Es befindet sich in der Kopfzeile, unter `/rag/research`. Sein Geschwisterwerkzeug,
die **KI-gestützte Suche** in der Dokumentliste, ist die Variante mit einer
einzigen Runde: Sie findet Dokumente und zeigt sie als Karten an, und sie ist das
bessere Werkzeug, wenn Sie wissen, wonach Sie suchen, und es nur gefunden haben
möchten.

Gut zu fragen heißt vor allem, zu sagen, was Sie zurückbekommen möchten. Nennen
Sie den Zeitraum, den Korrespondenten oder den Tag, falls Sie einen haben; sagen
Sie, ob Sie eine Zahl, ein Datum, eine Liste oder eine Erklärung möchten; und
stellen Sie eine Nachfrage im selben Chat, statt neu anzufangen, denn ein Chat
behält, was frühere Runden gelesen haben, und baut darauf auf. Chats werden
gespeichert, und jeder hat seine eigene URL.

## Die beiden Seiten {#the-two-pages}

Ein einziger Tool-Calling-Agent über dem Bleve-Volltextindex bedient zwei Seiten, je eine pro Pfad unter `/rag`, mit derselben Modellzuordnung.

- **KI-gestützte Suche** (`/rag/search`) befindet sich in der Kopfzeile der Dokumentliste – eine Runde `search_documents`, beantwortet aus Titeln, Zusammenfassungen und kurzen OCR-Ausschnitten. Die Ergebnisse werden als Dokumentkarten angezeigt.
- **Deep Research** (`/rag/research`) ist der Eintrag in der Kopfzeile – der Agent sucht, liest die gefundenen Dokumente (`read_documents`), sichtet viele auf einmal, wenn die Frage ein ganzes Thema umfasst (`survey_documents`), zählt, wenn nach einer Anzahl gefragt wird (`count_documents`), und schreibt eine Markdown-Antwort, die jedes verwendete Dokument als Quelle angibt, wobei die herangezogenen Dokumente unter der Antwort aufgeführt sind. Der Fortschritt wird über `POST /api/app/search/stream` (Server-Sent Events) übertragen, sodass jede Suche, jeder Lesevorgang, jede Sichtung und jede Zählung erscheint, während sie stattfindet.

Deep Research hat kein Runden- oder Dokumentlimit. Es sucht und liest weiter, bis es antworten kann, bis das Modell keine Fortschritte mehr macht oder bis eine Completion abgelehnt wird, weil die Unterhaltung das Kontextfenster des Modells überschritten hat. Ohne einen Anbieter für Sprachmodelle liefern beide Seiten einen Konfigurationsfehler – siehe [KI-Anbieter und Modelle](/de/ai_providers).

## Wie es Dokumente findet {#how-it-finds-documents}

Beide Seiten greifen über dasselbe Tool `search_documents` auf das Archiv zu, und
es führt für jede Anfrage bis zu zwei Suchen aus.

- **Schlüsselwörter (BM25)** über den Dokumentindex, [in zwei Stufen
  gelockert](/de/setup#full-text-search) statt des strikten UND, das die Seite
  Dokumente beibehält: Dort ist die Anfrage ein Filter, den Sie eingegeben haben,
  hier ist sie eine Vermutung, die das Modell aus einer Frage abgeleitet hat.
- **Bedeutung (kNN)** über `bleve/chunks`, einen zweiten Index, der einen Eintrag
  pro eingebettetem Textabschnitt samt Vektor enthält und nach Kosinus-Ähnlichkeit
  zur eingebetteten Frage durchsucht wird. Diese Hälfte gibt es nur, wenn
  `AI_EMBEDDING_MODEL` gesetzt ist und Dokumente tatsächlich eingebettet wurden.
  Das Modell kann ein gehostetes sein oder [eines, das Sie selbst
  betreiben](/de/local_embeddings).

Die beiden Listen werden per Reciprocal Rank Fusion zusammengeführt: Ein Dokument
erhält als Punktzahl die Summe von `1/(60 + rank)` über die Listen, in denen es
vorkommt. Gelesen werden nur die Positionen, nie die Punktzahlen – BM25 und
Kosinus liegen nicht auf derselben Skala, und das eine gegen das andere zu
normalisieren wäre Raterei. Ein Dokument, das von einem der beiden Signale
gefunden wird, bleibt erhalten; eines, das von beiden gefunden wird, steigt auf.

Die Textabschnitte, die dem Modell gezeigt werden, stammen aus demselben
Chunk-Index: eine Schlüsselwortsuche über die Abschnitte genau der Dokumente, die
zurückgegeben werden, eingegrenzt auf den Satz um den Treffer. Ohne Chunk-Index
werden sie stattdessen aus dem OCR-Text rund um die Suchbegriffe geschnitten und,
falls das nicht gelingt, aus dem Highlight des Index selbst – das Modell erhält
immer wörtlichen Text, egal wie die Suche erfolgte. Das Lesen eines langen
Dokuments ist immer ein Auszug: sein Anfang plus die Abschnitte, die auf dieselbe
Weise gegen den `focus` des Lesevorgangs gerankt werden, oder gegen die Frage des
Nutzers, wenn das Modell keinen angegeben hat. Der vollständige Text eines langen
Dokuments wird dem Recherchemodell nie übergeben.

Ist ein Embedding-Modell gesetzt, teilt der Prompt dem Modell mit, dass eine Suche
bereits sprachübergreifend funktioniert und es eine Suche nicht in eine andere
Sprache übersetzt wiederholen soll; die Liste `DEEP_SEARCH_LANGUAGES` ist dann nur
noch ein Hinweis für die Schreibweise exakter Begriffe. Ohne ein solches Modell
wird das Modell gebeten, einmal pro konfigurierter Sprache zu suchen, da die
Übersetzung das Einzige ist, was eine englische Frage zu einer deutschen Rechnung
bringt.

Filter – ein Tag, ein Typ, ein Korrespondent, ein Datumsbereich – sind
Eigenschaften eines Dokuments, und der Chunk-Index enthält bewusst keine davon,
damit das Umbenennen eines Tags nie einen Vektor neu schreibt. Sie werden
stattdessen gegen den Dokumentindex aufgelöst und als Liste von Dokument-IDs auf
die Abschnittssuche angewendet.

Was es kostet: eine Embedding-Anfrage pro unterschiedlichem Suchbegriff pro Runde
(das Ergebnis wird für den Rest dieser Runde wiederverwendet, einschließlich eines
fokussierten Lesevorgangs mit denselben Wörtern) sowie eine oder zwei zusätzliche
Indexsuchen. Alles auf dem Dense-Pfad verschlechtert sich, statt fehlzuschlagen:
kein Modell zugeordnet, ein fehlgeschlagener Embedding-Aufruf, ein Index, der
noch neu aufgebaut wird – die Suche ist dann die Schlüsselwortsuche, die sie
schon immer war. Jeder Aufruf protokolliert eine Zeile, `deep search retrieval
lexical=… dense=… fused=… embedded=…`; dort sollten Sie nachsehen, wenn eine
Antwort ein Dokument übersehen zu haben scheint.

## Wie Research ein Thema abdeckt {#how-research-covers-a-topic}

Dokumente einzeln Aufruf für Aufruf zu lesen ist richtig für eine Frage nach der
Nadel im Heuhaufen und falsch für ein Thema: Zweihundert Dokumente, die in eine
Unterhaltung eingelesen werden, sind zweihundert Dokumente, die in jeder späteren
Runde erneut gesendet werden. Drei Dinge halten eine breite Frage bezahlbar.

- **Destillierte Lesevorgänge.** Wenn ein `read_documents`-Aufruf mehr als etwa
  32 KB Text oder mehr als fünf Dokumente in die Unterhaltung bringen würde,
  werden die Dokumente stattdessen vom Modell **Allgemeine KI** gelesen. Das
  Recherchemodell erhält pro Dokument Notizen dazu, was es über den Fokus aussagt,
  wörtliche Zitate zum Belegen und alle angeforderten Werte – nie den Text.
  Kleinere Lesevorgänge werden als Auszüge durchgereicht, denn bei einer Frage
  nach der Nadel im Heuhaufen kommt es gerade auf den genauen Wortlaut an. Dem
  Lesemodell wird jedes Dokument vollständig gezeigt, bis zu 400 KB, und mehrere
  kurze Dokumente teilen sich einen Aufruf.
- **`survey_documents`.** Für „alles über X“ oder „die Summe von Y über das Jahr“:
  dieselbe Suche wie bei einer normalen Suche, standardmäßig auf 300 Dokumente
  begrenzt und höchstens 1000, wobei jedes Dokument vom Hilfsmodell für eine
  einzige Frage gelesen wird und pro Dokument eine kompakte Zeile zurückkommt.
  Zahlenfelder (`fields: [{name, type: "number"}]`) werden auf dem Server
  summiert, gemittelt und mit Minimum und Maximum versehen, je Währung, und das
  Modell wird angewiesen, diese Zahlen zu berichten, statt die Zeilen selbst zu
  addieren. Der Fortschritt wird als „120 von 300 Dokumenten gesichtet“
  übertragen.
- **`count_documents`.** Für „wie viele“ und „wie verteilen sie sich“: Reine
  Filter beantwortet die Datenbank (`COUNT(*)`, optional `GROUP BY` Typ,
  Korrespondent, Jahr, Monat oder Tag); mit Suchtext meldet der Index die exakte
  Gesamtzahl strikter Treffer, und eine gruppierte Aufschlüsselung bringt die IDs
  des Index zur Datenbank (ab 5000 Treffern als ungefähr gekennzeichnet). Ein
  Filter, der einen nicht existierenden Typ, Korrespondenten oder Tag nennt, zählt
  null und sagt, welcher Name nicht aufgelöst werden konnte, statt auf alles zu
  passen.

Die Denkschleife selbst läuft auf dem **Erweiterten Modell** (`AI_RESEARCH_MODEL`
oder **Erweitertes Modell** in den Einstellungen), während die umfangreichen
Lesevorgänge oben bei **Allgemeine KI** bleiben, weil diese Arbeit aus vielen
günstigen Aufrufen besteht, die Schleife dagegen aus wenigen teuren; ist kein
Erweitertes Modell zugeordnet, läuft die gesamte Recherche auf Allgemeine KI zu
deren Preis. Ein Allgemeine-KI-Modell, das den JSON-Modus ablehnt, wird erneut im
Klartext angefragt und tolerant geparst. Jede Completion protokolliert ihren
Tokenverbrauch
(`ai completion usage prompt_tokens=… cached_tokens=… completion_tokens=…`), und
ein Recherchelauf protokolliert seine Gesamtsumme; dort sollten Sie nachsehen,
wenn Sie prüfen möchten, was eine Frage gekostet hat.

Modelle, die Tool-Aufrufe in ihrem Inhalt statt nativ ausgeben (der DSML-Pfad),
werden angewiesen, nach einer Runde Tool-Ergebnisse zu antworten, sodass sie
keine Zählung an eine Sichtung und diese an einen Lesevorgang ketten können; sie
erhalten eine Tool-Runde und dann die Antwort.

## Pfade, Chats und Einstellungen {#paths-chats-and-settings}

Jede Seite hat ihren eigenen Pfad – `/rag/search` und `/rag/research` –, sodass die Zugehörigkeit eines Chats in der URL steckt und ein Neuladen, die Zurück-Schaltfläche, ein Lesezeichen und einen geteilten Link übersteht. `/rag` allein leitet auf die KI-gestützte Suche weiter.

Ein Chat bleibt auf der Seite, auf der er begonnen wurde. Ein Verlauf ist eine Abfolge: Seine Antworten wurden von einer der beiden Seiten erzeugt, und die nächste Runde spielt sie dem Modell als dessen eigene vorherige Arbeit vor, sodass ein Wechsel darunter eine spätere Frage auf eine Weise beantworten würde, die die früheren nicht stützen. Es gibt kein Bedienelement, das einen Chat hinüberverschiebt; das Öffnen von `/rag/search/<research-chat>` leitet auf den passenden Pfad weiter, und eine Runde, die an die Seite gesendet wird, zu der ein Chat nicht gehört, ergibt einen 409. Ein gespeicherter Chat öffnet sich wieder auf dem Pfad, auf dem er lief.

Konfigurieren Sie **Allgemeine KI**, das **Erweiterte Modell** und **Deep-Search-Sprachen** in den [Einstellungen](/de/ai_providers#binding-models-in-settings).

Wie ein Chat gespeichert, fortgesetzt und abgebrochen wird, steht unter [Chat-Sitzungen](/de/setup#chat-sessions).
