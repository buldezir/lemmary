# Speicher {#storage}

Lemmary speichert dauerhafte Daten an drei Orten: SQLite, im Speicher für
Dokumentdateien und in SQLite-gestützten Embeddings. Die Suchindizes sind
verzichtbare Kopien, die aus diesen dauerhaften Daten erzeugt werden.

| Daten | Standardort | Mit S3-Dateispeicher |
| --- | --- | --- |
| Datenbank | `/app/pb_data/data.db` | Bleibt lokal |
| Originaldateien und Vorschaubilder | `/app/pb_data/storage/` | Im konfigurierten S3-kompatiblen Bucket gespeichert |
| Embedding-Vektoren und ihr Zustand | Tabellen in `data.db` | Bleiben lokal |
| Volltext- und Vektorindizes | `/app/pb_data/bleve/` | Bleiben lokal |

## Datenbank {#database}

PocketBase verwendet SQLite. `data.db` enthält Dokumentmetadaten, extrahierten
OCR-Text, Tags und andere Taxonomie, Benutzer, Einstellungen,
Verarbeitungsaufträge, gespeicherte Chats und Embedding-Vektoren. Sichern Sie die
Datenbank zusammen mit den Dokumentdateien; keines von beiden ist für sich
allein ein vollständiges Archiv.

## Dokumentdateien {#document-files}

Standardmäßig schreibt PocketBase hochgeladene Originale und erzeugte
Vorschaubilder unter `pb_data/storage/`. In Docker bewahrt das Volume `app_data`
dieses Verzeichnis zusammen mit der Datenbank dauerhaft auf.

PocketBase kann diese Dateien stattdessen in einem S3-kompatiblen Dienst
speichern. Melden Sie sich in der PocketBase-Admin-Oberfläche unter `/_/` an,
öffnen Sie **Settings → Files storage** und aktivieren Sie S3 mit Bucket, Region
oder Endpunkt, Zugriffsschlüssel und Secret für den Dienst. Nur Datensatzdateien
wandern nach S3: Behalten Sie das lokale `pb_data`-Volume, denn es enthält
weiterhin SQLite und die Suchindizes.

S3-Dateispeicher und [der verschlüsselte Tresor von Lemmary](/de/encryption) sind
alternative Konfigurationen. Der Tresor verweigert den Start, wenn der
PocketBase-S3-Speicher aktiviert ist, da Dateien, die außerhalb des Tresors
geschrieben werden, nicht von dessen Verschlüsselung erfasst würden.

## Embeddings {#embeddings}

Wenn ein Embedding-Modell konfiguriert ist, teilt Lemmary den OCR-Text jedes
Dokuments in Passagen auf. Die float32-Vektoren und der Embedding-Zustand werden
in einfachen SQLite-Tabellen in `data.db` gespeichert; sie gelangen nicht nach
S3. Eine Passage wird als Paar von Byte-Offsets in den OCR-Text gespeichert
statt als zweite Kopie davon.

Deep Research fragt einen abgeleiteten Bleve-Vektorindex unter
`pb_data/bleve/chunks` ab. Dieser Index kann gelöscht und aus den Vektoren in
SQLite neu aufgebaut werden, ohne den Embedding-Anbieter erneut aufzurufen. Der
gewöhnliche Volltextindex daneben, unter `pb_data/bleve/documents`, ist
ebenfalls abgeleitet und neu aufbaubar.

Der Embedding-Speicher wächst mit der Anzahl der Passagen und den
Vektordimensionen. Siehe
[was Embeddings kosten](/de/ai_providers#what-embeddings-cost) und
[Lokale Embeddings](/de/local_embeddings) zu Dimensionierung und Anbieteroptionen.
