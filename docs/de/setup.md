# Konfigurationsleitfaden {#configuration-guide}

Laufzeitkonfiguration, Entscheidungen beim ersten Start und wie sich die
Funktionen von Lemmary verhalten. Für den üblichen Installationsweg beginnen Sie
mit [Self-Hosting mit Docker](/de/self_hosting). Host-Toolchains und Builds aus dem
Quellcode sind separat unter [Entwicklungsumgebung](/de/development) beschrieben.

Zwei Bereiche haben eigene Anleitungen:

- [KI-Anbieter und Modelle](/de/ai_providers) – welcher Anbieter zu wählen ist, der
  Block `AI_*` / `OCR_*` und was Embeddings kosten
- [Paperless-ngx-API-Kompatibilität](/de/paperless_ngx) – die kompatible
  `/api/`-Schnittstelle, die Anbindung von Clients Dritter und der Import einer
  bestehenden Bibliothek

## Umgebungsvariablen {#environment-variables}

Alle Variablen stehen in `.env` im Projektstammverzeichnis (siehe `.env.example`).
Die Familien `AI_*` und `OCR_*` sind unter
[KI-Anbieter und Modelle](/de/ai_providers#the-provider-block) dokumentiert.

`MANAGED` und die `LIMIT_*`-Variablen sind für gehostete Deployments gedacht und
nicht für das Selbst-Hosting; lassen Sie sie ungesetzt.

### Immer aus der Umgebung gelesen {#always-env-backed}

| Variable | Standard | Beschreibung |
| --- | --- | --- |
| `WORKER_CRON_EXPR` | `* * * * *` | Cron-Ausdruck für das Einsammeln hängengebliebener wartender Jobs (einmalig beim Start registriert) |
| `LOG_LEVEL` | nicht gesetzt (kein slog auf stdout) | Mindeststufe für JSON-slog-Zeilen auf stdout (`debug`, `info`, `warn`/`warning`, `error`). Wird ignoriert, solange PocketBase `--dev` aktiv ist (dieser Modus gibt bereits auf der Konsole aus, einschließlich SQL). PocketBase Admin → Settings → Logs steuert weiterhin die Log-Tabelle. |
| `METRICS_ADDR` | nicht gesetzt (aus) | Adresse für den OpenTelemetry-Metrik-Endpunkt, der als Prometheus-Scrape-Ziel auf einem eigenen Port bereitgestellt wird, damit nichts, was ihn abfragt, ein Zugangsdatum für das Archiv besitzt. Ein reiner Port (`9464`) oder einer ohne Host (`:9464`) lauscht auf jeder Schnittstelle; nur diese Form ist in Docker über einen veröffentlichten Port erreichbar &mdash; die Portzuordnung steht bewusst nicht in `docker-compose.yml`, da ein Endpunkt ohne Authentifizierung nicht standardmäßig aus dem Internet erreichbar werden sollte. Geben Sie einen Host an (`127.0.0.1:9464`), um ihn auf Loopback zu halten, wenn Sie das Binary direkt betreiben. Stellt bereit: Anfragerate und Latenz pro Route, Jobdauern nach Ergebnis, Tiefe der wartenden Warteschlange, ausgehende KI-/OCR-Latenz pro Anbieter und Modell, Tokenzahlen, an einen OCR-Anbieter gesendete Seiten, Deep-Search-Läufe nach Modus, wie viel die Instanz enthält (Dokumente, Seiten, gespeicherte Bytes, Konten sowie die größte gespeicherte Datei in Seiten und Bytes) sowie Go-Laufzeitmetriken; keine Dokumenttexte, Kontonamen oder Pfade. Der Bestand wird über zwei Gauges unter dem Label `resource` gemeldet: `lemmary_usage` für die Anzahlen und `lemmary_usage_bytes` für die Bytes. Kostet drei Abfragen pro Scrape: ein `COUNT` über `processing_jobs` sowie das Nutzungsaggregat (ein reiner Index-Scan über `documents` und ein `COUNT` über `users`). Ein Port, der sich nicht binden lässt, wird protokolliert, und die App läuft trotzdem weiter. |
| `MCP_ENABLED` | nicht gesetzt (an) | Auf `0`/`false`/`no`/`off` setzen, um den [Model-Context-Protocol](/de/mcp)-Endpunkt unter `POST /api/mcp` auf dem App-Port zu entfernen. Er ist standardmäßig an, weil er nur auf das gewöhnliche Bearer-Token antwortet und nichts kostet, bis ein Agent ihn aufruft. Such-Tools (`search_documents`, `read_documents`, `count_documents`) teilen sich den Index von Deep Search; Tools für den direkten Zugriff (`list_documents`, `get_document`, `list_taxonomy`) lesen die Zeilen direkt. Alle sind auf den Benutzer des Tokens beschränkt. Schreibgeschützt, solange ein Admin unter Einstellungen → MCP keine Schreibwerkzeuge einschaltet. Kostet eine Embedding-Anfrage pro Suche und pro Lesevorgang mit `focus`, wenn Embeddings konfiguriert sind; Lesevorgänge liefern Textauszüge und rufen nie ein Sprachmodell auf. Wird beim Start gelesen. |
| `IMPORT_ALLOW_PRIVATE` | nicht gesetzt (gesperrt) | Auf `1`/`true` setzen, damit der ngx-Import Loopback- und RFC1918-Hosts erreichen darf. Link-Local- und Cloud-Metadaten-Adressen bleiben gesperrt. Nötig, wenn Paperless-ngx im selben LAN oder Docker-Netzwerk liegt. |
| `UPLOAD_MAX_MB` | `100` | Obergrenze für einen bereitgestellten PDF-Upload zum Aufteilen, in Megabyte. Wird beim Start gelesen, nicht aus den Einstellungen: Das Bereitstellen eines PDFs kostet während des Renderns der Seiten ein Mehrfaches seiner Größe an Speicher, daher schützt der Wert den Host ebenso sehr, wie er das Produkt formt. Ein fehlerhafter oder nicht positiver Wert fällt auf den Standard zurück, statt den Start scheitern zu lassen. Uploads einzelner Dateien werden separat über das Feld `documents.file` begrenzt (47 MB). |
| `INGEST_DIR` | nicht gesetzt (aus) | Ein Verzeichnis im Container, das als Import-Ordner überwacht wird, siehe [Import-Ordner](#ingest-folder). Binden Sie dort einen Host-Ordner ein (`docker-compose.yml` enthält die Zeile auskommentiert). Wird beim Start gelesen; Eigentümer, Scan-Intervall und der Schalter zum Löschen nach dem Import befinden sich unter Einstellungen → Ingest. Kostet einen rekursiven Verzeichnisdurchlauf pro Intervall und einen Lesevorgang pro neuer Datei; bei ausgeschaltetem Löschen eine kleine Datenbankzeile pro importierter Datei. |
| `INGEST_IMAP_ENABLED` | nicht gesetzt (aus) | Auf `1`/`true` setzen, um ein IMAP-Postfach zu lesen und dessen Anhänge in Dokumente umzuwandeln, siehe [Import per IMAP](#ingest-from-imap). Wird beim Start gelesen; Server, Anmeldedaten, Ordner und die Aktion nach dem Import befinden sich unter Einstellungen → Ingest. Kostet eine IMAP-Anmeldung und eine UID-Suche pro Intervall, einen BODYSTRUCTURE-Abruf pro neuer Nachricht und den Download jedes speicherbaren Anhangs. Das Postfachpasswort wird wie die API-Schlüssel der Anbieter in `app_settings` gespeichert und ist nur mit `VAULT_ENABLED` auf der Festplatte verschlüsselt. |
| `IMPORT_STAGING_MAX_BYTES` | `1073741824` (1 GiB) | Obergrenze für ein zum Import bereitgestelltes Archiv (Amazon-Bestellungen, Lemmary-Sicherung), in Byte. Das Bereitstellen eines neuen Archivs verwirft das vorherige desselben Kontos, daher ist dies auch der Plattenplatz, den ein einzelnes Konto belegen kann, während es über die Bestätigung entscheidet – die Obergrenze des Bereitstellungsbereichs liegt grob bei diesem Wert mal der Anzahl der Konten. Verringern Sie ihn auf einem kleinen Volume; erhöhen Sie ihn für eine Bibliothek, deren Sicherung über ein Gigabyte hinausgeht. Ein fehlerhafter Wert oder einer unter 1 MiB fällt auf den Standard zurück, statt jeden Upload abzulehnen. |
| `PASSKEY_RP_ID` | aus dem Host der Anfrage abgeleitet | Relying-Party-ID für die [Passkey-Anmeldung](/de/passkeys): ein reiner Domainname, ohne Schema und ohne Port. Standardmäßig der Hostname, mit dem die Anfrage eintraf, was immer dann richtig ist, wenn der Proxy den öffentlichen `Host` weiterleitet. Setzen Sie ihn, wenn das nicht der Fall ist, oder um eine übergeordnete Domain festzulegen (`example.com`, während `app.example.com` ausgeliefert wird). **Jeder registrierte Passkey ist an diesen Wert gebunden – eine Änderung macht alle unbrauchbar.** Wird beim Start gelesen, nicht aus den Einstellungen. |
| `PASSKEY_ORIGINS` | aus Schema + Host der Anfrage abgeleitet | Kommagetrennte vollständige Origins (Schema, Host und Port), die eine Passkey-Zeremonie abschließen dürfen. Standardmäßig der Origin, über den die Anfrage eintraf, wobei für das Schema `X-Forwarded-Proto` verwendet wird, sofern vorhanden. Setzen Sie den Wert, wenn die App unter mehr als einem Origin erreichbar ist oder wenn ein TLS-terminierender Proxy diesen Header nicht setzt. |
| `VITE_POCKETBASE_URL` | `http://127.0.0.1:8090` | PocketBase-API-URL (Frontend) |
| `SETUP_ADMIN_EMAIL` | — | Das erste Admin-Konto, angelegt beim ersten Start, der keines vorfindet. Legt einen `_superusers`-Datensatz **und** das zugehörige `users`-Konto an, genau wie der Einrichtungsassistent. Setzt nie ein bereits vorhandenes Passwort zurück. In einem Entwicklungs-Build meldet sich die SPA außerdem selbst mit diesem Paar an; ein Produktions-Bundle enthält keinen der beiden Werte. **In `.env.example` auskommentiert** – ihn in einer ausgelieferten Installation einzukommentieren, würde ihr einen Admin verschaffen, dessen Passwort in diesem Repository veröffentlicht ist. |
| `SETUP_ADMIN_PASSWORD` | — | Das zugehörige Passwort, mindestens 8 Zeichen. Für die Lebensdauer des Containers über `docker inspect` und `/proc/<pid>/environ` lesbar, daher ist dies für lokale und CI-Instanzen gedacht – eine ausgelieferte Installation sollte den Assistenten oder `superuser upsert` verwenden. |

## Einrichtungsassistent beim ersten Start {#first-launch-setup-wizard}

Bei einer frischen Installation sperrt die SPA alles, bis die Einrichtung
abgeschlossen ist:

1. **Admin anlegen** – E-Mail + Passwort. Legt ein PocketBase-Konto `_superusers` **und** ein passendes `users`-Konto (mit denselben Zugangsdaten) an, damit der Admin Dokumente besitzen kann. Ersetzt die Browser-Installer-Oberfläche von PocketBase.
2. **Passkey** *(optional)* – bietet an, einen [Passkey](/de/passkeys) für das soeben angelegte Konto hinzuzufügen. Das Überspringen ändert nichts, und das Angebot kommt nicht wieder; ein Passkey lässt sich später unter **Mehr → Konto** hinzufügen. Der Schritt wird auf einer Adresse ausgeblendet, unter der kein Passkey erstellt werden kann (eine IP-Adresse oder reines HTTP außerhalb von `localhost`).
3. **Anbieter** – das geführte Formular aus der [geführten Einrichtung der KI-Anbieter](/de/guided_ai_setup): ein Mistral-Schlüssel (OCR und Embeddings) und ein weiterer Anbieter für Sprachmodelle, beide mit einem einzigen Absenden angelegt. Jede Hälfte darf leer bleiben; **Stattdessen einen Anbieter manuell hinzufügen** wechselt zum Formular für jeweils einen Anbieter, dem Weg zu einer ChatGPT-Anmeldung, einem `google_vision`-Schlüssel oder `docling`/`local`, den schlüssellosen Sidecars.
4. **Modelle** – wählen Sie Anbieter → Modell für OCR und Allgemeine KI (alles, was ein Sprachmodell tut) und optional für Embeddings. Alle drei sind vorausgefüllt, wenn die Anbieter aus dem geführten Formular stammen; Embeddings lassen sich auf **Keiner** setzen, und die Einrichtung ist auch ohne sie abgeschlossen.

Die Schritte 3 und 4 werden übersprungen, wenn `.env` die Schlüssel bereits
enthält – siehe [KI-Anbieter und Modelle](/de/ai_providers). Ebenso kann der Admin
aus der Umgebung stammen (`SETUP_ADMIN_EMAIL` / `SETUP_ADMIN_PASSWORD`, angewendet
beim ersten Start, der kein Konto vorfindet) oder aus der CLI (`go run . superuser
upsert EMAIL PASS` aus `backend/`, was auch das zugehörige `users`-Konto anlegt
bzw. aktualisiert). Mit beidem startet ein frisches Volume, ohne dass noch etwas
zu beantworten bleibt. Solange keine Schlüssel vorhanden sind, sehen normale
Benutzer einen Bildschirm „Einrichtung unvollständig“; nur ein Admin kann die
Konfiguration abschließen.

## Einstellungen (Admin-Oberfläche) {#settings-admin-ui}

1. Melden Sie sich mit der E-Mail-Adresse und dem Passwort des **Admins** an (die Anmeldung bevorzugt das `users`-Konto; ältere Installationen mit nur `_superusers` werden automatisch über `/api/app/ensure-user` verknüpft, das ein verborgenes Flag `is_app_admin` am zugehörigen `users`-Datensatz setzt).
2. Öffnen Sie **Einstellungen** in der Navigation (angezeigt, wenn `/api/app/me` `is_admin` meldet). Es gibt einen Tab pro Bereich, jeder unter einem eigenen Pfad, und jeder speichert nur seine eigenen Felder: **Darstellung** (`/settings`), **KI** (`/settings/ai`), **Verarbeitung** (`/settings/processing`), **Worker** (`/settings/worker`), **Duplikate** (`/settings/duplicates`) und, wenn `INGEST_DIR` oder `INGEST_IMAP_ENABLED` gesetzt ist, **Ingest** (`/settings/ingest`). Fügen Sie im KI-Tab Anbieter hinzu und ordnen Sie dann OCR, Allgemeine KI und optional das Erweiterte Modell einem Anbieter und Modell zu – siehe [Modelle in den Einstellungen zuordnen](/de/ai_providers#binding-models-in-settings). Änderungen laden die prozessinternen Clients im laufenden Betrieb neu (kein Neustart).

`WORKER_CRON_EXPR` ist dort nicht bearbeitbar; ändern Sie `.env` und starten Sie neu, oder verwenden Sie PocketBase Admin → Settings → Crons.

**Zusätzliche Extraktionsregeln** (Tab Verarbeitung) ist der einzige Teil des Extraktions-Prompts, den ein Admin selbst schreibt. Was darin steht, wird an den eingebauten Prompt angehängt, nach der Liste der vorhandenen Korrespondenten und Dokumenttypen und vor den Formatregeln, sodass sich Hauskonventionen formulieren lassen, die der feste Prompt nicht kennen kann – „behandle *Rechnung* als Dokumenttyp Invoice“, „versieh Versicherungsdokumente mit der Policennummer als Tag“. Welche Felder gespeichert werden, lässt sich damit nicht ändern: Die Pipeline parst die Antwort in einen festen Satz von Feldern, und der Prompt sagt das nach den Regeln auch. Bis zu 4000 Zeichen, standardmäßig leer, und angewendet auf Dokumente, die ab dann verarbeitet oder erneut verarbeitet werden. Die Logzeile der Extraktion meldet ihre Länge als `rule_chars`, und der `extract_metadata`-Schrittlauf jedes Dokuments hält den Prompt fest, unter dem er tatsächlich lief (siehe unten).

`EXTRACTION_PROMPT_VERSION` wird dort ebenfalls nicht angeboten. Der Wert dient reiner Buchführung – er wird im `extract_metadata`-Schrittlauf jedes Dokuments festgehalten, damit sich Metadaten auf einen Prompt zurückführen lassen, und erreicht den Prompt selbst nie –, daher gibt es für einen Admin nichts einzustellen. Wo Extraktionsregeln gesetzt sind, hält dieser Schrittlauf statt der reinen Version `v1+rules.<digest>` fest: Die Regeln ändern den Prompt, die Version aber nicht, und ein Lauf, der nur unter `v1` festgehalten wird, würde einen Prompt benennen, den es nicht mehr gibt. Dokumente, die ohne Regeln extrahiert wurden, behalten die reine Version, sodass sich für eine Instanz ohne Regeln nichts ändert. `PATCH /api/app/settings` akzeptiert weiterhin `extraction_prompt_version`, und der Wert lässt sich in PocketBase Admin → `app_settings` bearbeiten.

### Import-Ordner {#ingest-folder}

Ist `INGEST_DIR` gesetzt, durchläuft ein Cron-Job dieses Verzeichnis und macht aus jeder speicherbaren Datei (dieselben Typen, die der Upload akzeptiert: PDF, JPEG, PNG, WebP, TXT, CSV, DOCX, XLSX) ein Dokument, genau so, als wäre sie hochgeladen worden: Die Datei wird gehasht, an der Dokumentobergrenze von 47 MB und [der Seitenobergrenze](#the-page-ceiling) gemessen und für die vollständige Pipeline eingereiht. Der Durchlauf ist rekursiv (ein Stammverzeichnis, das selbst ein Symlink ist, wird verfolgt), und die Ordner zwischen dem Stammverzeichnis und der Datei werden zu deren Tags – `Taxes/2024/invoice.pdf` kommt mit den Tags **Taxes** und **2024** an, wobei ein vorhandener Tag, dessen Name ohne Beachtung der Groß-/Kleinschreibung übereinstimmt, wiederverwendet wird und noch nicht vorhandene angelegt werden. Die Extraktion fügt die Tags des Modells zu diesen hinzu, statt sie zu ersetzen. Dateien und Ordner, deren Name mit einem Punkt beginnt, Symlinks, leere Dateien und alles nicht Speicherbare werden ignoriert; eine Datei, die in den letzten 30 Sekunden geändert wurde, wartet auf den nächsten Scan, damit nichts halb Geschriebenes aufgenommen wird.

Der Tab **Ingest** (`/settings/ingest`) enthält die drei Einstellungen (Eigentümer und Intervall werden mit dem [IMAP-Import](#ingest-from-imap) geteilt):

- **Eigentümer** – das Konto, dem jedes Dokument aus dem Ordner gehört. Standard ist das zugehörige `users`-Konto des ersten Admins; jedes Konto kann gewählt werden.
- **Scannen alle** – standardmäßig 5 Minuten. Das Speichern plant den Cron-Job `dir_ingest` neu (sichtbar unter PocketBase Admin → Settings → Crons), sodass eine Änderung ohne Neustart wirkt. Akzeptiert werden die Minutenwerte, die eine Stunde teilen (1–30), und die Stundenwerte, die einen Tag teilen (1–24); alles andere wird abgelehnt, da ein Cron-Schritt die Abstände sonst ungleichmäßig verteilen würde.
- **Originaldatei nach dem Import löschen** – standardmäßig aus. Wenn aus, bleiben Dateien an Ort und Stelle, und jede wird einmal importiert: Das `ingest_files`-Verzeichnis des Kontos merkt sich jeden importierten Pfad mit Größe und Änderungszeit, sodass weder ein Neustart noch das Löschen des Dokuments eine Datei zurückbringt; eine Änderung der Datei importiert sie erneut. Eine Datei, deren Inhalt bereits in der Bibliothek liegt, wird von der Duplikatprüfung per Prüfsumme übersprungen. Dateien über der Dokumentobergrenze von 47 MB und Symlinks werden nie gelesen. Wenn an, wird eine Datei entfernt, sobald ihr Dokument existiert, und eine Datei, die sich als Duplikat herausstellt, wird ebenfalls entfernt; mit Verschlüsselung im Ruhezustand wartet das Entfernen, bis der Tresor das Dokument versiegelt hat, sodass ein hartes Beenden nicht beide Kopien verlieren kann. Eine Datei, die die Pipeline ablehnt (falscher Inhalt für ihre Endung, über der Seitenobergrenze), wird nie gelöscht; sie wird einmal protokolliert und übersprungen.

### Import per IMAP {#ingest-from-imap}

Ist `INGEST_IMAP_ENABLED` gesetzt, bietet der Tab **Ingest** zusätzlich ein Postfach an, und ein Cron-Job (`imap_ingest`, im selben Intervall wie der Ordner) liest dessen Ordner: Jeder Anhang, dessen Name eine speicherbare Endung eines gewählten Typs hat, wird zu einem Dokument desselben Kontos, über dieselben Hooks wie ein Upload. Der Nachrichtentext ist nie ein Dokument, ebenso wenig ein Bild irgendwo unterhalb eines `multipart/related`-Teils – die Logos und Symbole, die ein HTML-Text per `cid:` einbettet –, es sei denn, der Absender hat es mit `Content-Disposition: attachment` gekennzeichnet; ein Bild, das als echter Anhang gesendet wurde, zählt weiterhin. Ein Inline-Bild außerhalb von `multipart/related` wird importiert, denn so versendet iPhone Mail angehängte Fotos. E-Mails ohne einen solchen Anhang bleiben unberührt. Ein leeres Serverfeld schaltet die Funktion ab.

Gescannt werden nur E-Mails, die nach der Einrichtung des Postfachs eingegangen sind: Das Speichern eines neuen Servers, Benutzernamens oder Ordners hält diesen Zeitpunkt fest (`imap_since`, angezeigt unter den Postfachfeldern), und alles, was der Ordner bereits enthielt, bleibt unberührt, unabhängig von der Aktion nach dem Import. Ein neues Passwort oder eine neue Aktion nach dem Import behält den Zeitpunkt bei, sodass nichts übersprungen wird, was in der Zwischenzeit eingegangen ist. Ältere E-Mails werden nachträglich eingelesen: **Wartung → Postfach** nimmt zwei Tage entgegen, jeweils einschließlich, und importiert die Anhänge von allem, was dazwischen eingegangen ist (IMAP `SINCE`/`BEFORE`, nach dem Empfangsdatum des Servers). Das nachträgliche Einlesen öffnet den Ordner schreibgeschützt, verschiebt oder löscht nie eine Nachricht, rührt das Verzeichnis des Behalten-Modus nicht an und verlässt sich auf die Prüfsummenprüfung, um Anhänge zu überspringen, die bereits in der Bibliothek liegen; eine doppelte Ausführung ist daher unschädlich. Es läuft im Hintergrund und hält dieselbe Sperre wie der geplante Scan, sodass keiner von beiden startet, während der andere läuft – ein Scan mit Löschen oder Verschieben arbeitet nie auf dem Ordner, den ein nachträgliches Einlesen gerade liest. Eine fehlschlagende Nachricht wird gezählt und übergangen.

Nachrichten werden jeweils zu 200 Stück abgerufen. Anhänge innerhalb einer weitergeleiteten Nachricht (`message/rfc822`) zählen ebenfalls. Bereits mit `\Deleted` markierte Nachrichten werden ignoriert. Eine Nachricht, deren Import aus einem anderen Grund als einer Ablehnung wiederholt fehlschlägt (ein Hook-Fehler, eine volle Festplatte), wird bei den nächsten zwei Scans erneut versucht und dann mit einem Fehler im Log aufgegeben, damit sie die nachfolgenden E-Mails nicht aufhält; das nachträgliche Einlesen kann sie später importieren. Die Zeitgrenze erlaubt 10 Minuten für eine Serveruhr, die hinter der dieses Hosts zurückliegt.

Löschen und Verschieben setzen UIDPLUS (oder IMAP4rev2) voraus, um nur die Nachrichten endgültig zu entfernen, die Lemmary verarbeitet hat. Auf einem Server ohne diese Erweiterung entfernt das einzige verfügbare Expunge jede `\Deleted`-Nachricht im Ordner – auch solche, die ein E-Mail-Client markiert, aber noch nicht entfernt hat –, daher führt Lemmary es dort nie aus: Löschen lässt verarbeitete Nachrichten mit `\Deleted` markiert zurück, und Verschieben (auch ohne MOVE) kopiert sie in das Ziel und markiert das Original, für das nächste Expunge, das der eigene Client des Benutzers ausführt.

- **IMAP-Server** – `host` oder `host:port`; der Port ist standardmäßig 993 für **TLS** und 143 für **STARTTLS**. Das Zertifikat muss verifizierbar sein; IMAP im Klartext wird nicht angeboten.
- **Benutzername**, **Passwort** – das Passwort ist nur schreibbar: Die API meldet nur, ob eines gespeichert ist, und das Speichern mit leerem Feld behält es bei.
- **Ordner** – standardmäßig `INBOX`.
- **Importieren** – welche Dateitypen zu Dokumenten werden: **PDF**, **Office** (DOCX, XLSX), **Bilder** (JPG, PNG, WEBP) und **Text** (TXT, CSV); standardmäßig alle. Gespeichert als die zu überspringenden Typen (`imap_skip_types`); mindestens einer muss aktiviert bleiben. Eine Nachricht mit einem Anhang eines nicht ausgewählten Typs wird nie verschoben oder gelöscht, genau wie eine mit einem abgelehnten Anhang, sodass Löschen keine Datei mitnehmen kann, die nie importiert wurde; eingebettete Logos halten eine Nachricht nicht zurück. Das erneute Aktivieren eines Typs geht bereits gescannte E-Mails nicht noch einmal durch; ein nachträgliches Einlesen erfasst sie. Das nachträgliche Einlesen wendet denselben Filter an.
- **Nach dem Import** – **Behalten** (Standard) öffnet den Ordner schreibgeschützt und merkt sich die höchste verarbeitete UID im `ingest_files`-Verzeichnis, sodass weder ein Neustart noch das Löschen des Dokuments eine Nachricht zurückbringt; ein Server, der UIDVALIDITY zurücksetzt, beginnt von vorn, und die Prüfsummenprüfung überspringt, was bereits in der Bibliothek liegt. **Verschieben** verschiebt jede verarbeitete Nachricht in einen anderen Ordner (der bei Bedarf angelegt wird; er muss sich vom Quellordner unterscheiden). **Löschen** markiert sie mit `\Deleted` und entfernt sie endgültig. Mit Verschlüsselung im Ruhezustand wartet das Verschieben oder Löschen, bis der Tresor die Dokumente versiegelt hat.

Eine Nachricht, deren Anhang abgelehnt wird (leer, über der Obergrenze von 47 MB, falscher Inhalt für seine Endung), wird nie verschoben oder gelöscht; sie wird protokolliert und übersprungen. Jeder andere Fehler stoppt den Scan, und die Nachricht wird im nächsten Intervall erneut versucht.

## Verwaltung (Admin-Oberfläche) {#management-admin-ui}

**Verwaltung** in der Navigation (nur für Admins, unterhalb von Einstellungen) verwaltet die Konten der Instanz. Der Tab **Benutzer** listet jedes `users`-Konto auf, legt eines an (E-Mail, optionaler Name, Passwort; als verifiziert angelegt, sodass es sich sofort anmelden kann), bearbeitet E-Mail, Name oder Passwort eines normalen Kontos und löscht eines. Das Löschen eines Kontos löscht auch dessen Dokumente, Tags, Freigaben und Passkeys. Admin-Konten (`is_app_admin`) werden aufgeführt, sind hier aber schreibgeschützt; verwalten Sie sie im PocketBase-Dashboard. Die Routen sind `GET`/`POST /api/app/admin/users` und `PATCH`/`DELETE /api/app/admin/users/{id}`, nur für Admins, mit `403` bei einem Admin-Konto. Bei aktivem Tresor erhält das Passwort eines neuen Kontos wie bei jedem anderen Konto eine eigene Schlüsselhülle.

## Wartung (Admin-Oberfläche) {#maintenance-admin-ui}

**Wartung** in der Navigation (nur für Admins, neben Einstellungen) enthält Wartungsaktionen, die über die gesamte Bibliothek laufen, nicht pro Dokument:

- **Nach Duplikaten suchen** – `POST /api/app/duplicates/scan`, siehe [Duplikaterkennung](#duplicate-detection).
- **Postfach scannen** (nur mit `INGEST_IMAP_ENABLED`) – `POST /api/app/ingest/imap/scan` mit `{ "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" }` startet im Hintergrund ein nachträgliches Einlesen älterer E-Mails, siehe [Import per IMAP](#ingest-from-imap), und antwortet mit `202` samt Status; `GET` auf demselben Pfad liefert `{ "running", "from", "to", "created", "skipped", "failed", "error" }` für den letzten Lauf, den die Seite alle 3 s abfragt. `409`, solange ein Postfach-Scan läuft, `400` bei ungültigen Daten oder fehlendem Postfach.
- **Veraltete Daten bereinigen** – `POST /api/app/taxonomy/prune` löscht jeden Korrespondenten und jeden Dokumenttyp, auf den kein Dokument mehr verweist (übrig geblieben durch gelöschte Dokumente, Umbenennungen oder einen abgebrochenen Import). Dokumente werden nie verändert, Tags ebenso wenig: Diese werden von Hand angelegt, ein Tag ohne Verweis ist also einer, den sein Eigentümer noch nicht vergeben hat, und kein Überbleibsel. Die Antwort enthält trotzdem einen `tags`-Zähler, immer `0`. Das Erfassen der Verweise und das Löschen teilen sich eine Transaktion, sodass ein gleichzeitig gespeichertes Dokument entweder als Verweis zählt oder an seiner eigenen Relationsprüfung scheitert; es kann keine verwaiste ID behalten.
  Die Schaltfläche ist deaktiviert, solange ein Verarbeitungsjob `pending` oder `running` ist, damit eine Entität, die ein Job gerade anhängen will, nicht mit weggeräumt wird. Die Anzahl stammt aus der Collection-API von PocketBase (`GET /api/collections/processing_jobs/records`), wird alle 5 s abgefragt und beim Klick erneut geprüft. Diese Listenregel lautet `document.user = @request.auth.id`, daher sieht die Sperre nur Jobs zu den eigenen Dokumenten des Admins – ein laufender Upload eines anderen Benutzers blockiert die Schaltfläche nicht.
- **Suchindex neu aufbauen** – `POST /api/app/search/reindex`, siehe [Volltextsuche](#full-text-search).
- **N fehlende Dokumente einbetten** – `POST /api/app/embeddings/backfill` geht jedes Dokument durch, das noch Abschnittsvektoren braucht: das Archiv, das existierte, bevor ein Embedding-Modell zugeordnet wurde, wiederhergestellte Sicherungen (deren Dokumente überhaupt keinen Verarbeitungsjob erhalten), seit der Einbettung bearbeitete Dokumente und alles, was durch einen Wechsel von Modell oder Chunker ungültig wurde. Es antwortet sofort mit `{ "started", "running", "stats" }` und arbeitet den Rückstand im Hintergrund stapelweise bis zu 30 Minuten lang ab; `GET` auf demselben Pfad liefert `{ "running", "stats" }`, was die Fortschrittszeile alle 3 s abfragt. Der Abschnitt ist mit einem Verweis auf die Einstellungen deaktiviert, wenn kein Embedding-Modell zugeordnet ist (`409`), und die Schaltfläche ist deaktiviert, solange ein Durchlauf läuft. Ein Durchlauf und der Cron-Job `EMBEDDING_BACKFILL_BATCH` teilen sich eine Sperre, sodass sie nie dasselbe Dokument doppelt einbetten – und `EMBEDDING_BACKFILL_BATCH=0` deaktiviert nur den Cron-Job, nie diese Schaltfläche.

Einträge im Navigationsmenü, die nur für Admins sind, tragen ein Schild-Symbol (rein dekorativ – die Einträge werden ohnehin nur für Admins dargestellt).

## Ausgehende E-Mails (SMTP / `outbound_emails`) {#outbound-mail-smtp-outbound-emails}

Konfigurieren Sie SMTP unter PocketBase Admin → Settings → Mail. Ist SMTP **deaktiviert** (Standard), würde PocketBase normalerweise auf lokales `sendmail` zurückgreifen. Diese App ersetzt diesen Rückfall: Nachrichten werden stattdessen in die Collection `outbound_emails` geschrieben (Passwort-Zurücksetzung, Verifizierung, OTP, Anmeldewarnungen usw.).

Sie können sie als Superuser in PocketBase Admin durchsehen. Aktivieren Sie SMTP, wenn Sie eine echte Zustellung wünschen; die Ablage in der Datenbank entfällt, solange SMTP aktiv ist.

## Upload-Seite {#upload-page}

**Hochladen** (`/upload`) gruppiert die Wege, auf denen Dokumente in die Bibliothek gelangen, in Unterbereiche, jeder unter einer eigenen Route, sodass sich ein Bereich verlinken, als Lesezeichen speichern und mit der Zurück-Schaltfläche des Browsers erreichen lässt:

| Bereich | Route | Status |
| --- | --- | --- |
| Dateien | `/upload` (Standard) | Umgesetzt – Upload von Dateien oder ganzen Ordnern per Drag-and-drop oder Dateiauswahl, siehe den Verarbeitungsablauf unten |
| Scannen | `/upload/scan` | Umgesetzt – scannt von einem eSCL-Scanner (AirScan) im lokalen Netzwerk, siehe [Scannen im Netzwerk](/de/scanning) |
| Amazon-Bestellungen | `/upload/amazon` | Umgesetzt – importiert die Rechnungs-PDFs aus einem bei Amazon angeforderten Bestellarchiv, siehe [Import von Amazon-Bestellungen](#amazon-order-import) |
| Zip-Archiv | `/upload/zip` | Umgesetzt – importiert die Dokumente aus einem vom Benutzer selbst gepackten Zip, siehe [Import von Zip-Archiven](#zip-archive-import) |
| Dokumente aufteilen | `/upload/split` | Umgesetzt – teilt ein PDF, das mehrere zusammengefügte Dokumente enthält, in ein Dokument pro Teil auf, siehe [Dokumente aufteilen](#document-splitting) |

Der einfache Datei-Upload bleibt auf `/upload` selbst (eine Index-Route), sodass bestehende Links und der Navigationseintrag **Hochladen** weiterhin dort landen.

Ein Ordner lässt sich auf den Tab Dateien ziehen oder mit **Stattdessen einen Ordner wählen** auswählen und wird im Browser bis ganz nach unten durchlaufen: Jede darin enthaltene Datei wird als eigenes Dokument gesendet, genau so, als wäre sie von Hand ausgewählt worden. Eine Datei aus einem Ordner erhält den Namen `<parent folder>-<file>`, dieselbe Regel, die die Zip-Importe verwenden, denn ein Scanner, der pro Stapel `1.pdf` in einen Ordner schreibt, würde die Bibliothek sonst mit Dokumenten namens `1.pdf` füllen. Überbleibsel von Finder und Packprogrammen werden stillschweigend verworfen, statt als falscher Typ gemeldet zu werden – `__MACOSX/`, AppleDouble-Schattendateien `._` (die die Endung der Datei tragen, zu der sie gehören) und jede andere Datei, deren Name mit einem Punkt beginnt, dieselbe Regel, die der Zip-Import anwendet. Ein hier abgelegtes `.zip` wird nicht hochgeladen; es verweist auf den Tab Zip-Archiv, der zuerst zeigen kann, was das Archiv enthält.

### Die Seitenobergrenze {#the-page-ceiling}

Eine Grenze ist nicht konfigurierbar: **Ein Dokument darf höchstens 1000 Seiten
umfassen**, auf jeder Installation. Ein Upload darüber wird mit `limit_ocr_pages`
abgelehnt, bevor irgendein OCR-Anbieter aufgerufen wird.

Sie existiert wegen des Ziels, in das der Text fließt. Die OCR-Anbieter liefern
den gesamten Text eines Dokuments als eine einzige Zeichenkette zurück, und diese
muss in die Spalte `ocr_text` passen, die 47 Mi Zeichen fasst – dieselben 47 MB,
die das Feld `documents.file` akzeptiert, nur in Zeichen statt in Byte gezählt.
Nichts anderes begrenzt ein OCR-Ergebnis: Mistral ist der einzige Anbieter, der
ein Seitenlimit dokumentiert (1000 Seiten, woher diese Zahl stammt), und Google
Vision liest so viele Seiten, wie die Datei hat, jeweils fünf auf einmal. Die
Seitenzahl, die vor dem ersten Anbieteraufruf ermittelt wird, ist die einzige
Messung, die sagt, ob die Antwort gespeichert werden könnte – und die Ablehnung an
dieser Stelle bedeutet, dass ein zu langes Dokument nichts kostet, statt bezahlt
und dann verworfen zu werden.

Wissenswerte Folgen:

- Bei jedem PDF-Upload werden die Seiten mit `pdfinfo` gezählt; andere Dateitypen
  kosten das Lesen eines fünf Byte langen Headers. Ein PDF, dessen Seitenzahl sich
  nicht lesen lässt, zählt als eine Seite, sodass diese Obergrenze auf einem Host
  ohne poppler beim Upload nicht durchgesetzt wird – dort lehnt stattdessen der
  OCR-Schritt ein zu langes Ergebnis ab, wodurch das Dokument statt des Uploads
  fehlschlägt.
- Eine Wiederherstellung oder ein Paperless-ngx-Import, der den Text eines
  Dokuments mitbringt, umgeht die Obergrenze: Es läuft keine OCR, also gibt es
  nichts auszugeben, und ein langes Dokument, das archiviert wurde, bevor es diese
  Grenze gab, bleibt wiederherstellbar.
- DOCX und XLSX werden nicht über die Seitenzahl begrenzt – eine Tabelle hat keine
  –, daher werden sie stattdessen beim Parsen gemessen, und eine Extraktion, die
  über die Spalte hinausläuft, wird mit einem Fehler abgebrochen, statt gekürzt
  gespeichert zu werden. Bei XLSX ist das relevant: Zellen verweisen auf eine
  gemeinsame String-Tabelle, sodass der extrahierte Text nicht durch die Bytes
  begrenzt ist, in denen die Datei ankam.

### Import von Amazon-Bestellungen {#amazon-order-import}

Fordern Sie das Archiv bei Amazon unter Account → Request your data → Your Orders an; Amazon schickt einen Download-Link per E-Mail, sobald der Export bereitsteht. Das Zip enthält CSV-Berichte, Zustellfotos und – unter `Additional Data/Retail.TransactionalInvoicing.*` – die Rechnungs-PDFs. Nur die PDFs werden importiert; jeder andere Eintrag wird als ignoriert gezählt und in Ruhe gelassen.

Hochladen und Importieren sind zwei Schritte, sodass nichts angelegt wird, bevor der Benutzer gesehen hat, was das Archiv enthält:

1. `POST /api/app/import/amazon/upload` (multipart, Feld `file`) streamt das Zip nach `<data dir>/temp/zip_import/` – es wird nie im Speicher gepuffert, da echte Exporte Hunderte MB groß werden. Das Archiv wird durchsucht, jedes PDF gehasht und dann als Vorschau zurückgegeben: Gesamtzahl der Dateien, wie viele importierbar sind, wie viele Duplikate oder übergroß sind, die Anzahl ignorierter Einträge und die Liste pro Datei. Duplikate sind PDFs, deren Prüfsumme bereits unter den Dokumenten des Eigentümers existiert (`duplicate_of` nennt die vorhandene ID) oder die sich früher im selben Archiv wiederholen. Importierte Dokumente erhalten den Namen `<parent folder>-<file>`, weil Amazon die Rechnungen pro Ordner nummeriert (`1.pdf`, `2.pdf`, …).
2. `POST /api/app/import/amazon` mit `{ "upload_id": "..." }` startet den Import und gibt `202 Accepted` mit `{ "job_id", "status": "running" }` zurück. Fragen Sie `GET /api/app/import/amazon/status?job_id=...` nach `progress` (`{ done, total }`) ab, bis `status` `completed` (mit `result`) oder `failed` (mit `error`) ist. `result` zählt `imported`, `skipped_duplicates`, `skipped_oversized` und `failed`, dazu bis zu 25 Fehlermeldungen pro Datei. Jedes importierte Dokument wird als `pending` gespeichert und durchläuft so den normalen [Verarbeitungsablauf](#processing-flow) mit OCR + KI.

`DELETE /api/app/import/amazon/upload?upload_id=...` verwirft ein bereitgestelltes Archiv, das der Benutzer nicht importieren möchte. Bereitgestellte Archive verfallen nach 30 Minuten und werden beim nächsten Upload weggeräumt, einschließlich Dateien, die ein früherer Prozess hinterlassen hat – das Register der Bereitstellungen und der Jobstatus liegen im Speicher, beide gehen also bei einem Neustart verloren. Das Hochladen eines zweiten Archivs verwirft ebenfalls das vorherige des Kontos, sodass ein Konto höchstens eines zur Zeit hält. Die Bestätigung verbraucht die Upload-ID: Dasselbe Archiv kann nicht zweimal importiert werden, und pro Benutzer darf jeweils ein Import laufen (ein zweiter Start liefert `409`).

Ablehnungen kommen bereits bei der Vorschau als `400` zurück statt mitten im Import: kein lesbares Zip, keine PDFs, mehr als 5000 PDFs, ein Upload über `IMPORT_STAGING_MAX_BYTES` (standardmäßig 1 GiB) oder ein Archiv, das sich auf über 8 GiB entpackt (eine Zip-Bombe). Ein einzelnes PDF über dem Limit von 47 MB für `documents.file` ist nicht fatal – es wird in der Vorschau als `oversized` markiert und beim Import übersprungen. Ein PDF über [der Seitenobergrenze](#the-page-ceiling) wird während des Laufs pro Eintrag abgelehnt statt bereits bei der Vorschau, da sich die tatsächlichen Seitenzahlen eines Archivs nur ermitteln lassen, indem man jedes PDF darin öffnet.

### Import von Zip-Archiven {#zip-archive-import}

Dieselben zwei Schritte wie beim Amazon-Import oben, gegen `/api/app/import/zip/upload`, `/api/app/import/zip` und `/api/app/import/zip/status`, mit identischen Payloads, Limits und Ablehnungen. Es ist dieselbe Implementierung: Der einzige Unterschied zwischen den beiden Abläufen ist, welche Einträge im Archiv als Dokumente zählen; das legt die Upload-Route fest, und der bereitgestellte Upload merkt es sich anschließend.

Hier bedeutet das jeder Typ, den `documents.file` speichern kann – PDF, JPEG, PNG, WebP, reiner Text, CSV, `.docx` und `.xlsx` – statt nur PDFs. Alles andere im Archiv wird als ignoriert gezählt und in Ruhe gelassen, ebenso Verzeichniseinträge, leere Einträge und Verwaltungsdateien von Packprogrammen (`__MACOSX/`, AppleDouble-Dateien `._`). Importierte Dokumente erhalten den Namen `<parent folder>-<file>`, sodass ein Zip mit der Ausgabe eines Scanners lesbar bleibt.

Die Liste der Endungen ist ein Vorfilter, nicht das letzte Wort: PocketBase entscheidet beim Speichern anhand des Inhalts, was das Feld `file` akzeptiert, sodass ein Eintrag, dessen Endung über seinen Inhalt täuscht, dort abgelehnt und in den Fehlern des Laufs pro Datei gemeldet wird statt bereits bei der Vorschau.

Ein bereitgestelltes Archiv und ein laufender Import gibt es pro Konto nur einmal über beide Zip-Abläufe hinweg, sodass das Hochladen eines Amazon-Exports ein kurz zuvor bereitgestelltes Zip verwirft und ein zweiter Import, während einer läuft, `409` liefert. Das Wiederherstellen einer Lemmary-Sicherung ist etwas völlig anderes und befindet sich unter [`/import/archive`](#restoring); es hat eine eigene Bereitstellung und kann parallel laufen.

### Scannen im Netzwerk {#network-scanning}

**Scannen** (`/upload/scan`) scannt von einem eSCL-Gerät („AirScan“) im lokalen Netzwerk und fügt die Seiten als ein Dokument hinzu. Die Einrichtung, die zwei Wege, auf denen Scanner gefunden werden, und warum mDNS unter Docker `network_mode: host` braucht, sind unter [Scannen im Netzwerk](/de/scanning) beschrieben; die API lautet:

1. `GET /api/app/scan/discover?cidr=...` liefert `{ scanners, cidr }`. Die Erkennung ist eine mDNS-Suche nach `_uscan._tcp`/`_uscans._tcp` und ein Durchlauf über `cidr`, der `GET http://<ip>/eSCL/ScannerCapabilities` abfragt; beide laufen gleichzeitig und werden nach Adresse zusammengeführt, gemeinsam innerhalb eines Budgets von 5 Sekunden. Ein weggelassenes `cidr` wird als /24 der Client-Adresse der Anfrage abgeleitet, dem einzigen Hinweis, den eine containerisierte App auf das LAN hat, und kommt in der Antwort zurück, damit die Oberfläche sagen kann, was durchsucht wurde. Ein Bereich, der nicht privat oder größer als ein /22 ist, wird mit `400` abgelehnt.
2. `POST /api/app/scan` mit `{ "scanner", "source", "upload_id" }` liefert `202 Accepted` und `{ "job_id" }`. `source` ist `platen` (eine Seite) oder `feeder` (jedes Blatt in einem Auftrag). Eine leere `upload_id` beginnt ein neues Dokument; andernfalls werden die Seiten an dieses angehängt. Fragen Sie `GET /api/app/scan/status?job_id=...` ab, bis `completed` erreicht ist, dessen `result` das bereitgestellte Dokument ist: `upload_id`, `page_count`, `size_bytes`, `expires_at`. Pro Benutzer läuft jeweils ein Scan (ein zweiter Start liefert `409`), was auch verhindert, dass zwei Läufe in dieselbe Datei zusammenfließen.
3. `GET /api/app/scan/pdf?upload_id=...` streamt das bisherige Dokument für die Vorschau. `DELETE /api/app/scan?upload_id=...` verwirft es.
4. `POST /api/app/scan/document` mit `{ "upload_id" }` speichert es als `pending`-Dokument und liefert `{ "document_id" }`, sodass es den normalen [Verarbeitungsablauf](#processing-flow) mit OCR + KI durchläuft. Ein erneuter Scan von etwas, das bereits in der Bibliothek liegt, kommt als `400` mit `duplicate_of` zurück.

Der Scan selbst besteht aus drei Anfragen an das Gerät: `POST {base}/ScanJobs` mit einem PWG-ScanSettings-Dokument (A4, 300 dpi, RGB24, `application/pdf`), dann `GET {job}/NextDocument`, bis es mit `404` antwortet, dann `DELETE {job}` – das Löschen läuft immer, auch nach einem Fehler, denn ein Gerät, das noch einen offenen Auftrag hält, verweigert den nächsten. Die Seiten werden mit `pdfunite` zu `<data dir>/temp/scan/<upload id>.pdf` zusammengeführt, das wie jeder andere bereitgestellte Upload nach 30 Minuten verfällt.

Da die Adresse von dem jeweils angemeldeten Benutzer stammt, durchläuft jede Anfrage beim Verbindungsaufbau eine Schutzprüfung, die nur RFC1918- und IPv6-ULA-Adressen zulässt: Öffentliche Adressen, `localhost` und Link-Local `169.254.x` (der Cloud-Metadatendienst) werden abgelehnt, Weiterleitungen werden nicht verfolgt, und ein `Location`-Header, der auf einen anderen Host zeigt, wird zurückgewiesen. Das bereitgestellte Dokument ist auf die eigenen 47 MB des Felds `documents.file` begrenzt, was vor jedem Scan geprüft wird, sodass ein volles Dokument gemeldet wird, solange man noch etwas dagegen tun kann.

### Dokumente aufteilen {#document-splitting}

**Dokumente aufteilen** (`/upload/split`) nimmt ein PDF, das mehrere separate, in eine Datei gescannte Dokumente enthält, und legt pro Teil ein Dokument an. Das bereitgestellte Original wird verworfen – nur die Teile werden zu Dokumenten.

Hochladen und Aufteilen sind zwei Schritte, sodass nichts angelegt wird, bevor der Benutzer entschieden hat, wo die Schnitte liegen:

1. `POST /api/app/split/upload` (multipart, Feld `file`) streamt das PDF nach `<data dir>/temp/split_upload/<upload id>/source.pdf` und rendert daneben ein Vorschaubild pro Seite mit 900 px an der längsten Kante – größer als die 400-px-Vorschau der Dokumentkarte, denn um zu entscheiden, wo ein Dokument endet, muss man den Briefkopf einer Seite lesen können (ein einziger `pdftoppm`-Lauf; `pdftoppm` füllt seine eigene Ausgabe mit führenden Nullen auf, daher werden die Dateien in `page-<n>.png` umbenannt). Die Antwort enthält `upload_id`, `file_name`, `page_count`, `size_bytes` und `expires_at`.
2. `GET /api/app/split/page?upload_id=...&page=n` liefert ein zwischengespeichertes Vorschaubild als `image/png`. Der Endpunkt braucht das Sitzungstoken, das ein `<img src>` nicht mitführen kann, daher ruft die SPA jede Seite ab und verpackt sie in eine Blob-URL.
3. `POST /api/app/split` mit `{ "upload_id": "...", "parts": [{ "from": 1, "to": 2 }, …] }` startet die Aufteilung und gibt `202 Accepted` mit `{ "job_id", "status": "running" }` zurück. Fragen Sie `GET /api/app/split/status?job_id=...` nach `progress` (`{ done, total }`) ab, bis `status` `completed` (mit `result`) oder `failed` (mit `error`) ist. `result` zählt `created`, `skipped_duplicates`, `skipped_oversized` und `failed`, dazu bis zu 25 Fehlermeldungen pro Teil und die angelegten `document_ids`. Jeder Teil wird als `pending` gespeichert und durchläuft so den normalen [Verarbeitungsablauf](#processing-flow) mit OCR + KI.

`parts` muss jede Seite genau einmal und in Reihenfolge abdecken – mehr kann die Oberfläche zum Setzen der Schnitte nicht ausdrücken, daher kommt eine Lücke, eine Überlappung, eine unsortierte Liste oder ein Bereich außerhalb der Datei als `400` mit einer Meldung zurück, die die Seite nennt, an der es schiefging. Eine abgelehnte Anfrage lässt den Upload bereitgestellt, sodass eine korrigierte Anfrage folgen kann.

Teile werden nach den enthaltenen Seiten benannt (`scan-page-1.pdf`, `scan-pages-2-5.pdf`), ausgehend von einer bereinigten Form des hochgeladenen Dateinamens. `pdfseparate` und `pdfunite` kopieren die ursprünglichen Seitenobjekte, statt sie neu zu rastern, sodass Textebene und Bildqualität erhalten bleiben; beide schreiben eine zufällige Trailer-`/ID` in ihre Ausgabe, die auf einen festen Wert umgeschrieben wird, damit das zweimalige Extrahieren derselben Seiten dieselben Bytes erzeugt. Ohne das könnte die Prüfung auf exakte Duplikate einen erneut aufgeteilten Teil nie erkennen, und das zweimalige Aufteilen desselben Scans würde stillschweigend eine zweite Kopie von allem anlegen. Das Umschreiben geschieht nur, wenn die `/ID` hinter dem von `startxref` genannten Offset liegt (sodass sich kein Querverweis-Offset verschieben kann), und wird rückgängig gemacht, wenn sich das Ergebnis nicht öffnen lässt.

`DELETE /api/app/split/upload?upload_id=...` verwirft ein bereitgestelltes PDF, das der Benutzer nicht aufteilen möchte. Bereitgestellte Uploads verfallen nach 30 Minuten und werden beim nächsten Upload weggeräumt, einschließlich Verzeichnissen, die ein früherer Prozess hinterlassen hat – das Register der Bereitstellungen und der Jobstatus liegen im Speicher, beide gehen also bei einem Neustart verloren. Die Bestätigung verbraucht die Upload-ID: Dasselbe PDF kann nicht zweimal aufgeteilt werden, und pro Benutzer darf jeweils eine Aufteilung laufen (ein zweiter Start liefert `409`).

Ablehnungen kommen bereits beim Upload als `400` zurück: kein lesbares PDF (es entscheiden der Header `%PDF-` und `pdfinfo`, nicht der angegebene Content-Type), ein einseitiges PDF (nichts aufzuteilen), mehr als 100 Seiten oder ein Upload über 100 MiB. Ein Teil über dem Limit von 47 MB für `documents.file` ist nicht fatal – er wird als `skipped_oversized` gezählt.

#### Automatische Erkennung {#automatic-detection}

`POST /api/app/split/detect` mit `{ "upload_id": "..." }` schlägt die Schnitte vor und gibt `202 Accepted` mit einer `job_id` zurück; fragen Sie `GET /api/app/split/detect/status?job_id=...` auf dieselbe Weise ab. `result` ist `{ "parts": [{ "from", "to", "title" }], "text_source" }`. Die Erkennung verbraucht den Upload nicht: Sie lässt sich wiederholen, und der Benutzer bestätigt die Aufteilung weiterhin selbst.

Der Seitentext stammt zunächst seitenweise aus `pdftotext` (`text_source: "pdf"`). Eine Seite gilt ab 16 Zeichen als mit Textebene versehen; erreicht weniger als die Hälfte der Seiten diese Schwelle, wird die Datei als Scan behandelt, und stattdessen liest der konfigurierte OCR-Anbieter jede Seite (`text_source: "ocr"`) – pro Seite gezählt statt gemittelt, da sonst ein einziges digital erzeugtes Deckblatt vor dreißig gescannten Seiten einen Durchschnitt über jede Schwelle heben würde. Der OCR-Rückfall extrahiert jede Seite in ein eigenes PDF und ist auf 40 Seiten begrenzt; darüber schlägt der Job mit einer Meldung fehl, die den Benutzer auffordert, die Schnitte von Hand zu setzen. Die Erkennung braucht ein Extraktionsmodell (andernfalls `400`) und bei einem Scan einen OCR-Anbieter.

Die Seitentexte gehen in einer einzigen Anfrage an Extraktionsanbieter und -modell, die `{"parts":[{"from","to","title"}]}` anfordert, mit einem Zeichenbudget pro Seite von `max(200, 30000 / pages)`, sodass selbst eine Datei mit 100 Seiten vollständig ankommt, statt auf ihre ersten Seiten gekürzt zu werden. Die Antwort wird dann serverseitig zu einer lückenlosen Abdeckung aller Seiten normalisiert: Nur die daraus abgeleiteten Schnittpositionen werden behalten und die Teile daraus neu gebildet, sodass auch ein unsortierter, lückenhafter, überlappender oder über den Bereich hinausgehender Vorschlag noch etwas ergibt, das `POST /api/app/split` akzeptiert, und ein unbrauchbarer auf einen einzigen Teil mit der ganzen Datei zurückfällt.

## Sicherung und Wiederherstellung {#backup-and-restore}

Jeder angemeldete Benutzer kann seine gesamte Bibliothek als ein Zip herunterladen und wiederherstellen – in diese oder eine andere Instanz. Eine vollständige Sicherung gilt pro Benutzer: Sie enthält die Dokumente und die Taxonomie des Aufrufers, nie die von jemand anderem, und nie die Einstellungen oder API-Schlüssel der Instanz. Gespeicherte KI-Chats sind nicht enthalten: Ein Export enthält das Archiv, nicht die Unterhaltungen darüber.

### Exportieren {#exporting}

**Mehr → Export → Sicherung herunterladen** oder `POST /api/app/documents/export` ohne Body. Die Antwort streamt ein Zip namens `lemmary-export.zip`.

Um nur einen Teil der Bibliothek zu exportieren, filtern Sie die Dokumentliste und verwenden Sie das Download-Symbol neben **KI-gestützte Suche**. Das sendet `{"ids": [...]}`, und das Archiv enthält nur die Dokumente, die der Aufrufer lesen darf, einschließlich geteilter, sowie nur die Tags, Korrespondenten und Dokumenttypen, die diese tragen.

Jeder Eintrag liegt flach unter `lemmary-export/`, sodass sich das Archiv von Hand durchsehen lässt:

```text
lemmary-export/manifest.json
lemmary-export/<date> [<id>] <title><ext>              the original upload
lemmary-export/<date> [<id>] <title>.ocr.txt           extracted text (omitted when empty)
lemmary-export/<date> [<id>] <title>.metadata.json     titles, tags, dates, checksum, timestamps
lemmary-export/<date> [<id>] <title>.preview.png       generated thumbnail (omitted when there is none)
```

`<date>` ist das eigene Datum des Dokuments als `YYYY-MM-DD`, sodass eine Sortierung der Dateien nach Namen sie nach Datum sortiert. Bei einem Dokument ohne Datum entfällt es, und dessen Einträge werden nach den datierten einsortiert. Archive aus der Zeit vor dem Datumspräfix werden auf dieselbe Weise wiederhergestellt. `<title>` wird bereinigt und gekürzt, sodass der längste Name unter der Grenze von 255 Byte bleibt, die Dateisysteme einem Pfadelement setzen. Relationen werden als **Namen** geschrieben, nicht als IDs, denn IDs bedeuten in der Instanz, in die das Archiv wiederhergestellt wird, nichts.

`manifest.json` ist das Inhaltsverzeichnis: `format`, `version`, `exported_at`, `document_count`, die vollständige `taxonomy` und die exakten Eintragspfade jedes Dokuments. Zwei Dinge hängen davon ab:

- **Tags, Korrespondenten und Dokumenttypen, auf die kein Dokument verweist.** Sie existieren sonst nirgends im Archiv, ohne das Manifest würde eine Wiederherstellung sie also verlieren.
- **Eindeutige Zuordnung von Begleitdateien.** Ein Dokument, dessen eigene Datei ein `.txt` ist, das wie eine OCR-Begleitdatei benannt ist, lässt sich allein am Namen nicht von einer solchen unterscheiden.

Ein Dokument, dessen gespeicherte Datei im Speicher fehlt, wird übersprungen und im Manifest weggelassen, sodass das Manifest nie etwas behauptet, das das Archiv nicht enthält.

### Wiederherstellen {#restoring}

**Mehr → Import** (`/import`) oder die API unten. Das Archiv wird nach `<data dir>/temp/archive_import/` gestreamt – nie im Speicher gepuffert –, dann durchsucht und in einer Vorschau angezeigt: wie viele Dokumente es enthält, wie viele neu sind, wie viele Duplikate, übergroß oder fehlend sind und wie viel Taxonomie mitkommt. Nichts wird angelegt, bis Sie bestätigen.

Zwei Modi:

- **Archiv so wiederherstellen, wie es war** (`restore`, der Standard): stellt Titel, Tags, Korrespondenten, Dokumenttypen, Daten, OCR-Text und Vorschaubilder wieder her und stellt die Taxonomie zuerst wieder her, damit auch Einträge ankommen, auf die nichts verweist. Wiederhergestellte Dokumente erhalten **überhaupt keinen Verarbeitungsjob** – alles, was eine Pipeline ableiten würde, liegt bereits im Archiv –, daher führt eine Wiederherstellung **keine OCR- oder LLM-Aufrufe** aus und kann nicht überschreiben, was sie gerade wiederhergestellt hat. Ein Dokument, für das das Archiv keine `.metadata.json` enthält, hat nichts wiederherzustellen und nimmt daher den gewöhnlichen Upload-Pfad; nur `originals`-Archive aus der Zeit vor dem Manifest enthalten solche.
- **Nur Dateien importieren und erneut verarbeiten** (`reprocess`): ignoriert jede Begleitdatei und reiht die vollständige OCR- + KI-Pipeline ein, wie bei einem neuen Upload.

Da eine Wiederherstellung die Pipeline nicht ausführt, läuft die Erkennung von Beinahe-Duplikaten über die wiederhergestellten Dokumente nicht erneut. Exakte Duplikate werden beim Anlegen weiterhin per Prüfsumme abgelehnt, und `duplicate_of` / `text_fingerprint` kommen aus dem Archiv zurück; **Wartung → Nach Duplikaten suchen** leitet die Verknüpfungen von Beinahe-Duplikaten über die gesamte Bibliothek neu ab, wenn Sie sie neu berechnen lassen möchten. Ein wiederhergestelltes Dokument behält außerdem das Vorschaubild, das das Archiv mitbrachte – ein älteres Archiv ohne `.preview.png`-Begleitdateien lässt diese Dokumente ohne Vorschaubild, bis sie erneut verarbeitet werden.

Was eine Wiederherstellung *nicht* erhält: **Dokument-IDs**. Wiederhergestellte Dokumente erhalten neue IDs, und `duplicate_of` wird auf die wiederhergestellte Kopie umgeschrieben, wenn das Original im selben Archiv liegt (andernfalls entfernt). `created` und `updated` werden nach dem Speichern zurückgeschrieben, sodass die Bibliothek in ihrer ursprünglichen Reihenfolge zurückkommt. Dokumente, deren Dateiprüfsumme bereits in Ihrer Bibliothek liegt, werden übersprungen, was die doppelte Wiederherstellung desselben Archivs unbedenklich macht.

Archive, die exportiert wurden, bevor es Manifeste gab, lassen sich weiterhin wiederherstellen: Ihre Dokumente werden allein aus den Eintragsnamen rekonstruiert. Nur verwaiste Taxonomie – und dieser eine Fall der verwechselbaren Begleitdatei – lässt sich daraus nicht wiedergewinnen.

### API {#api}

1. `POST /api/app/import/archive/upload` (multipart, Feld `file`) stellt das Zip bereit und liefert die Vorschau, einschließlich `upload_id` und `has_manifest`.
2. `DELETE /api/app/import/archive/upload?upload_id=...` verwirft ein bereitgestelltes Archiv. Bereitgestellte Archive verfallen außerdem nach 30 Minuten von selbst.
3. `POST /api/app/import/archive` mit `{ "upload_id": "...", "mode": "restore" | "reprocess" }` liefert `202 Accepted` mit `{ "job_id", "status": "running" }`.
4. `GET /api/app/import/archive/status?job_id=...`, bis `status` `completed` (mit `result`) oder `failed` (mit `error`) ist.

Der Jobstatus liegt nur für den laufenden Prozess im Speicher, und pro Benutzer darf jeweils ein Import laufen. Das Bereitstellen eines neuen Archivs verwirft das vorherige des Kontos, sodass ein Konto höchstens eines zur Zeit hält. Uploads sind durch `IMPORT_STAGING_MAX_BYTES` (standardmäßig 1 GiB) und 5000 Dokumente begrenzt, jeder Eintrag durch das Dokumentlimit von 47 MB; ein einziges Budget deckt alles ab, was Prüfung und Wiederherstellung entpacken, sodass ein Archiv, das sich weit über seine Größe hinaus entpackt, als Zip-Bombe abgelehnt wird. Ein wiederhergestelltes Dokument, das seine eigene OCR-Begleitdatei mitbringt, ist von [der Seitenobergrenze](#the-page-ceiling) ausgenommen – es braucht keine OCR, sodass ein langes Dokument, das vor Einführung dieser Grenze archiviert wurde, weiterhin wiederhergestellt wird; ein Eintrag ohne Begleitdatei nimmt den gewöhnlichen Upload-Pfad und unterliegt ihr.

Ein Archiv ohne Dokumente, aber mit nicht leerer Taxonomie ist gültig und wiederherstellbar – so sieht die Sicherung einer Bibliothek mit Tags, aber ohne Dokumente aus.

## Verarbeitungsablauf {#processing-flow}

1. Der Benutzer lädt ein Dokument über `/upload` hoch
2. PocketBase speichert die Datei und legt über einen Go-Hook einen `processing_jobs`-Datensatz an
3. Ein `OnRecordAfterCreateSuccess`-Hook startet den Job sofort; ein Cron-Job (`process_pending_jobs`) sammelt hängengebliebene wartende Jobs ein
4. Der Worker erzeugt aus der ersten PDF-Seite eine PNG-Vorschau (über `pdftoppm`), extrahiert dann den Text, prüft optional auf Beinahe-Duplikate und führt die KI-Extraktion der Metadaten aus
5. Die extrahierten Metadaten werden am Dokument gespeichert
6. Die Oberfläche zeigt den Status auf Listen- und Detailseiten, und alles, was auf einen Menschen wartet, erscheint im [Prüf-Posteingang](#review-inbox)

Die Metadatenextraktion sendet den OCR-Text des aktuellen Dokuments **und** bis zu 500 der vorhandenen Korrespondenten- und Dokumenttypnamen dieses Eigentümers an den konfigurierten LLM-Anbieter, damit das Modell vorhandene Bezeichnungen wiederverwenden kann, statt Beinahe-Duplikate anzulegen. Die Namen werden als JSON-Array gesendet, das als nicht vertrauenswürdige Daten gekennzeichnet ist. Die Übernahme gleicht weiterhin exakte Namen ab, danach eine Form ohne Beachtung von Satz- und Akzentzeichen (`Amazon EU S.à r.l.` vs. `Amazon EU S.a.r.l.`). Ein vorhandener Name wird bei der Wiederverwendung nicht überschrieben.

### Eigene Felder {#custom-fields}

Ein Admin legt zusätzliche Dokumentfelder unter Einstellungen → **Felder** an: einen Namen, einen Typ (Text, Zahl, Datum oder Auswahl) und optional einen Hinweis für die Extraktion wie *steht neben „Rechnungsnr.“*. Name und Hinweis stehen im Prompt, mit dem das Extraktionsmodell jedes Dokument liest; im Hinweis sagen Sie also, was das Feld bedeutet und wo es zu finden ist. Die Felder gelten für die ganze Instanz, und jede Dokumentseite zeigt sie an; wer ein geteiltes Dokument liest, sieht die Werte schreibgeschützt. Sie sind Datensätze der Collection `custom_fields`, die jeder angemeldete Benutzer lesen und nur ein Admin ändern kann.

**Vorlagen** unter der Liste legen mit einem Klick fertige Felder für eine Art von Unterlagen an: Rechnungen, Buchhaltung im Unternehmen (Rechnungsart, Nettobetrag, USt., Skonto), Angebote und Lieferscheine, Verträge, Miete, Versicherungen, Garantie, Steuern und Behörden, Rechtsstreit (Gericht, Gegenpartei, Verhandlungstermin, Streitwert), Vollmachten und Notarurkunden, Lohn und Gehalt sowie Kontoauszüge. Jede Vorlage mit Beträgen legt auch *Währung* an, als dreistelligen ISO-Code. Eine Vorlage legt ihre Felder in der aktuellen Sprache der Oberfläche an, jedes mit Hinweis, und überspringt ein Feld, das es unter demselben Namen schon gibt; Vorlagen mit einem gemeinsamen Feld wie *Betrag* lassen sich also zusammen anlegen. Was sie anlegt, ist ein gewöhnliches Feld: umbenennen, den Hinweis ändern (bei *Rechnungsart* gehört dort der eigene Firmenname hin) oder löschen.

Die Extraktion fragt das Modell nach jedem Feld anhand seines Namens und **füllt nur die leeren**: Ein eingetippter Wert übersteht jede erneute Verarbeitung. Um sie zu ersetzen, setzen Sie beim erneuten Verarbeiten eines Dokuments unter *Metadaten übernehmen* das Häkchen bei **Eigene Felder überschreiben**: Jedes Feld, für das die Extraktion einen Wert findet, wird überschrieben, die übrigen behalten ihren. Eine Zahl kommt als einfache Zahl zurück, ein Datum als `YYYY-MM-DD`, eine Auswahl als einer der Werte des Felds, und eine Antwort, die nicht zum Typ des Felds passt, wird verworfen. Ohne angelegte Felder ist der Extraktions-Prompt genau derselbe wie zuvor.

Ein **Auswahl**-Feld nimmt einen Wert aus einer festen Liste, die der Admin in den Einstellungen des Felds pflegt. Der Prompt nennt die Werte, die Extraktion darf nur einen davon wählen, und die Dokumentseite bietet sie als Auswahlliste an. Jeder Wert ist ein Datensatz von `custom_field_choices`, den jeder angemeldete Benutzer lesen und nur ein Admin ändern kann. Ein Anlegen oder Ändern in `custom_fields` kann die ganze Liste als `choices` mitschicken, `[{"id", "name"}]` in Reihenfolge, ohne `id` für einen neuen Wert; der Server speichert sie mit dem Feld in einer Transaktion und löscht die weggelassenen Werte – so speichert der Einstellungsdialog. Ein Dokumentwert verweist auf seinen Auswahlwert: Wird dieser umbenannt, heißt er auf jedem Dokument neu, und wird er entfernt, verlieren die Dokumente, die ihn trugen, diesen Wert.

Jeder Wert ist eine Zeile in `custom_field_values` (Dokument, Feld und eine Spalte `text`, `number`, `date`, `option` oder `choice`), ein umbenanntes Feld behält seine Werte also, und der Typ eines Felds steht fest, sobald es gespeichert ist. Wird ein Feld entfernt, werden seine Werte gelöscht. Korrespondent und Dokumenttyp sind zwei vordefinierte Felder derselben Art: Ihre Werte verweisen auf die eigene Namensliste des Eigentümers in `custom_field_options`, die Extraktion und Dokumentseite ergänzen und wiederverwenden und die die paperless-ngx-API als ihre Korrespondenten und Dokumenttypen ausliefert. Die Werte sind Teil der [Volltextsuche](#full-text-search). Ein [Export](#exporting) schreibt sie nach Feldnamen, und ein Import stellt einen Wert nur auf einem Feld wieder her, das diese Instanz unter demselben Namen angelegt hat – legen Sie die Felder also vor dem Import an.

### Prüf-Posteingang {#review-inbox}

**Posteingang** in der Kopfzeile ist die Liste der Dokumente mit dem Status `needs_review` – alles, was auf Sie wartet statt auf den Worker. Er trägt einen Zähler, sodass ein Blick auf die Kopfzeile zeigt, ob etwas zu tun ist.

Ein Dokument landet aus einem von drei Gründen dort:

- **Geringe Extraktionssicherheit** – das Modell hat seine eigene Antwort unter 0,5 bewertet.
- **Ein mögliches Duplikat** – siehe [Duplikaterkennung](#duplicate-detection) unten; Karte und Detailseite verlinken auf das Dokument, das es möglicherweise dupliziert.
- **Weil Sie es für alle verlangt haben** – Einstellungen → **Prüfung für neue Dokumente immer verlangen** (bei einer neuen Instanz an; eine aktualisierte behält ihre Einstellung). Ist sie an, wartet jedes Dokument, für das die KI Metadaten extrahiert hat, im Posteingang, egal wie sicher die Extraktion war, erneut verarbeitete Dokumente eingeschlossen: Nichts erreicht `completed`, außer Sie sagen es. Das ist die Einstellung für den Arbeitsablauf, Dinge hochzuladen, wie sie eintreffen, und die Korrekturen eines ganzen Monats in einer Sitzung zu erledigen.

  Sie gilt nicht für paperless-ngx-Importe im Beibehalten-Modus. Diese führen keine KI-Extraktion aus – die Metadaten sind die in paperless gepflegten –, daher gibt es für eine Prüfung nichts zu kontrollieren, und ein migriertes Archiv wird nicht in den Posteingang gekippt.

  Das Einschalten ordnet auch die beiden Listen darum herum neu, weil **Dokumente** sonst größtenteils eine zweite Kopie des Posteingangs wäre:

  - **Dokumente** zeigt standardmäßig **Abgeschlossen** – das Archiv, das Sie tatsächlich gelesen haben. Das Status-Dropdown bietet weiterhin *Alle Status* an, und dessen Auswahl setzt `?status=all` in die URL.
  - Ein Upload mehrerer Dateien, ein Amazon-Import und eine Aufteilung enden alle im **Posteingang** statt unter **Dokumente**, das genau das herausfiltern würde, was gerade hinzugefügt wurde. Ein Upload einer einzelnen Datei öffnet weiterhin dieses Dokument.

  Die Instanz gibt die Einstellung über `GET /api/app/meta` bekannt, damit die SPA beides für jeden Benutzer tun kann, nicht nur für Admins; ändern kann sie nur ein Admin.

Zwei Wege hinaus, die beide den Status auf `completed` setzen:

- **Korrekturen speichern** auf der Detailseite – ein falsch gelesenes `document_date` zu korrigieren und zu speichern zählt als Prüfung.
- **Als geprüft markieren**, wenn die Metadaten bereits stimmen – auf der Detailseite, auf einer Karte in der Liste oder für eine Auswahl von Karten auf einmal aus dem Posteingang. Es schreibt nichts außer dem Status, sodass `metadata_source` weiterhin festhält, dass das Modell die Metadaten geschrieben hat.

Ist **Prüfung immer verlangen** an, bittet die Extraktion das Modell außerdem um bis zu drei **vorgeschlagene Tags**: neue Namen, die noch nicht in Ihrem Vokabular stehen. Sie erscheinen als gestrichelte `+ name`-Chips auf der Karte und der Detailseite, solange das Dokument wartet, und das Annehmen eines Vorschlags legt den Tag an und fügt ihn dem Dokument hinzu. Vorschläge werden am Verarbeitungsjob gespeichert, nie am Dokument, und verschwinden, sobald es geprüft ist. Ansonsten legt die KI nie Tags an.

Ein Dokument, das als geprüft markiert wird, während `duplicate_of` gesetzt ist, behält diese Verknüpfung: Die Beziehung stimmt weiterhin, und die Markierung als geprüft besagt, dass Sie nachgesehen und beide behalten haben.

### Duplikaterkennung {#duplicate-detection}

- **Exakte Duplikate** – beim Anlegen wird die hochgeladene Datei gehasht (SHA-256) und in `documents.checksum` gespeichert. Ein zweiter Upload mit derselben Prüfsumme für denselben Benutzer wird **abgelehnt**, mit einem Fehler, der auf die ID des vorhandenen Dokuments verweist. Die Eindeutigkeit wird über einen eindeutigen Index pro Benutzer auf nicht leeren Prüfsummen durchgesetzt, sodass nicht zwei gleichzeitige Uploads beide erfolgreich sein können.
- **Beinahe-Duplikate (optional)** – nach der OCR kann ein Schritt `detect_duplicates` normalisierten OCR-Text vergleichen (SimHash + Jaccard). Gesteuert wird das über Einstellungen → **Erkennung von Beinahe-Duplikaten nach der OCR aktivieren** (standardmäßig aus). Treffer werden als `needs_review` markiert, wobei `duplicate_of` auf das frühere Dokument gesetzt wird (nie auf ein neueres), sodass sie im [Posteingang](#review-inbox) erscheinen; die KI-Schritte Extraktion/Übernahme werden übersprungen.
- **Massenscan** – Wartung → **Nach Duplikaten suchen** (Admin) ergänzt fehlende Prüfsummen/Fingerabdrücke und markiert exakte (und, falls aktiviert, Beinahe-)Duplikate unter den vorhandenen Dokumenten.

Textextraktion:

- **PDF und Bilder** – konfigurierter OCR-Anbieter (Google Vision, Mistral Document OCR, ein OpenAI-/OpenRouter-Modell, das Dateien/Bilder akzeptiert, oder der [lokale Docling-Sidecar](/de/local_ocr))
- **TXT, CSV, DOCX, XLSX** – native Parser (kein Aufruf einer OCR-API); die Vorschau entfällt bei diesen Formaten. Ein DOCX oder XLSX, dessen Text über das hinausgeht, was `ocr_text` fassen kann, lässt das Dokument fehlschlagen, statt gekürzt gespeichert zu werden; siehe [die Seitenobergrenze](#the-page-ceiling)

Cron-Jobs sind in PocketBase Admin → Settings → Crons sichtbar und lassen sich dort manuell auslösen.

## Volltextsuche {#full-text-search}

Die Archivsuche verwendet einen invertierten Index von [Bleve](https://github.com/blevesearch/bleve) (nicht SQLite `LIKE`). Der Index liegt unter `{dataDir}/bleve/documents` (Docker: `/app/pb_data/bleve/documents` auf dem vorhandenen Volume `app_data`). Er ist abgeleitete Daten: Das Leeren von `pb_data` leert auch den Index, und der nächste Start baut ihn aus den Dokumenten neu auf.

Verhalten von Suchanfragen:

- Begriffe werden per **UND** verknüpft (alle müssen passen) und mit **BM25** gerankt. `"Phrasen"` in Anführungszeichen müssen in dieser Reihenfolge vorkommen, und eine Suchfeld-Anfrage mit einer solchen Phrase fügt nichts über die Bedeutung hinzu: Anführungszeichen verlangen genau diese Wörter. **Dokumente finden** auf der Tags-Seite sucht so nach dem Tag-Namen.
- **Passt nichts exakt, werden Begriffe mit drei oder mehr Buchstaben erneut als Präfixe versucht** – `amaz` findet *Amazon*, während Sie noch tippen, `Rechnung` findet *Rechnungsnummer*. Exakte Treffer und Präfixtreffer vermischen sich nie: Der erneute Versuch läuft nur, wenn die Suche nach ganzen Wörtern leer zurückkam, sodass ein Dokument, das das Wort tatsächlich enthält, nie von einem verdrängt wird, das nur damit beginnt. Begriffe mit einer Ziffer sind ausgenommen (`202` wäre Präfix jedes Jahres im Archiv), und es ist ein Präfix, keine Teilzeichenkette – `mazon` findet weiterhin nichts.
- Die Suche umfasst Titel, Zusammenfassung, OCR-Text, Namen von Tags/Typen/Korrespondenten, `people_or_organizations` sowie die Werte [eigener Felder](#custom-fields).
- Das Suchfeld auf der Startseite ruft `GET /api/app/documents/search` auf, sobald drei Zeichen eingegeben sind – darunter weist es darauf hin und lässt die Liste ungefiltert, da ein Präfix aus einem oder zwei Buchstaben auf den Großteil des Archivs passen würde. Ein leeres Suchfeld listet weiterhin über PocketBase (sortiert nach Erstellung).
- Das Tool `search_documents` hinter beiden [Suchseiten](/de/deep_research) und paperless-ngx `GET /api/documents/?query=` verwenden denselben Index. `read_documents` von Deep Research liest `ocr_text` direkt aus der Datenbank, nicht aus dem Index.
- **Die Suchen des Agents lockern dieses UND, das Suchfeld nicht** (mit einem Embedding-Modell ergänzt es bedeutungsnahe Dokumente, siehe unten). Die Prompts bitten das Modell, eine Frage in Schlüsselwörter zu zerlegen, und jedes davon zu verlangen lieferte nichts bei Archiven, die ein Dokument pro Schlüsselwort enthielten. Daher verlangt `search_documents` die meisten Begriffe (alle bei 2, n−1 bis 5, danach 70 %), und wenn *das* überhaupt nichts findet, versucht es erneut mit einem beliebigen davon – mit einer Änderung Spielraum bei Wörtern ab fünf Buchstaben ohne Ziffern – und begrenzt den erneuten Versuch auf 10 Treffer. Eine Phrase in Anführungszeichen bleibt bei beiden Versuchen Pflicht. Das Suchfeld behält das strikte UND absichtlich bei: Dort ist die Anfrage ein Filter über Dokumente, die der Benutzer kennt, und ein Treffer, bei dem ein Wort weggefallen ist, wirkt wie ein Fehler.
- Collection-Filter von PocketBase (`field ~ "..."`) stehen API-Clients weiterhin zur Verfügung; die Oberfläche verwendet sie für das Suchfeld nicht mehr.

Ist ein Embedding-Modell zugeordnet, gibt es daneben einen zweiten Index unter
`{dataDir}/bleve/chunks`: einen Eintrag pro eingebettetem Textabschnitt, mit dem
Text des Abschnitts und seinem Vektor. Der Such-Agent fragt ihn ab, und das
Suchfeld ebenso: Dort wird jeder strikte Schlüsselworttreffer zusammen mit bis zu
20 Dokumenten gerankt, deren Abschnitte sich der Bedeutung nach vom Rest abheben
(Reciprocal Rank Fusion, wobei die Filter der Liste auf beides angewendet
werden), sodass eine Umschreibung oder ein Wort in einer anderen Sprache ihr
Dokument trotzdem findet. Die Bedeutung ordnet die Schlüsselworttreffer um,
überholt sie aber nie: Dokumente, die nur über die Bedeutung gefunden wurden,
folgen nach dem letzten, das die Wörter enthält. Ein nächster Nachbar allein
genügt nicht: Ein Dokument zählt, wenn seine Ähnlichkeit das mittlere Dokument
der Anfrage (Median) um 15 % des Spielraums oberhalb dieses Medians übertrifft
oder, bei `bge-m3`, wenn sie 0,48 erreicht – das einzige Modell mit einer
gemessenen Untergrenze und die einzige Möglichkeit, wie „invoice“ ein Archiv
deutscher Rechnungen findet, die alle ähnlich bewertet werden. Eine Anfrage ohne
Bezug listet nichts. Jede Anfrage im Suchfeld kostet eine Embedding-Anfrage. Der
Index ist wie der erste abgeleitete Daten – aus `data.db` ohne Aufrufe beim
Embedding-Anbieter neu aufgebaut – und nach Modell *und* Anzahl der Dimensionen
versioniert, sodass eine Änderung von beidem nur diesen Index leert und neu
befüllt, während die Schlüsselwortsuche weiter bedient. Das Entfernen der
Embedding-Zuordnung löscht das Verzeichnis.

Admins können über **Wartung → Suchindex neu aufbauen** (`POST /api/app/search/reindex`) einen Neuaufbau erzwingen. Er baut beide Indizes neu auf.

## Deep Research {#deep-research}

Beide Suchseiten, wie sie Dokumente finden und was eine breite Frage kostet,
stehen auf einer eigenen Seite: [Deep Research](/de/deep_research).

## Chat-Sitzungen {#chat-sessions}

Die beiden Suchseiten (`/rag/search`, `/rag/research`) und die Seite **KI fragen** eines Dokuments (`/document/<id>/ask`) speichern ihre Unterhaltungen. Jede Seite listet frühere Chats in einer Seitenleiste auf, gibt dem geöffneten Chat eine eigene URL (`/rag/search/<chatId>`, `/rag/research/<chatId>`, `/document/<id>/ask/<chatId>`) und erlaubt, einen Chat umzubenennen oder zu löschen. Eine Seitenleiste deckt beide ab: Ein Chat wird auf beiden Seiten aufgeführt und öffnet sich auf dem Pfad, auf dem er lief.

Der Server verwaltet den Verlauf. Eine Anfrage enthält eine Sitzungs-ID und eine neue Nachricht – `POST /api/app/search` mit `{"session_id": "...", "content": "...", "mode": "search|research"}`, `POST /api/app/documents/<id>/chat` mit `{"session_id": "...", "content": "..."}` –, und der Verlauf wird aus der Datenbank gelesen, statt vom Browser erneut eingespielt zu werden. Eine weggelassene `session_id` beginnt einen neuen Chat, benannt nach seiner ersten Nachricht.

`POST /api/app/search/stream` nimmt denselben Body entgegen und speichert auf dieselbe Weise; da die Statuszeile mit dem ersten Schritt-Event hinausgeht, kommt die gespeicherte Runde als `saved`-Event an, das den Stream abschließt, statt als Antwort-Body. Beide laufen darüber – Deep Research für seine Schritt-Events, die KI-gestützte Suche für den Heartbeat darunter, denn eine Antwort, die nichts schreibt, bis die Antwort fertig ist, ist für einen Proxy mit Lese-Timeout nicht von einem hängenden Backend zu unterscheiden. Das erneute Öffnen eines Such-Chats stellt die Seite wieder her, auf der seine letzte Runde lief.

Ein Lauf endet nicht, wenn seine Verbindung endet. Der Verlust des Streams kostet die Live-Ansicht des Laufs, nicht den Lauf: Er wird beendet und die Runde gespeichert, sodass ein Netzwerkabbruch mitten in der Antwort den Chat wartend in der Seitenleiste zurücklässt, statt eine Antwort zu verlieren, für die beim Anbieter bereits bezahlt wurde. Ein Abbruch muss daher ausdrücklich erfolgen – senden Sie eine `run_id` mit der Anfrage und `POST /api/app/search/cancel` mit `{"run_id": "..."}`, um ihn zu stoppen. Ein nicht abgebrochener Lauf endet nach seinem eigenen Budget von 20 Minuten.

Ein Chat wird angelegt, sobald die erste Nachricht abgeschickt wird, noch bevor das Modell aufgerufen wird, sodass der gesamte Lauf innerhalb der Unterhaltung stattfindet, in der er gespeichert wird – diese ID ist auch die `x-opencode-session`, die eine OpenCode-Anfrage mitführt, sodass sich alle Runden eines Chats einen Prompt-Cache teilen. Eine erste Runde, die nie eine Antwort hervorbringt, nimmt ihren Chat wieder mit, sodass ein falsch konfigurierter Anbieter oder ein Timeout keine leeren Chats zurücklässt. Eine Überschreitung des Limits von 500 Chats wird vorab mit `409` abgelehnt statt erst, nachdem für eine Antwort bezahlt wurde. Wird eine Antwort erzeugt, lässt sich aber nicht speichern, enthält die Antwort `"saved": false`, und die Antwort wird angezeigt, ohne dem Verlauf hinzugefügt zu werden.

Gespeicherte Chats verwalten:

| Route | Zweck |
| --- | --- |
| `GET /api/app/chats` | Chats auflisten. Filter: `kind=search\|document`, `document=<id>`; seitenweise mit `page` / `perPage` |
| `GET /api/app/chats/{id}` | Ein Chat mit seinen Nachrichten |
| `PATCH /api/app/chats/{id}` | Umbenennen (`{"title": "..."}`) |
| `DELETE /api/app/chats/{id}` | Den Chat und seine Nachrichten löschen |

Die Chatliste ist nicht seitenweise aufgeteilt: Eine Anfrage liefert jeden Chat, den ein Konto haben kann, und die Seitenleiste scrollt. Das Lesen eines Verlaufs ist begrenzt, und die Begrenzung verwirft die ältesten Runden – `truncated` zeigt an, dass der Anfang verloren ging, nie das aktuelle Ende.

Grenzen:

- Eine Nachricht hat keine eigene Längenbegrenzung; der Request-Body ist auf 2 MB begrenzt. Was eine lange Frage kostet, ist das Kontextfenster des Modells, und das Eingabefeld der Recherche weist vor dem Absenden darauf hin, wenn dieses Fenster bekannt ist.
- Das Modell sieht den Verlauf vollständig, bis zu den 500 jüngsten Zeilen eines Lesevorgangs. Nichts anderes kürzt ihn: Das Kontextfenster des Anbieters ist die einzige Grenze, und eine Unterhaltung, die darüber hinauswächst, schlägt mit dem eigenen Fehler des Anbieters fehl, statt stillschweigend ihre ältesten Runden zu verlieren. Recherche-Runden melden, was sie verbraucht haben – siehe [KI-Anbieter → Der Modellkatalog](/de/ai_providers#the-model-catalogue).
- Ein **Recherche**-Chat speichert mehr als die Unterhaltung. Die Tool-Aufrufe jeder Runde und alles, was die Tools zurückgegeben haben, werden gespeichert, während sie geschehen, und mit der nächsten Frage erneut eingespielt, sodass eine Nachfrage auf dem aufbaut, was frühere Runden gefunden haben, statt dieselben Dokumente noch einmal zu lesen – und ein Lauf, der abgebrochen, abgelehnt oder durch einen Neustart unterbrochen wird, behält die bereits geleistete Arbeit. Diese Zeilen sind die Mechanik unter einer Runde, keine Nachrichten: Der Verlauf zeigt die Fragen und Antworten, der Rest ist in die Spur darunter eingeklappt. Eine Runde, die nie eine Antwort erreicht hat, sagt das und bietet an, fortzufahren. Suche und KI fragen speichern wie bisher eine Frage und eine Antwort.
- Ein Lauf zur Zeit pro Recherche-Chat. Eine zweite Frage, die gestellt wird, während eine noch bearbeitet wird, wird abgelehnt, statt in die gespeicherte Unterhaltung verschachtelt zu werden.
- Ein Konto darf 500 Chats behalten. Darüber hinaus werden neue abgelehnt, bis einige gelöscht sind; nichts wird automatisch entfernt.

Das Löschen eines Dokuments löscht dessen Chats unter KI fragen, und das Löschen eines Kontos löscht alle seine Chats. Die Collections `chat_sessions` und `chat_messages` haben keine API-Regeln, sodass sie – wie `passkey_credentials` – über `/api/collections` überhaupt nicht erreichbar sind und `/api/app/chats` der einzige Zugang ist. Das ist beabsichtigt: Ein Client, der eigene `assistant`-Nachrichten schreiben könnte, könnte Text einschleusen, den der Server dem Modell dann als echte frühere Antwort erneut vorspielen würde.

## Fehlerbehebung {#troubleshooting}

- **Hängt im Einrichtungsassistenten, OCR schlägt fehl, KI-Extraktion schlägt fehl:** siehe [KI-Anbieter → Fehlerbehebung](/de/ai_providers#troubleshooting).
- **Upload gelingt, bleibt aber wartend:** Stellen Sie sicher, dass der Backend-Server läuft; der Worker startet mit `serve`.
- **Einstellungsseite fehlt:** Melden Sie sich mit der Admin-E-Mail an (dem bei der Einrichtung / per `superuser upsert` angelegten Konto). Normale Benutzer ohne Adminrechte sehen die Einstellungen nicht.
- **Authentifizierungsfehler im Frontend:** Löschen Sie das Datenverzeichnis von PocketBase (`backend/pb_data`) und starten Sie neu, um die Collections neu anzulegen, und laden Sie dann die App neu. Dabei wird auch der Bleve-Index gelöscht (beim nächsten Start neu aufgebaut).
- **Die Suche übersieht ein Dokument:** Warten Sie, bis die Verarbeitung abgeschlossen ist, und versuchen Sie es erneut. Admins können **Wartung → Suchindex neu aufbauen** verwenden oder `backend/pb_data/bleve` löschen und neu starten.
