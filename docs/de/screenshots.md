# Screenshots {#screenshots}

Ein Rundgang durch jeden Bildschirm von Lemmary, in der Reihenfolge, in der Sie
ihnen begegnen.

Die durchgehend gezeigte Bibliothek ist ein Demo-Archiv mit erfundenen Dokumenten –
Rechnungen, Gehaltsabrechnungen, Verträge, Kontoauszüge und Quittungen, adressiert
an eine fiktive Person namens „Robin Marsh“. Alle Namen, Beträge, Adressen und
Referenznummern in diesen Bildern sind ausgedacht; die Metadaten, OCR-Texte und
Deep-Research-Antworten drumherum sind dagegen echte Ergebnisse der Pipeline, die
diese Dateien gelesen hat.

Jedes Bild auf dieser Seite öffnet sich per Klick. Die Aufnahmen sind in doppelter
Größe erstellt, sodass **Full size** im Betrachter die Oberfläche genau so zeigt,
wie sie auf dem Bildschirm erscheint – nur so ist der Text darin lesbar. Im
geöffneten Betrachter führen `←` und `→` durch den gesamten Rundgang, `z` schaltet
die Größe um und `Esc` schließt ihn.

## Erster Start {#first-launch}

Der Einrichtungsassistent in der App läuft einmalig, auf einer Instanz ohne
Admin-Konto. Er legt dieses Konto an und erfasst anschließend die OCR- und
LLM-Zugangsdaten, ohne die die Pipeline nicht starten kann.

![Einrichtungsassistent, Schritt Admin-Konto](../screenshots/setup-admin.png)

Danach zwei Schlüssel statt eines Anbieterkatalogs: Mistral liest die Dokumente
und ermöglicht die bedeutungsbasierte Suche, und ein weiterer Anbieter übernimmt
das Denken. Der Schritt verlinkt die Anleitung, mit der sich beide Konten
eröffnen lassen, für alle, die noch keine haben.

![Einrichtungsassistent, die beiden Anbieterschlüssel](../screenshots/setup-providers.png)

Sobald ein Anbieter gespeichert ist, legt der letzte Schritt fest, welches Modell
welche Aufgabe übernimmt. Der Katalog wird vom Anbieter abgerufen, die Liste
enthält also genau das, was Ihr Schlüssel tatsächlich erreichen kann.

![Einrichtungsassistent, Schritt Modellauswahl](../screenshots/setup-models.png)

## Anmelden {#signing-in}

Anmeldung per Passwort, [Passkey](/de/passkeys) und [OAuth2](/de/oauth) befinden
sich auf einem einzigen Bildschirm. Die Passkey-Schaltfläche erscheint, sobald
irgendein Konto der Instanz einen registriert hat; die Anbieter-Schaltflächen
erscheinen für alles, was in PocketBase aktiviert ist.

![Anmeldebildschirm mit Passwort, Passkey und OAuth2](../screenshots/login.png)

Jedes Konto verwaltet seine eigenen Passkeys, einen pro Gerät.

![Kontoseite mit einem registrierten Passkey](../screenshots/account.png)

## Die Dokumentliste {#the-documents-list}

Die Karten zeigen, was die KI extrahiert hat: Titel, Dokumenttyp, Korrespondent,
Zusammenfassung und Tags, dazu den Verarbeitungsstatus und das Datum des Dokuments
selbst. Die Zeitleiste am linken Rand zählt die Dokumente pro Jahr und Monat.

![Dokumentliste](../screenshots/documents.png)

Die Volltextsuche durchsucht Titel, OCR-Text, Tags und Zusammenfassungen
über einen Bleve-Index.

![Die Bibliothek durchsuchen](../screenshots/documents-search.png)

Dokumenttyp und Korrespondent sind Filter mit Autovervollständigung, die aus der
beim Extraktionsschritt angelegten Taxonomie gebildet werden; Tags grenzen dieselbe
Liste ein und lassen sich kombinieren, und die Auswahl eines Zeitraums in der
Zeitleiste setzt den Datumsbereich. Jeder Filter steht im Query-String, sodass eine
gefilterte Liste ein Neuladen übersteht und verlinkt werden kann.

![Geöffneter Dokumenttyp-Filter](../screenshots/documents-filter-type.png)

Filtert man auf fehlgeschlagene Dokumente, werden die Karten auswählbar, sodass
sich ein Stapel für einen weiteren Versuch einreihen lässt.

![Fehlgeschlagene Dokumente zur erneuten Verarbeitung ausgewählt](../screenshots/documents-failed.png)

Auf einem schmalen Bildschirm fließt alles in eine Spalte um, wobei die Links der
Kopfzeile – und die seltener besuchten Seiten hinter dem Zahnradmenü – hinter der
Menüschaltfläche gestapelt werden.

![Dokumentliste auf einem Smartphone](../screenshots/documents-mobile.png)

## Tags {#tags}

Ein Tag existiert erst, wenn Sie ihn hier anlegen. Die Verarbeitung vergibt Tags
aus dieser Liste und erfindet nie etwas Neues – genau das verhindert, dass eine
Bibliothek mit 200 Dokumenten am Ende 200 Tags hat. Einen neuen Tag auf Dokumente
anzuwenden, die bereits im Archiv liegen, bedeutet, sie erneut zu lesen; deshalb
beziffert die Seite die Kosten dieses Durchlaufs, bevor sie ihn anbietet.

![Das Tag-Vokabular](../screenshots/tags.png)

## Die Ablage und die Warteschlange {#the-tray-and-the-queue}

Ist die Prüfung immer erforderlich, wartet ein fertig verarbeitetes Dokument
darauf, gelesen zu werden, statt ungesehen in die Bibliothek aufgenommen zu
werden. Der Posteingang ist diese Ablage – wartende, noch in Verarbeitung
befindliche und fehlgeschlagene Dokumente zusammen –, und die Kopfzeile zeigt,
wie viel darin liegt.

![Der Posteingang](../screenshots/inbox.png)

Die Aktivität ist die Warteschlange selbst: was der Worker gerade tut, darunter
was noch zu erledigen ist, und alles, was am letzten Tag abgebrochen wurde oder
fehlgeschlagen ist, bleibt weiter aufgeführt.

![Die Verarbeitungswarteschlange](../screenshots/activity.png)

## Ein Dokument {#one-document}

Auf der Detailseite wird die Extraktion geprüft. Die Datei steht neben ihren
Metadaten -- ein PDF im browsereigenen Betrachter, sodass Sie Seite drei lesen
können, während Sie die zugehörigen Felder korrigieren --, und die Schaltfläche
**Vorschau** blendet diese Spalte aus, wenn die Felder die Breite brauchen. Ist
eine Ergebnissprache gesetzt, schreibt das Modell diese Felder in ihr, sodass sich
eine deutsche Rechnung auf Englisch lesen lässt, während ihr OCR-Text behält, was
tatsächlich darin stand.

![Dokumentdetails](../screenshots/document-detail.png)

**Bearbeitung entsperren** verwandelt diese Felder in ein Formular. Korrekturen
werden am Dokument gespeichert, und die Taxonomie zieht nach: Ein neuer Typ oder
Korrespondent, der hier eingegeben wird, wird angelegt und ab dann
wiederverwendet. Bei Tags ist das anders -- sie werden aus einer Liste gewählt,
die Sie unter **Tags** pflegen, daher bietet der Editor an, was existiert, statt
aus Ihrer Eingabe einen neuen anzulegen.

Jeder Durchlauf wird Schritt für Schritt protokolliert – Vorschau, OCR,
Duplikaterkennung, Extraktion, Übernahme, Einbettung –, jeweils mit dem Anbieter
und Modell, das der Schritt verwendet hat.

![Details eines Verarbeitungsjobs](../screenshots/document-processing.png)

Schlägt ein Schritt fehl, nennt das Panel, welcher es war und warum, und bietet
genau die Schritte zur erneuten Ausführung an.

![Ein fehlgeschlagenes Dokument mit Fehlermeldung und Auswahl zur erneuten Verarbeitung](../screenshots/document-failed.png)

KI fragen beantwortet Fragen zu einem einzelnen Dokument und nutzt dessen OCR-Text
als Kontext. Der Chat wird gespeichert.

![Fragen zu einem Dokument stellen](../screenshots/document-ask.png)

## Dokumente hinzufügen {#adding-documents}

PDFs, Bilder, reiner Text, CSV, Word und Excel. Textformate werden lokal gelesen
und überspringen OCR vollständig.

![Zum Hochladen bereitgestellte Dateien](../screenshots/upload-staged.png)

Ein Netzwerkscanner ist eine eigene Quelle: Die Seite spricht über das LAN per
eSCL mit dem Gerät, es wird also nichts installiert und kein Computer steht
dazwischen.

![Scannen von einem Netzwerkscanner](../screenshots/upload-scan.png)

Ein selbst gepacktes Zip-Archiv wird gelesen, bevor irgendetwas angelegt wird;
darin enthaltene Ordner bleiben als Namenspräfix erhalten.

![Vorschau eines Zip-Archivs](../screenshots/upload-zip.png)

Hat ein Scanner aus einem Stapel unzusammenhängender Papiere ein einziges PDF
erzeugt, lässt es sich wieder auftrennen – von Hand oder mit den Schnitten, die
das Modell vorschlägt.

![Ein vierseitiger Scan, in drei Dokumente aufgeteilt](../screenshots/upload-split.png)

Ein Amazon-Export „Meine Bestellungen“ wird vorab angezeigt, bevor irgendetwas
angelegt wird: wie viele Rechnungs-PDFs er enthält, wie viele davon neu sind und
wie viele seiner übrigen Dateien ignoriert werden.

![Vorschau eines Amazon-Bestellexports](../screenshots/upload-amazon.png)

## KI-Suche und Recherche {#ai-search-and-research}

Zwei Seiten, zwei Zugänge. Die **KI-gestützte Suche**, erreichbar über die
Dokumentliste, findet Dokumente und listet sie als Karten auf.

![KI-gestützte Suche mit Treffern](../screenshots/deep-search.png)

**Deep Research**, der Eintrag in der Kopfzeile, liest, was sie gefunden hat,
zählt und summiert über das Archiv hinweg und schreibt eine Antwort, die auf ihre
Quellen verlinkt.

![Eine Antwort mit Quellenangaben in Deep Research](../screenshots/deep-search-research.png)

Die unternommenen Schritte werden live eingeblendet, solange der Lauf aktiv ist,
und klappen nach dessen Ende hinter eine Zusammenfassung zusammen, sodass auch ein
langer Lauf übersichtlich bleibt.

![Aufgeklappte Recherche-Schritte](../screenshots/deep-search-steps.png)

Eine Antwort, die es wert ist, behalten zu werden, lässt sich abzweigen: Die
Abzweigung kopiert den Chat bis zu dieser Antwort und setzt ihn dort fort, während
das Original unverändert bleibt. Chats werden gespeichert, in der Seitenleiste
aufgelistet und lassen sich per URL fortsetzen.

![Ein Recherche-Chat, an einer seiner Antworten abgezweigt](../screenshots/deep-search-fork.png)

## Sicherung, Wiederherstellung und Migration {#backup-restore-and-migration}

Die gesamte Bibliothek – Originaldateien, OCR-Text, Metadaten, Vorschaubilder und
Taxonomie – wird als ein einziges Zip heruntergeladen.

![Exportseite](../screenshots/export.png)

Dieses Zip lässt sich in diese oder eine andere Instanz wiederherstellen. Es wird
zuerst in einer Vorschau angezeigt, und bereits vorhandene Dokumente werden
übersprungen, sodass eine doppelte Wiederherstellung unbedenklich ist. Eine
Wiederherstellung „wie es war“ sendet nichts an OCR oder den KI-Anbieter.

![Vorschau eines Lemmary-Archivs vor der Wiederherstellung](../screenshots/import-archive-preview.png)

Ein bestehender paperless-ngx-Server lässt sich direkt übernehmen, entweder mit
seinen Metadaten oder indem die Pipeline erneut über die Dateien läuft.

![Import aus paperless-ngx](../screenshots/import-ngx.png)

## Administration {#administration}

Die Einstellungen sind Laufzeitkonfiguration, die in der Datenbank statt in der
Umgebung gespeichert ist, aufgeteilt in je einen Tab. Unter Darstellung erhält die
Instanz ihren Namen und die Akzentfarbe, die die gesamte Oberfläche übernimmt.

![Einstellungen, Darstellung](../screenshots/settings-appearance.png)

Unter KI stehen die Anbieter und welches Modell welche Aufgabe übernimmt.
API-Schlüssel sind nur schreibbar: Die Seite meldet, dass ein Schlüssel gesetzt
ist, aber nie, wie er lautet.

![Einstellungen, der KI-Tab vollständig](../screenshots/settings.png)

Verarbeitung umfasst die Timeouts, die Sprache, in der Ergebnisse zurückkommen,
ob jedes Dokument auf eine Prüfung wartet, sowie Hausregeln, die an den
Extraktions-Prompt angehängt werden – der Ort, um festzulegen, dass „Rechnung“
einer „Invoice“ entspricht oder dass diese drei Absender dasselbe Unternehmen
sind.

![Einstellungen, Verarbeitung](../screenshots/settings-processing.png)

Die eigenen Grenzen des Workers: wie lange ein Job laufen darf und wie oft ein
fehlgeschlagener Schritt wiederholt wird.

![Einstellungen, Worker](../screenshots/settings-worker.png)

Exakte Duplikate werden beim Hochladen immer abgelehnt. Die Erkennung von
Beinahe-Duplikaten vergleicht stattdessen den OCR-Text, was einen Durchlauf über
die Bibliothek kostet; daher ist sie ausgeschaltet, bis sie angefordert wird.

![Einstellungen, Duplikate](../screenshots/settings-duplicates.png)

Bibliotheksweite Wartung: fehlgeschlagene Dokumente stapelweise erneut
verarbeiten, nach Duplikaten suchen, Taxonomie-Einträge löschen, auf die nichts
mehr verweist, den Suchindex neu aufbauen und alles einbetten, dem noch ein Vektor
fehlt.

![Wartungsseite](../screenshots/maintenance.png)

Eine Testseite, um einen Anbieter und ein Modell an einer einzelnen Datei zu
prüfen, ohne ein Dokument anzulegen.

![OCR-Testseite](../screenshots/ocr-test.png)
