# Paperless-ngx-API-Kompatibilität {#paperless-ngx-api-compatibility}

Lemmary stellt auf demselben Host wie PocketBase eine paperless-ngx-kompatible REST-API bereit (zum Beispiel `http://127.0.0.1:8090/api/`). Das Backend implementiert die Endpunkte, die Drittanbieter-Clients für Authentifizierung, Dokumente, Tags, Korrespondenten, Dokumenttypen und zugehörige Metadaten erwarten.

Die Kompatibilität ist bewusst unvollständig: Gängige Lese- und Schreibabläufe funktionieren, aber nicht jede paperless-ngx-Funktion ist verfügbar (zum Beispiel liefern einige Listen-Endpunkte leere Platzhalter, wo Lemmary keine entsprechenden Daten hat).

## Dokument-IDs {#document-ids}

Paperless-ngx adressiert Datensätze über eine ganzzahlige ID; PocketBase verwendet Zeichenketten mit 15 Zeichen. Lemmary speichert zu jedem Dokument, Tag, Korrespondenten und Dokumenttyp eine `ngx_id`, die aus einem Hash der PocketBase-ID abgeleitet wird, sodass IDs, die vor Einführung dieser Spalte ausgegeben wurden, weiterhin auf dieselben Datensätze zeigen. Sie ist pro Konto eindeutig, wird beim Anlegen vergeben und ändert sich danach nie – Clients speichern sie zwischen, und swift-paperless verwendet für seinen Vorschaubild-Cache eine URL, die sie enthält, als Schlüssel.

Beim Upgrade wird die bestehende Bibliothek in einer einzigen Migration durchnummeriert. Ergeben zwei Datensätze eines Kontos denselben Ausgangswert, erhält der zweite die nächste freie ID; vor Einführung der Spalte war der zweite über die Paperless-API überhaupt nicht erreichbar.

## Filter für die Dokumentliste {#document-list-filters}

`GET /api/documents/` versteht die Filter, die Clients tatsächlich senden:

| Filter | Parameter |
| --- | --- |
| Volltext | `query` |
| Titel und Inhalt | `title_content`, `title__icontains`, `content__icontains` |
| Tags | `tags__id`, `tags__id__all`, `tags__id__in`, `tags__id__none`, `is_tagged` |
| Dokumenttyp | `document_type__id`, `document_type__id__in`, `document_type__id__none`, `document_type__isnull` |
| Korrespondent | `correspondent__id`, `correspondent__id__in`, `correspondent__id__none`, `correspondent__isnull` |
| Dokumentdatum | `created__date__{gt,gte,lt,lte}`, `created__{gt,gte,lt,lte}`, `created__year` |
| Hochladedatum | `added__date__{gt,gte,lt,lte}`, `added__{gt,gte,lt,lte}`, `added__year` |
| Eigentümer | `owner__id`, `owner__id__in`, `owner__id__none`, `owner__isnull` |
| Bestimmte Dokumente | `id`, `id__in` |
| Seitenaufteilung und Form | `page`, `page_size`, `ordering`, `truncate_content`, `fields` |

Filter lassen sich kombinieren, und `count` entspricht immer der gefilterten Menge, sodass das seitenweise Durchblättern einer gefilterten Liste sicher ist.

Zwei dieser Gruppen verdienen ein Wort zu Granularität und Geltungsbereich:

- **`added__{gt,gte,lt,lte}` vergleichen den vollständigen Zeitpunkt**, sodass `added__gt=2025-06-15T10:00:00Z` Uploads von später am selben Vormittag zurückgibt. Die Formen `added__date__` vergleichen den Tag, ebenso wie jeder `created`-Vergleichsoperator: Das eigene Datum eines Dokuments hat keine Uhrzeit. Ein Dokument ohne eigenes Datum gilt als am Tag seines Hochladens datiert, und dieses Datum wird dem Client auch dafür angezeigt.
- **Eigentümerfilter werden beantwortet, nicht angewendet.** Jedes Dokument, das diese API zurückgeben kann, gehört entweder dem Aufrufer oder wurde ihm von einem anderen Konto schreibgeschützt freigegeben; die Angabe dieser Konten schränkt daher nichts ein, und die Angabe irgendeines anderen Kontos findet nichts. Ein freigegebenes Dokument wird wie ein eigenes aufgelistet, abgerufen, heruntergeladen und durchsucht, trägt die Tags, den Korrespondenten und den Typ seines Eigentümers und lehnt `PATCH` und `DELETE` mit einem `404` ab.

Drei Dinge verhalten sich bewusst anders als bei paperless-ngx:

- **Ein Filter, den Lemmary nicht umsetzen kann, ergibt einen `400`, keine ungefilterte Seite.** Lemmary hat keine Speicherpfade oder Archiv-Seriennummern, und seine [eigenen Felder](/de/setup#custom-fields) sind über diese API nicht erreichbar (`/api/custom_fields/` listet keine), daher wird eine Anfrage, die nach einem davon filtert, mit `{"detail": "Unsupported filter \"…\"."}` abgelehnt. Einen `200` zurückzugeben, der den Filter ignoriert, wäre schlimmer: Der Client stellt ihn so dar, als wäre der Filter angewendet worden, und aus „Dokumente mit dem Tag Rechnung“ wird stillschweigend das gesamte Archiv.
- **Die Textsuche findet ganze Wörter, keine Teilzeichenketten.** Alle vier Textfilter laufen über denselben Bleve-Index wie das Suchfeld der Weboberfläche, der tokenisiert ist. Eine Suche nach `rechn` findet `Rechnung` nur als *Präfix*, und nur dann, wenn kein Dokument `rechn` als ganzes Wort enthält; `rechnung` trifft direkt. Ein Begriff innerhalb eines Wortes (`echnung`) findet nichts, und ein Begriff, der eine Ziffer enthält, wird nie als Präfix verwendet.
- **Eine gefilterte Textsuche zählt höchstens 5000 Treffer auf.** Darüber hinaus ist der gemeldete `count` zu niedrig – und zwar konsistent, sodass die Seitenlinks nie über das hinauszeigen, was geliefert werden kann.

Die Ergebnisse werden nach Relevanz sortiert, wenn ein Textfilter vorhanden ist und `ordering` fehlt, `score` oder `-score` ist oder ein Feld nennt, nach dem dieser Server nicht sortiert – genau so verhält sich auch paperless-ngx. Jede andere `ordering` wird von der Datenbank bedient. `ordering=id` sortiert nach der ganzzahligen ID, die dem Client angezeigt wurde, und `ordering=created` nach demselben Datum, das die Antwort meldet. Eine Liste ohne Textfilter und ohne erkannte `ordering` wird mit dem neuesten Upload zuerst zurückgegeben.

## Externe Clients anbinden {#connecting-external-clients}

1. Richten Sie den Client auf die URL Ihres Lemmary-Servers (Schema + Host + Port, ohne das Suffix `/api` – Clients fügen es selbst hinzu).
2. Melden Sie sich mit einem PocketBase-Benutzerkonto an. Der Endpunkt `/api/token/` akzeptiert denselben Benutzernamen und dasselbe Passwort wie die Weboberfläche und gibt ein langlebiges JWT (zehn Jahre) zurück. Paperless-ngx-Clients speichern dieses Token und erneuern es nicht; es ist nicht die fünftägige Sitzung der Weboberfläche. Eine Änderung des Kontopassworts macht es ungültig.
3. Clients, die `Authorization: Token <jwt>` senden (im Stil von paperless-ngx), werden neben normalen Bearer-Tokens unterstützt.

Die API-Versionen 9 und 10 werden über den `Accept`-Header akzeptiert (`application/json; version=9`).

## Aufgaben {#tasks}

`GET /api/tasks/` meldet die Verarbeitungsaufträge von Lemmary als Paperless-Aufgaben, die neuesten zuerst, begrenzt auf 100 pro Antwort. `POST /api/acknowledge_tasks/` (und `/api/tasks/acknowledge/`, sein Name seit paperless-ngx 2.14) quittiert sie anhand der ID.

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
