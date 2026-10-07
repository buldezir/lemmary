# Lemmary vs Paperless-ngx vs Papra {#lemmary-vs-paperless-ngx-vs-papra}

Alle drei Produkte machen aus einer selbst gehosteten Dateisammlung ein
durchsuchbares Dokumentenarchiv. Lemmary geht weiter: Es ist darauf ausgelegt,
das Archiv für Sie zu organisieren und es zu einer Quelle zu machen, die Sie
befragen und erforschen können, ohne zuerst ein ausgeklügeltes Ablagesystem
aufzubauen.

- **Lemmary** ist ein KI-zentriertes persönliches Archiv. Es extrahiert
  automatisch umfangreiche Metadaten, kann Fragen zu einem Dokument beantworten
  und kann über das gesamte Archiv hinweg suchen, lesen und Erkenntnisse
  zusammenführen – mit Links zurück zu den Quellen. Agenten können dasselbe
  Archiv über MCP durchsuchen, lesen und, wo ein Admin es erlaubt, ändern.
- **Paperless-ngx** ist ein ausgereiftes, auf Scans ausgerichtetes
  Dokumentenmanagementsystem. Von den dreien hat es das umfassendste
  Ablagemodell, die meisten Automatisierungen, Dokumentbearbeitung,
  Berechtigungen und das größte Client-Ökosystem – um den Preis von mehr
  Infrastruktur und mehr manueller Organisation.
- **Papra** ist ein minimalistisches kollaboratives Archiv. Organisationen,
  Freigaben, eine öffentliche API und flexibler Speicher stehen im Mittelpunkt,
  während sich seine KI-Funktion auf automatisches Taggen konzentriert und
  nicht darauf, im Archiv zu suchen und daraus Antworten zu geben.

Dies ist ein Funktionsvergleich, kein Benchmark. „Integriert“ kann dennoch
Konfiguration, Zugangsdaten oder einen optionalen Dienst erfordern. Alle drei
Spalten wurden am **7. Oktober 2026** geprüft: Lemmary anhand dieser
Dokumentation, Paperless-ngx anhand von v3.3.0 und Papra anhand von 26.7.0 und
der verlinkten Projektdokumentation. Prüfen Sie das jeweilige Quellprojekt,
bevor Sie sich aufgrund einer einzelnen Funktion entscheiden.

## Funktionsvergleich {#feature-comparison}

| Bereich | Lemmary | Paperless-ngx | Papra |
| --- | --- | --- | --- |
| Hauptzweck | Ein Dokumentenarchiv, das sich selbst organisiert und archivweite Recherche mit Quellenangaben unterstützt | Ein klassischer Dokumentenmanagement-Workflow für gescannte und digitale Unterlagen | Einfache Dokumentenablage und Zusammenarbeit |
| Self-Hosting | Ein kompakter Standard-Footprint: ein Anwendungscontainer und ein persistentes Volume; [PocketBase unterstützt außerdem S3-kompatiblen Speicher für Dokumentdateien](/de/storage#document-files), und Sidecars für lokale OCR und Embeddings sind optional, in Docker oder [nativ auf einem Mac mit Apple Silicon](/de/local_ai_macos) | Docker Compose oder Bare Metal; benötigt zusätzlich zur App einen Redis-kompatiblen Broker und unterstützt SQLite, PostgreSQL oder MariaDB | Ein Docker-Image für eine Basisinstallation; Dateisystem-, S3-kompatibler und Azure-Blob-Speicher werden unterstützt |
| Hochladen und Aufnahme | Hochladen von Dateien, Ordnern und Zip-Archiven im Web; ein [überwachter Eingangsordner](/de/setup#ingest-folder), dessen Unterordner zu Tags werden; [Anhänge aus IMAP-Postfächern](/de/setup#ingest-from-imap); [direktes Scannen mit AirScan/eSCL-Netzwerkscannern](/de/scanning); eine teilweise Paperless-ngx-kompatible REST-API; direkte Migration aus Paperless-ngx, ein eigener Export von Amazon-Bestellungen und die Aufnahme von PDFs mit mehreren Dokumenten | Hochladen über Web/API, überwachter Eingangsordner, E-Mail-Konten und -Regeln | Hochladen über Web/API/CLI, überwachte Organisationsordner und eingehende E-Mails |
| OCR und Textextraktion | Mistral OCR, Google Cloud Vision, ein OpenAI-kompatibles oder Anthropic-Dateimodell, ein [ChatGPT-Abonnement](/de/chatgpt_login) oder ein [lokaler Docling-Sidecar](/de/local_ocr) (RapidOCR, EasyOCR, Tesseract oder Apple Vision auf einem Mac); native Extraktion für Text- und Office-Dateien; ist eine Ergebnissprache gesetzt, lässt sich der OCR-Text bei Bedarf übersetzt lesen | Lokale Tesseract-OCR in über 100 Sprachen, optional mit entfernter Azure-AI-OCR; erstellt standardmäßig nur für gescannte Dateien ein Archiv-PDF/A neben dem Original | Standardmäßig interne Extraktion mit Tesseract; Mistral OCR, Docling, Azure Document Intelligence und benutzerdefinierte HTTP-Extraktoren werden ebenfalls unterstützt |
| Automatische Organisation | Jedes verarbeitete Dokument kann vom LLM automatisch Titel, Datum, Typ, Korrespondent und eine Zusammenfassung erhalten, dazu Tags aus einer von Ihnen gepflegten Liste – das LLM weist sie zu, erfindet aber nie neue. Vom Administrator verfasste Hausregeln steuern die Extraktion; Dokumente, die zur Prüfung zurückgehalten werden, erhalten bis zu drei vorgeschlagene neue Tags zum Übernehmen; ein neuer Tag kann per KI auch Dokumenten zugewiesen werden, die verarbeitet wurden, bevor es ihn gab; mit eingeschalteter [Verknüpfung verwandter Dokumente](/de/setup#related-documents) werden Dokumente mit anderen verknüpft, die dieselbe Referenznummer tragen oder inhaltlich nahe sind | Zuordnungsregeln und ein gelernter Klassifikator weisen Tags, Korrespondenten, Typen und Speicherpfade zu; ein optionales LLM schlägt Metadaten zur Prüfung vor, oder eine Workflow-Aktion übernimmt sie automatisch und kann fehlende Tags, Korrespondenten und Typen anlegen | Regeln können Tags vergeben; ein optionales LLM kann vorhandene Tags vergeben oder neue anlegen, extrahiert aber nicht die umfassenderen Metadaten, die Lemmary extrahiert |
| Metadatenmodell | Titel, Datum, Typ, Korrespondent, farbige Tags, Zusammenfassung, bearbeitbarer OCR-Text, vom Admin angelegte [eigene Felder](/de/setup#custom-fields) für Text, Zahl, Datum und Auswahl, die die Extraktion füllt, mit Vorlagen für gängige Unterlagen per Klick, und beidseitige [Verknüpfungen zwischen verwandten Dokumenten](/de/setup#related-documents) | Tags, Korrespondenten, Dokumenttypen, Speicherpfade, Archiv-Seriennummern, Notizen und typisierte benutzerdefinierte Felder, einschließlich beidseitiger Dokumentverknüpfungen | Tags, Notizen und typisierte benutzerdefinierte Eigenschaften auf Organisationsebene, einschließlich Verweisen auf andere Dokumente |
| Konventionelle Suche | Volltextsuche über OCR-Text, Metadaten und Werte eigener Felder mit Präfixsuche, exakten Phrasen in Anführungszeichen, Filtern (einschließlich „alle diese Tags“, „keine Tags“ und „kein Datum“), einer Datumszeitleiste und einem Posteingang zur Prüfung – alles ohne Embedding-Modell verfügbar | Gewichtete Volltextsuche mit unscharfer Suche und erweiterter Abfragesyntax, ähnliche Dokumente („More like this“), Filter, gespeicherte Ansichten und ein konfigurierbares Dashboard | Volltextsuche, Abfragefilter, Suche in benutzerdefinierten Eigenschaften und gespeicherte Suchen |
| KI-Suche und Chat | Der am stärksten auf Recherche ausgerichtete Workflow: **KI-gestützte Suche** findet Dokumente per Stichwort und optional per Embeddings, und mit einem Embedding-Modell findet auch das Suchfeld der Dokumentliste Dokumente nach Bedeutung; **Deep Research** liest viele Dokumente und führt sie mit Quellenlinks zusammen, auf einem optionalen separaten erweiterten Modell, und seine Chats lassen sich verzweigen; **KI fragen** chattet mit einem Dokument. Beide können [im Web suchen](/de/ai_providers#the-web-search-provider), wenn ein Leser dies einschaltet, und ein Chat kann das Modell wechseln. Sprachmodelle laufen mit API-Schlüsseln (OpenAI, Anthropic, OpenRouter, Mistral, Opencode Go) oder einem ChatGPT-Abonnement; Embeddings können auf [Ihrer eigenen Hardware](/de/local_embeddings) laufen | Optionale Embeddings stützen die Metadatenvorschläge des LLM und einen Chat mit Quellenangaben über ein Dokument oder alle Dokumente, die ein Benutzer sehen darf, es gibt aber keinen separaten mehrstufigen Recherchemodus | Kein Archiv-Chat und keine semantische Suche dokumentiert; die aktuelle KI-Unterstützung beschränkt sich auf automatisches Taggen |
| PDF- und Versionswerkzeuge | Kann ein PDF mit mehreren Dokumenten bei der Aufnahme manuell aufteilen oder das Modell Schnittstellen vorschlagen lassen | Seiten zusammenführen, aufteilen, drehen, löschen und neu anordnen; mehrere Dateiversionen unter einem Dokument aufbewahren | Kein vergleichbarer PDF-Editor und keine Versionshistorie für Dokumente dokumentiert |
| Personen und Freigabe | Mehrere Konten und Bibliotheken pro Benutzer; ein Dokument kann schreibgeschützt für andere Konten auf derselben Instanz freigegeben werden; keine öffentlichen Dokumentlinks oder gemeinsamen Arbeitsbereiche | Benutzer, Gruppen, globale und objektbezogene Berechtigungen, Eigentümerschaft, ablaufende öffentliche Links, Bündel und Freigabe per E-Mail | Organisationen mit den Rollen Inhaber, Admin und Mitglied; Freigabelinks können ein Ablaufdatum und ein Passwort haben |
| Authentifizierung | Passwort, Anmeldung über OAuth2-Anbieter und Passkeys; keine TOTP-Zwei-Faktor-Authentifizierung | Passwort mit optionaler TOTP-Zwei-Faktor-Authentifizierung, OIDC- und Social-Login oder ein Reverse-Proxy-Header; die Passwortanmeldung lässt sich abschalten; keine Passkeys | Passwort, Anmeldung über GitHub und Google, konfigurierbare OAuth2/OIDC-Anbieter und optionale TOTP-Zwei-Faktor-Authentifizierung; keine Passkeys |
| Oberflächensprachen | Englisch, Deutsch und Russisch, für Oberfläche, API-Fehler und diese Dokumentation | Etwa 37, übersetzt auf Crowdin | 17 in der Web-App |
| Automatisierung und Integrationen | Eine vollständige asynchrone Verarbeitungspipeline für OCR und KI, ein [MCP-Server](/de/mcp), damit Agenten das Archiv durchsuchen, lesen und, wo ein Admin es erlaubt, ändern können, die kompatible Teilmenge der Paperless-ngx-API für bestehende Clients und optionale Prometheus-Metriken | Ereignisgesteuerte Workflows, E-Mail-Regeln, Webhooks, Skripte, eine umfassende REST-API und ein großes Ökosystem von Drittanbieter-Clients | Tagging-Regeln, API-Schlüssel mit Geltungsbereichen, REST-API, TypeScript-SDK, CLI und Webhooks |
| Import, Export und Wiederherstellung | Der direkteste Umzug aus Paperless-ngx – Lemmary holt die Daten über die API aus einer laufenden Instanz –, dazu ein portables Zip der vollständigen Bibliothek oder einer gefilterten Auswahl mit Originalen, OCR, Metadaten, Vorschaubildern und Taxonomie für Sicherung und Wiederherstellung | Exporter/Importer für die gesamte Instanz zur Migration und Sicherung; ausgewählte Dokumente lassen sich als Zip mit Dateien ohne Metadaten herunterladen; ein Papierkorb für 30 Tage | Ein CLI-Importer für Paperless-ngx-Exporte und Werkzeuge zur Speichermigration; kein Bibliotheksexport, daher müssen für eine Sicherung die konfigurierte Datenbank und der Dokumentspeicher getrennt gesichert werden |
| Verschlüsselung im Ruhezustand | Der umfassendste integrierte Schutz: Der optionale Tresor verschlüsselt die Datenbank, Dokumentdateien, Vorschauen und gespeicherte Vektoren; er startet gesperrt und entschlüsselt in den Arbeitsspeicher. PocketBase-S3-Dateispeicher und der Tresor sind alternative Konfigurationen, da der Tresor unverschlüsselten externen Speicher ablehnt | Keine integrierte Verschlüsselung im Ruhezustand; das Upstream-Projekt weist darauf hin, dass Dokumente im Klartext gespeichert werden, und empfiehlt einen vertrauenswürdigen Host | Optionale AES-256-GCM-Verschlüsselung der Dokumentdateien mit Schlüsselrotation und ein separater optionaler Schlüssel für die Datenbankverschlüsselung; beides ist standardmäßig aus, und mit Dateiverschlüsselung allein bleiben extrahierter Text und Metadaten im Klartext |
| Lizenz | [PolyForm Noncommercial 1.0.0](https://github.com/buldezir/lemmary/blob/main/LICENSE): source-available; kommerzielle Nutzung erfordert eine Lizenz | [GPL-3.0](https://github.com/paperless-ngx/paperless-ngx/blob/dev/LICENSE) | [AGPL-3.0](https://github.com/papra-hq/papra/blob/main/LICENSE) |

## Wechsel zwischen den Produkten {#moving-between-them}

Lemmary kann Originale und ausgewählte Metadaten direkt aus Paperless-ngx
übernehmen. Seine `/api/`-Schnittstelle implementiert außerdem eine nützliche
Teilmenge der Paperless-ngx-REST-API, sodass kompatible mobile Clients
durchsuchen und hochladen können, aber Lemmary ist **kein direkter Ersatz für
einen Paperless-ngx-Server**: Workflows, Berechtigungen, gespeicherte Ansichten,
benutzerdefinierte Felder und viele Endpunkte liegen außerhalb dieser
Kompatibilitätsschicht. Siehe [Paperless-ngx-API-Kompatibilität](/de/paperless_ngx).

Es gibt keinen direkten Importer für Papra, und Papra hat keinen
Bibliotheksexport. Laden Sie die Originaldateien aus Papra herunter und in
Lemmary hoch; rechnen Sie damit, Tags und andere Metadaten neu anlegen zu
müssen, es sei denn, Sie schreiben eine Migration, die die beiden APIs
verwendet.

## Quellen {#sources}

Die Fähigkeiten von Lemmary sind in dieser Dokumentation beschrieben,
insbesondere unter [Einrichtung](/de/setup), [KI-Anbieter](/de/ai_providers),
[Deep Research](/de/deep_research), [Self-Hosting](/de/self_hosting),
[MCP](/de/mcp), [Scannen](/de/scanning) und
[Verschlüsselung im Ruhezustand](/de/encryption).

Für die anderen Projekte stützt sich der Vergleich auf deren offizielle
Repositories und Dokumentation:

- Paperless-ngx: [Projekt und Lizenz](https://github.com/paperless-ngx/paperless-ngx),
  [Versionshinweise](https://github.com/paperless-ngx/paperless-ngx/releases),
  [Funktionsübersicht](https://docs.paperless-ngx.com/),
  [Nutzung, Aufnahme, Berechtigungen, Freigabe und Workflows](https://docs.paperless-ngx.com/usage/),
  [KI und Zuordnung](https://docs.paperless-ngx.com/advanced_usage/),
  [Konfiguration](https://docs.paperless-ngx.com/configuration/) und
  [REST-API](https://docs.paperless-ngx.com/api/).
- Papra: [Projekt und Lizenz](https://github.com/papra-hq/papra),
  [Versionshinweise](https://github.com/papra-hq/papra/releases),
  [Konfiguration und Extraktionsanbieter](https://docs.papra.app/self-hosting/configuration/),
  [KI-Auto-Tagging](https://docs.papra.app/guides/auto-tagging/),
  [Dokumentverschlüsselung](https://docs.papra.app/guides/document-encryption/) und
  [API](https://docs.papra.app/resources/api-endpoints/).
