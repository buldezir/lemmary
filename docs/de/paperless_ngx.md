---
description: "Die Paperless-ngx-kompatible REST-API von Lemmary lässt bestehende Paperless-ngx-Clients und -Apps weiterarbeiten, und Lemmary kann die Bibliothek eines laufenden Paperless-ngx importieren."
---

# Paperless-ngx-API-Kompatibilität {#paperless-ngx-api-compatibility}

Lemmary stellt auf demselben Host wie PocketBase eine paperless-ngx-kompatible REST-API bereit (zum Beispiel `http://127.0.0.1:8090/api/`). Das Backend implementiert die Endpunkte, die Drittanbieter-Clients für Authentifizierung, Dokumente, Tags, Korrespondenten, Dokumenttypen und zugehörige Metadaten erwarten.

Die Kompatibilität ist bewusst unvollständig: Gängige Lese- und Schreibabläufe funktionieren, aber nicht jede paperless-ngx-Funktion ist verfügbar (zum Beispiel liefern einige Listen-Endpunkte leere Platzhalter, wo Lemmary keine entsprechenden Daten hat). `GET /api/documents/{id}/suggestions/` antwortet immer mit leeren Listen, da die KI von Lemmary die Metadaten eines Dokuments schon bei der Verarbeitung setzt, und `GET /api/documents/{id}/preview/` liefert die Originaldatei. Ein Pfad, den Lemmary nicht bereitstellt, antwortet mit `404 {"detail": "Not found."}`.

## Dokument-IDs {#document-ids}

Paperless-ngx adressiert Datensätze über eine ganzzahlige ID; PocketBase verwendet Zeichenketten mit 15 Zeichen. Lemmary speichert zu jedem Dokument, Tag, Korrespondenten und Dokumenttyp eine `ngx_id`, die aus einem Hash der PocketBase-ID abgeleitet wird, sodass IDs, die vor Einführung dieser Spalte ausgegeben wurden, weiterhin auf dieselben Datensätze zeigen. Sie ist pro Konto eindeutig, wird beim Anlegen vergeben und ändert sich danach nie – Clients speichern sie zwischen, und swift-paperless verwendet für seinen Vorschaubild-Cache eine URL, die sie enthält, als Schlüssel.

Beim Upgrade wird die bestehende Bibliothek in einer einzigen Migration durchnummeriert. Ergeben zwei Datensätze eines Kontos denselben Ausgangswert, erhält der zweite die nächste freie ID; vor Einführung der Spalte war der zweite über die Paperless-API überhaupt nicht erreichbar.

## Filter für die Dokumentliste {#document-list-filters}

`GET /api/documents/` versteht die Filter, die Clients tatsächlich senden:

| Filter | Parameter |
| --- | --- |
| Volltext | `query` |
| Titel und Inhalt | `text`, `title_search`, `title_content`, `title__icontains`, `content__icontains` |
| Tags | `tags__id`, `tags__id__all`, `tags__id__in`, `tags__id__none`, `is_tagged` |
| Dokumenttyp | `document_type__id`, `document_type__id__in`, `document_type__id__none`, `document_type__isnull` |
| Korrespondent | `correspondent__id`, `correspondent__id__in`, `correspondent__id__none`, `correspondent__isnull` |
| Dokumentdatum | `created__date__{gt,gte,lt,lte}`, `created__{gt,gte,lt,lte}`, `created__year` |
| Hochladedatum | `added__date__{gt,gte,lt,lte}`, `added__{gt,gte,lt,lte}`, `added__year` |
| Letzte Änderung | `modified__date__{gt,gte,lt,lte}`, `modified__{gt,gte,lt,lte}`, `modified__year` |
| Eigentümer | `owner__id`, `owner__id__in`, `owner__id__none`, `owner__isnull` |
| Bestimmte Dokumente | `id`, `id__in` |
| Seitenaufteilung und Form | `page`, `page_size`, `ordering`, `truncate_content`, `fields` |

Filter lassen sich kombinieren, und `count` entspricht immer der gefilterten Menge, sodass das seitenweise Durchblättern einer gefilterten Liste sicher ist.

Zwei dieser Gruppen verdienen ein Wort zu Granularität und Geltungsbereich:

- **`added__{gt,gte,lt,lte}` und `modified__{gt,gte,lt,lte}` vergleichen den vollständigen Zeitpunkt**, sodass `added__gt=2025-06-15T10:00:00Z` Uploads von später am selben Vormittag zurückgibt; mit `modified__gt` holen Clients wie Paperparrot nur, was sich seit ihrer letzten Synchronisierung geändert hat. Die Formen `added__date__` vergleichen den Tag, ebenso wie jeder `created`-Vergleichsoperator: Das eigene Datum eines Dokuments hat keine Uhrzeit. Ein Dokument ohne eigenes Datum gilt als am Tag seines Hochladens datiert, und dieses Datum wird dem Client auch dafür angezeigt.
- **Eigentümerfilter werden beantwortet, nicht angewendet.** Jedes Dokument, das diese API zurückgeben kann, gehört entweder dem Aufrufer oder wurde ihm von einem anderen Konto schreibgeschützt freigegeben; die Angabe dieser Konten schränkt daher nichts ein, und die Angabe irgendeines anderen Kontos findet nichts. Ein freigegebenes Dokument wird wie ein eigenes aufgelistet, abgerufen, heruntergeladen und durchsucht, trägt die Tags, den Korrespondenten und den Typ seines Eigentümers und lehnt `PATCH` und `DELETE` mit einem `404` ab.

Drei Dinge verhalten sich bewusst anders als bei paperless-ngx:

- **Ein Filter, den Lemmary nicht umsetzen kann, ergibt einen `400`, keine ungefilterte Seite.** Lemmary hat keine Speicherpfade oder Archiv-Seriennummern, und seine [eigenen Felder](/de/setup#custom-fields) sind über diese API nicht erreichbar (`/api/custom_fields/` listet keine), daher wird eine Anfrage, die nach einem davon filtert, mit `{"detail": "Unsupported filter \"…\"."}` abgelehnt. Einen `200` zurückzugeben, der den Filter ignoriert, wäre schlimmer: Der Client stellt ihn so dar, als wäre der Filter angewendet worden, und aus „Dokumente mit dem Tag Rechnung“ wird stillschweigend das gesamte Archiv.
- **Die Textsuche findet ganze Wörter, keine Teilzeichenketten.** Alle sechs Textfilter, auch `text` und `title_search`, die Paperless-ngx 3 als Teilzeichenketten abgleicht, laufen über denselben Bleve-Index wie das Suchfeld der Weboberfläche, der tokenisiert ist. Eine Suche nach `rechn` findet `Rechnung` nur als *Präfix*, und nur dann, wenn kein Dokument `rechn` als ganzes Wort enthält; `rechnung` trifft direkt. Ein Begriff innerhalb eines Wortes (`echnung`) findet nichts, und ein Begriff, der eine Ziffer enthält, wird nie als Präfix verwendet.
- **Eine gefilterte Textsuche zählt höchstens 5000 Treffer auf.** Darüber hinaus ist der gemeldete `count` zu niedrig – und zwar konsistent, sodass die Seitenlinks nie über das hinauszeigen, was geliefert werden kann.

Die Ergebnisse werden nach Relevanz sortiert, wenn ein Textfilter vorhanden ist und `ordering` fehlt, `score` oder `-score` ist oder ein Feld nennt, nach dem dieser Server nicht sortiert – genau so verhält sich auch paperless-ngx. Jede andere `ordering` wird von der Datenbank bedient. `ordering=id` sortiert nach der ganzzahligen ID, die dem Client angezeigt wurde, und `ordering=created` nach demselben Datum, das die Antwort meldet. Eine Liste ohne Textfilter und ohne erkannte `ordering` wird mit dem neuesten Upload zuerst zurückgegeben.

## Externe Clients anbinden {#connecting-external-clients}

1. Richten Sie den Client auf die URL Ihres Lemmary-Servers (Schema + Host + Port, ohne das Suffix `/api` – Clients fügen es selbst hinzu). Manche Clients, darunter Paperparrot, prüfen den Server vor der Anmeldung mit `GET /api/remote_version/`; der Endpunkt antwortet wie bei Paperless-ngx ohne Anmeldedaten.
2. Melden Sie sich mit einem PocketBase-Benutzerkonto an. Der Endpunkt `/api/token/` akzeptiert denselben Benutzernamen und dasselbe Passwort wie die Weboberfläche und gibt ein langlebiges JWT (zehn Jahre) zurück. Paperless-ngx-Clients speichern dieses Token und erneuern es nicht; es ist nicht die fünftägige Sitzung der Weboberfläche. Eine Änderung des Kontopassworts macht es ungültig.
3. Clients, die `Authorization: Token <jwt>` senden (im Stil von paperless-ngx), werden neben normalen Bearer-Tokens unterstützt.
4. Clients, die statt eines Passworts ein Token verlangen, etwa Paperparrot, erhalten es in der Weboberfläche unter **Konto → Paperless-ngx-Apps**. Es ist dasselbe Token mit zehn Jahren Laufzeit und der Weg für ein Konto, das sich per Passkey oder OAuth anmeldet und dessen Nutzer sein Passwort nicht kennt. Clients können selbst eines mit `POST /api/profile/generate_auth_token/` erzeugen, wie bei paperless-ngx; anders als dort liefert jeder Aufruf ein neues Token, und die früheren bleiben gültig. Eine Passwortänderung unter **Konto → Passwort** widerruft alle Tokens auf einmal, auch die für Agenten; ein einzelnes lässt sich nicht widerrufen.

Die API-Versionen 9 und 10 werden über den `Accept`-Header akzeptiert (`application/json; version=10`), dasselbe Paar wie bei Paperless-ngx 3. Wie dort wird eine Anfrage ohne Version als Version 10 beantwortet, und jede Antwort trägt `X-Api-Version: 10` und `X-Version: 3.0.0`, die Version, deren API hier nachgebildet ist; Clients, die Funktionen an der Serverversion festmachen, behandeln Lemmary daher wie Paperless-ngx 3.0.

Dokumente tragen die Versionsfelder von Paperless-ngx 3, aber Lemmary speichert eine Datei pro Dokument: Jedes meldet sich als Wurzel (`root_document` ist `null`), deren einziger Eintrag in `versions` es selbst ist. Die Versions-Endpunkte werden nicht bereitgestellt.

## Aufgaben {#tasks}

`GET /api/tasks/` meldet die Verarbeitungsaufträge von Lemmary als Paperless-Aufgaben, die neuesten zuerst. Unter API-Version 10 ist die Liste seitenweise, jede Aufgabe hat die Felder von Paperless-ngx 3 (`task_type`, `trigger_source`, `status`, `result_data`, `related_document_ids`), gefiltert wird nach `status`, `is_complete`, `task_type`, `acknowledged` und `task_id`, sortiert mit `ordering=date_created` oder `date_done`. Unter Version 9 ist es die ältere Liste ohne Seiten, begrenzt auf 100. Jede Aufgabe ist ein `consume_file`, und da Lemmary nicht festhält, woher ein Upload kam, ist `trigger_source` immer `web_ui`.

`GET /api/tasks/active/` und `GET /api/tasks/status_counts/` antworten wie bei Paperless-ngx 3; `/api/tasks/summary/` und `POST /api/tasks/run/` werden nicht bereitgestellt. `POST /api/acknowledge_tasks/` (und `/api/tasks/acknowledge/`, sein Name seit paperless-ngx 2.14) quittiert Aufgaben anhand der ID oder mit `{"all": true}` alle offenen.

Die Quittierung wird in einer Spalte `ngx_acknowledged` in `processing_jobs` gespeichert, die nur diese API liest oder schreibt – die eigene Oberfläche von Lemmary zeigt den Verarbeitungsstatus am Dokument an und kennt kein Quittieren. `acknowledged=true` und `acknowledged=false` filtern danach; ohne den Parameter wird beides zurückgegeben.

## Import aus Paperless-ngx {#importing-from-paperless-ngx}

Jeder angemeldete Benutzer kann eine Paperless-ngx-Bibliothek in sein eigenes Lemmary-Konto migrieren. Das entfernte API-Token authentifiziert einen bestimmten ngx-Benutzer, daher läuft der Import als der aktuelle lokale Benutzer und nicht als Administrator.

1. Öffnen Sie **Importieren** im Menü „Mehr“ und dann den Tab **Paperless-ngx** (oder rufen Sie `/import/ngx` auf).
2. Geben Sie die Basis-URL der entfernten Paperless-ngx-Instanz und ein API-Token aus dem Profil dieser Instanz ein.
3. Wählen Sie einen Importmodus:
   - **Paperless-ngx-Metadaten beibehalten** (`preserve`): legt Tags, Korrespondenten und Dokumenttypen anhand des Namens an oder aktualisiert sie; lädt jedes Dokument mit seinem OCR-`content`, Titel, Datum und seinen Taxonomie-Verknüpfungen herunter. Vorschau und Duplikaterkennung laufen weiterhin; die KI-Metadatenextraktion wird übersprungen, damit die entfernten Metadaten erhalten bleiben.
   - **Nur Dateien importieren und erneut verarbeiten** (`reprocess`): lädt nur die Originaldateien herunter und stellt die vollständige OCR- + KI-Pipeline wie bei einem neuen Upload in die Warteschlange.
4. Starten Sie den Import. Exakte Dateiduplikate (gleiche Prüfsumme) werden übersprungen.

Derselbe Ablauf steht als `POST /api/app/import/ngx` mit dem JSON-Body `{ "url": "...", "api_key": "...", "mode": "preserve" | "reprocess" }` zur Verfügung. Die Anfrage gibt `202 Accepted` mit `{ "job_id", "status": "running" }` zurück. Fragen Sie `GET /api/app/import/ngx/status?job_id=...` ab, bis `status` den Wert `completed` (mit `result`) oder `failed` (mit `error`) hat. Der Auftragsstatus wird nur für den laufenden Prozess im Arbeitsspeicher gehalten. Pro Benutzer kann jeweils ein Import laufen. `mode` ist standardmäßig `preserve`. Der API-Schlüssel wird nicht gespeichert.

Der Import ruft nur die vom Aufrufer angegebene URL ab. Private, Loopback- und Link-Local-Ziele sind standardmäßig gesperrt (auch nach Weiterleitungen). Setzen Sie `IMPORT_ALLOW_PRIVATE=1`, wenn sich die entfernte Paperless-ngx-Instanz in einem privaten Netzwerk befindet; Cloud-Metadaten-Adressen bleiben gesperrt.

## swift-paperless (iOS) {#swift-paperless-ios}

[swift-paperless](https://github.com/paulgessinger/swift-paperless) ist der wichtigste mobile Client, mit dem diese API erprobt wird. Dokumente durchsuchen, Details ansehen, suchen, filtern und hochladen funktionieren im Allgemeinen. Einige paperless-ngx-spezifische Einstellungen oder erweiterte Funktionen können fehlen oder wirkungslos sein, weil Lemmary nicht den vollen Funktionsumfang von paperless-ngx implementiert.

Beim Öffnen der Dokumentliste werden 250 Dokumente und anschließend für jedes davon ein Vorschaubild abgerufen, jeweils mit einem eigenen `GET /api/documents/{id}/thumb` – paperless-ngx hat keinen Batch-Endpunkt für Vorschaubilder, und swift-paperless lädt die ganze Seite im Voraus statt nur der sichtbaren Zeilen. Diese Lastspitze ist zu erwarten und fällt nur bei leerem Cache an: Vorschaubilder werden mit einem 30-tägigen `Cache-Control` ausgeliefert, und die App führt einen eigenen Cache auf dem Gerät, dessen Schlüssel die URL ist.

Wenn die App 401 zurückgibt, nachdem sie beim Hinzufügen des Servers funktioniert hat, entfernen Sie den Server einmal und fügen ihn erneut hinzu, damit sie ein neues Token abrufen kann. Tokens, die vor den langlebigen `/api/token/`-JWTs ausgegeben wurden, laufen nach fünf Tagen ab und können nicht nachträglich verlängert werden.
