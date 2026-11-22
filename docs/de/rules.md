# Regeln und Webhooks {#rules-and-webhooks}

Regeln sind Wenn-dann-Automatisierung für Ihre eigenen Dokumente. Jede Regel hat einen Auslöser,
Bedingungen und Aktionen: Löst der Auslöser aus und treffen alle Bedingungen zu, laufen die
Aktionen. Typische Beispiele:

- **Rechnungen** – ist der Dokumenttyp *Rechnung*, füllt die KI *Betrag*, *Fälligkeitsdatum* und
  *IBAN* mit Anweisungen, die nur für Rechnungen gelten, und der Tag *zu zahlen* kommt dazu.
- **Große Beträge** – ist der *Betrag* größer als 1000, landet das Dokument zur Prüfung im Eingang.
- **Automatisierung** – ist der Korrespondent Ihr Vermieter, wird ein Webhook von n8n, Home
  Assistant oder Zapier mit dem Dokument aufgerufen.

Regeln finden Sie unter **Mehr → Regeln**. Jeder Benutzer hat seine eigenen; eine Regel wirkt nur
auf die Dokumente ihres Besitzers. Ein Administrator kann außerdem [globale Regeln](#global)
anlegen, die für alle laufen.

---

## Auslöser {#triggers}

- **Ein Dokument wird verarbeitet** – nachdem die KI-Extraktion die Metadaten geschrieben hat,
  auch bei einer Neuverarbeitung. Dokumente, die mit ihren Metadaten importiert werden
  (Paperless-ngx, Papra), werden nicht verarbeitet; für sie löst dieser Auslöser nicht aus.
- **Ich ändere seine Tags oder Felder** – nachdem Sie in der App die Tags, den Dokumenttyp, den
  Korrespondenten oder ein anderes Feld eines Dokuments geändert haben. Eine Änderung von Titel
  oder Zusammenfassung zählt nicht. Die Regeln laufen als kurzer Auftrag für das Dokument; ein
  Dokument im Eingang bleibt im Eingang.

Eine Regel kann beide haben.

## Bedingungen {#conditions}

Alle Bedingungen einer Regel müssen zutreffen. Eine Regel ohne Bedingungen läuft für jedes Dokument.

| Feld | Vergleiche |
| --- | --- |
| Dokumenttyp, Korrespondent, Auswahlfelder | ist, ist nicht, ist ausgefüllt, ist leer |
| Tags | enthalten, nicht enthalten |
| Titel, Dokumenttext, Textfelder | enthält, enthält nicht, passt auf das Muster, ist ausgefüllt, ist leer |
| Zahlenfelder, KI-Konfidenz | ist, ist größer als, ist kleiner als, ist ausgefüllt, ist leer |
| Datumsfelder | ist, ist nach, ist vor, ist ausgefüllt, ist leer |
| KI | beantwortet mit Ja, beantwortet mit Nein – eine Frage, die Sie formulieren |

Textvergleiche ignorieren Groß- und Kleinschreibung. Ein Muster ist ein [regulärer Ausdruck in
Go-Syntax](https://github.com/google/re2/wiki/Syntax) und beachtet Groß- und Kleinschreibung, außer
es beginnt mit `(?i)`. Die KI-Konfidenz reicht von 0 bis 1; eine Extraktion unter 0,5 landet
ohnehin im Eingang.

Eine **KI**-Bedingung stellt dem Extraktionsmodell eine Ja/Nein-Frage, die Sie formulieren, etwa
*Ist das eine letzte Mahnung?*. Das Modell sieht die Metadaten des Dokuments, so wie die Regeln
darüber sie hinterlassen haben, und seinen Text, und antwortet nur dann mit Ja, wenn das Dokument
es klar belegt. Jede Frage ist eine KI-Anfrage je Dokument, darum wird sie zuletzt gestellt: erst
wenn alle anderen Bedingungen der Regel zutreffen. Stellen Sie eine günstige Bedingung daneben –
*Dokumenttyp ist Rechnung* –, und nur Rechnungen werden gefragt. Antwortet das Modell nicht, läuft
die Regel nicht, und **Regeln ausführen** zeigt den Grund.

## Aktionen {#actions}

- **Tag hinzufügen / Tag entfernen**
- **Setzen** – einen Dokumenttyp, einen Korrespondenten oder ein anderes Feld auf einen Wert.
  Die Werte von Korrespondent, Dokumenttyp und anderen Feldern mit eigenen Werten werden unter
  **Mehr → Feldoptionen** angelegt, umbenannt und zusammengeführt.
- **Zur Prüfung in den Eingang legen** – das Dokument wartet, bis jemand es als geprüft markiert.
- **Felder mit KI ausfüllen** – eine zusätzliche Anfrage an das Extraktionsmodell, die nur nach den
  angehakten Feldern fragt, mit Ihren Anweisungen. Felder, die schon einen Wert haben, behalten
  ihn. Die Felder selbst legt Ihr Administrator unter **Admin → Dokumentfelder** an.
- **Webhook aufrufen** – siehe unten.

## Reihenfolge {#order}

Regeln laufen von oben nach unten, jede einmal. Eine Regel sieht, was die Regeln darüber geändert
haben: Auf eine Regel, die den *Betrag* per KI füllt, kann eine folgen, die den *Betrag* prüft.
Eine Änderung lässt keine Regel ein zweites Mal laufen. Aktionen laufen in der aufgeführten
Reihenfolge, außer Webhooks: Sie werden gesendet, nachdem alles andere gespeichert ist.

Kann eine Aktion nicht ausgeführt werden – das Modell antwortet nicht, ein Webhook ist nicht
erreichbar, ein Tag aus der Regel wurde gelöscht –, wirken die übrigen Aktionen trotzdem, und die
Verarbeitungsschritte des Dokuments zeigen **Regeln ausführen** als fehlgeschlagen mit dem Grund.
Die Schritte nennen auch die Regeln, die gegriffen haben.

## Globale Regeln {#global}

Administratoren verwalten Regeln für alle unter **Admin → Regeln**. Sie funktionieren genau wie
die eigenen Regeln eines Benutzers und laufen für die Dokumente jedes Benutzers, vor dessen
eigenen Regeln; eine Regel des Benutzers sieht also, was eine globale getan hat. Benutzer sehen
globale Regeln nicht.

Tags, Dokumenttypen und Korrespondenten gehören jedem Benutzer selbst, darum nennt eine globale
Regel sie beim Namen, statt die eines Benutzers auszuwählen: Eine Bedingung wie *Tags enthalten
„zu zahlen“* trifft auf den Tag dieses Namens beim Besitzer des Dokuments zu, ohne Rücksicht auf
Groß- und Kleinschreibung. Eine Aktion, die einen Tag hinzufügt oder einen Dokumenttyp oder
Korrespondenten setzt, legt ihn in der Liste des Besitzers an, wenn es dort keinen dieses Namens
gibt; einen Tag zu entfernen, den er nicht hat, bewirkt nichts. Benutzerdefinierte Felder und ihre
Auswahlwerte gelten für alle und werden wie gewohnt ausgewählt.

## Webhooks {#webhooks}

Ein Webhook ist ein `POST` mit diesem JSON-Body:

```json
{
  "event": "rule.matched",
  "trigger": "processed",
  "rule": { "id": "…", "name": "Rechnungen" },
  "document": {
    "id": "…",
    "title": "Rechnung R-2026-001",
    "summary": "…",
    "document_date": "2026-10-01",
    "document_type": "Rechnung",
    "correspondent": "ACME GmbH",
    "tags": ["zu zahlen"],
    "fields": { "Betrag": 129.9, "Fälligkeitsdatum": "2026-10-31" },
    "processing_status": "completed"
  }
}
```

Mit einem Signaturgeheimnis trägt der Header `X-Lemmary-Signature` `sha256=` gefolgt vom
hexadezimalen HMAC-SHA256 des Bodys unter dem Geheimnis. Vergleichen Sie ihn in konstanter Zeit,
bevor Sie dem Aufruf vertrauen. Jede 2xx-Antwort gilt als Erfolg, eine Weiterleitung als Fehler;
ein Webhook hat 10 Sekunden, und ein fehlgeschlagener wird nicht wiederholt. Startet der Server neu,
während der Auftrag läuft, läuft der Auftrag erneut, sodass ein Webhook doppelt ankommen kann.

Webhooks durchlaufen dieselbe Adressprüfung wie Remote-Importe: Link-Local- und
Cloud-Metadaten-Adressen werden immer abgelehnt, private und Loopback-Adressen dann, wenn der
Server mit `IMPORT_ALLOW_PRIVATE=0` läuft.
