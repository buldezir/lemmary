---
description: "Verbinden Sie Claude Code, Claude Desktop, Cursor oder einen eigenen Agenten über das Model Context Protocol mit einem Lemmary-Archiv. Schreibgeschützt, solange ein Admin keine Änderungen erlaubt."
---

# MCP für Agenten {#mcp-for-agents}

Lemmary kann sein Archiv über das [Model Context Protocol](https://modelcontextprotocol.io) bereitstellen, sodass ein Agent – Claude Code, Claude Desktop, Cursor oder einer, den Sie selbst geschrieben haben – Ihre Dokumente direkt durchsucht und liest statt über den Browser. Der Endpunkt ist schreibgeschützt, solange ein Admin keine Änderungen erlaubt, und nutzt dieselbe Abfrage wie Deep Search: Ein Token sieht genau das, was sein Benutzer in der App sieht, und nichts anderes.

## Standardmäßig eingeschaltet {#it-is-on-by-default}

Der Endpunkt antwortet nur auf ein Bearer-Token und kostet nichts, bis ein Agent ihn aufruft; daher ist er eingeschaltet, solange ein Betreiber ihn nicht abschaltet:

```env
MCP_ENABLED=0
```

Ausgeschaltet bedeutet, dass es die Route überhaupt nicht gibt. Starten Sie nach einer Änderung neu; das Flag wird einmal beim Start gelesen.

## Ein Token erhalten {#get-a-token}

Die Kontoseite hat einen Abschnitt **Agenten**: Ein Klick erzeugt ein langlebiges Token für Ihr Konto und zeigt die sofort einfügbare Konfiguration für Claude Code, Codex CLI, Gemini CLI, Cursor, VS Code, Windsurf, OpenCode und Claude Desktop (als Befehl, wo der Agent einen hat, sonst als dessen Konfigurationsdatei).

Jedes Lemmary-Auth-Token funktioniert ebenfalls. In einem Skript ist das langlebige Token vom Paperless-kompatiblen Endpunkt das bequemste:

```bash
TOKEN=$(curl -s -X POST https://lemmary.example.com/api/token/ \
  -d username=you@example.com -d password=… | jq -r .token)
```

Jedes Token ist auf die Dokumente eines Kontos beschränkt. Das Token eines Administrators gehört zum eigenen `users`-Konto des Administrators (dem, das bei der Einrichtung zusammen mit ihm angelegt wurde), nicht zu jedem Konto auf der Instanz.

## Verbinden {#connect}

Der Server spricht Streamable HTTP unter `POST /api/mcp` und erwartet `Authorization: Bearer <token>`. Wie vom Protokoll verlangt, enthalten Anfragen `Accept: application/json, text/event-stream`; MCP-Clients setzen das selbst, ein handgeschriebenes `curl` muss es angeben.

Claude Code:

```bash
claude mcp add --transport http lemmary https://lemmary.example.com/api/mcp \
  --header "Authorization: Bearer $TOKEN"
```

Claude Desktop, Cursor und die meisten anderen Clients erwarten dasselbe als JSON:

```json
{
  "mcpServers": {
    "lemmary": {
      "type": "http",
      "url": "https://lemmary.example.com/api/mcp",
      "headers": { "Authorization": "Bearer …" }
    }
  }
}
```

## Werkzeuge {#tools}

Zwei Arten: Suche über den Index und einfacher Zugriff auf die Datensätze. Die Intelligenz steckt in Ihrem Agenten, daher ruft hier nichts ein Sprachmodell auf.

| Werkzeug | Was es tut |
| --- | --- |
| `search_documents` | Hybride Suche nach Bedeutung und Stichwörtern über den Volltext- und Vektorindex, mit optionalen Filtern für Datum, Typ, Korrespondent und Tag. Gibt passende Dokumente mit jeweils ein bis drei wörtlichen Passagen zurück. |
| `read_documents` | Die für einen `focus` relevanten Teile von bis zu zehn Dokumenten, gewichtet über denselben Index. Lange Dokumente kommen als Passagen mit markierten Lücken zurück. |
| `list_documents` | Einfache Auflistung nach Metadaten: Datumsbereich, Dokumenttyp, Korrespondent, Tags, Verarbeitungsstatus (`unfinished` bedeutet alles außer abgeschlossen); sortiert nach Datum oder nach dem Zeitpunkt des Hinzufügens; seitenweise mit `limit` und `offset`. Gibt Metadaten und die Gesamtzahl zurück, keinen Text. |
| `get_document` | Die Metadaten eines Dokuments und sein extrahierter Text, ungewichtet und ungekürzt. Ein Aufruf liefert bis zu 200.000 Zeichen; blättern Sie durch ein längeres Dokument mit `offset` und `max_chars`, und `truncated` zeigt an, wenn noch mehr folgt. |
| `count_documents` | Zählt Dokumente, die den Filtern entsprechen, optional gruppiert nach `document_type`, `correspondent`, `year`, `month` oder `tag`. |
| `list_taxonomy` | Die Namen der Tags, Dokumenttypen und Korrespondenten im Archiv, für die oben genannten Filter. Jede Liste endet bei 5.000 Namen und meldet dies mit `truncated`. |

## Schreibwerkzeuge {#write-tools}

Änderungen am Archiv sind standardmäßig aus. Ein Admin schaltet jede Art von Änderung unter **Einstellungen → MCP** ein, für die Token aller Benutzer zugleich; ein ausgeschaltetes Werkzeug wird gar nicht angeboten. Jede Änderung betrifft nur Dokumente und Tags, die dem Benutzer des Tokens gehören: Ein mit ihm geteiltes Dokument bleibt schreibgeschützt.

| Schalter | Werkzeuge | Was es tut |
| --- | --- | --- |
| Metadaten bearbeiten | `update_document` | Setzt Titel, Zusammenfassung, Datum, Dokumenttyp, Korrespondent (beide bei Bedarf per Name angelegt) und Tags (vorhandene Namen, die ganze Liste) eines Dokuments und schließt die Prüfung ab. Die Änderung gilt als von Hand gemacht. |
| Dokumente erneut verarbeiten | `reprocess_documents` | Stellt bis zu 50 Dokumente erneut in die Warteschlange für OCR und Extraktion (`auto`, `full` oder `extraction`). Verursacht KI-Kosten, und die Extraktion überschreibt die Metadaten, auch manuelle Änderungen. |
| Dokumente hochladen | `upload_document` | Fügt eine Datei hinzu, base64-kodiert, bis 20 MB; es gelten dieselben Typen, Kontingente und dieselbe Duplikatprüfung wie auf der Upload-Seite, und die Datei wird wie jede andere verarbeitet. |
| Dokumente löschen | `delete_documents` | Löscht bis zu 50 Dokumente samt Dateien. Endgültig: Es gibt keinen Papierkorb. |
| Tags verwalten | `create_tag`, `rename_tag`, `delete_tag` | Legt Tags per Name an, benennt sie um und löscht sie. Ein gelöschter Tag verschwindet von seinen Dokumenten, die Dokumente bleiben. |

Ein Schalter wirkt ab dem nächsten Aufruf, ohne Neustart. Für bestehende Clients bleibt die [paperless-ngx-API](/de/paperless_ngx) der Weg zum Schreiben.

## Was es kostet {#what-it-costs}

Jeder Aufruf von `search_documents` ist eine Deep-Search-Abfrage: eine Volltextabfrage sowie, wenn Embeddings konfiguriert sind, eine Embedding-Anfrage an Ihren Anbieter für den Abfragetext. Ein Aufruf von `read_documents` mit einem `focus` bettet den Fokus auf dieselbe Weise ein. Hier ruft nichts ein Sprachmodell auf, daher werden keine Chat-Tokens verbraucht. Ein Agent in einer Schleife zahlt trotzdem pro Aufruf, über Ihren Schlüssel. Erneute Verarbeitung und Uploads durchlaufen die Verarbeitungspipeline, mit denselben Modellen und Kosten wie jedes andere Dokument. Solange das Flag nicht gesetzt ist, wird nichts ausgegeben.
