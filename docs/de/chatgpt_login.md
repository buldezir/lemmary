# Anmeldung mit ChatGPT {#chatgpt-sign-in}

Lemmary kann seine Sprachmodell-Arbeit über ein **ChatGPT-Plus-, Pro- oder
Business-Abonnement** statt über einen nutzungsbasiert abgerechneten API-Schlüssel
ausführen. Sie melden sich einmal mit einem Gerätecode an, weisen Chat, Extraktion,
Deep Research und OCR diesem Abonnement zu, und diese Aufrufe gehen zulasten des
Platzes, den Sie ohnehin bezahlen, statt zulasten von API-Guthaben.

Das SDK wird auf jeder selbst gehosteten Instanz angeboten, aber nichts läuft darüber,
bis Sie den Anbieter hinzufügen und sich anmelden – und es gibt gute Gründe, vorher die
ganze Seite zu lesen.

## Was es kann und was nicht {#what-it-can-and-cannot-do}

| | |
| --- | --- |
| Chat mit Dokumenten | ✅ |
| Metadatenextraktion | ✅ |
| Deep Research (und sein Helfer pro Dokument) | ✅ |
| OCR | ✅ – ein PDF oder ein Bild, bis 10 MB, gelesen vom zugewiesenen Modell |
| Embeddings | ❌ – behalten Sie einen Anbieter mit Schlüssel oder den Sidecar zugewiesen |

OCR funktioniert genauso wie bei OpenAI oder OpenRouter: Die Datei geht als Anhang an
das Modell, und das Modell transkribiert sie. Die gesamte Pipeline kann also über das
Abonnement laufen – eine Instanz, deren einziger KI-Zugang ein ChatGPT-Platz ist, ist
damit eine vollständige Installation, ganz ohne API-Schlüssel.

Embeddings sind die Ausnahme, und zwar grundsätzlich: Der Endpunkt hat überhaupt kein
`/embeddings`, daher lehnen die Einstellungen diese Zuweisung ab. Deep Research
funktioniert auch ohne sie – es weicht auf Stichwortsuche aus –, aber seine dichte Hälfte
braucht einen Anbieter mit Schlüssel oder den
[Sidecar für lokale Embeddings](/de/local_embeddings).

Zwei Dinge sollten Sie wissen, bevor Sie OCR hierüber laufen lassen. Das Kontingent eines
Abonnements ist ein Zeitfenster und kein Zähler, und OCR ist das Aufwendigste, was Lemmary
pro Dokument tut, sodass ein Massenimport ein Zeitfenster schnell aufbrauchen kann. Und hier
werden allgemeine Modelle gebeten, einen Scan zu lesen, kein Dokument-Endpunkt: Die OCR von
Mistral ist eigens für diese Aufgabe gebaut, und der Docling-Sidecar erledigt sie, ohne Ihren
Host zu verlassen. Beide sind die bessere OCR, sofern Sie sie haben.

## Worauf Sie sich einlassen {#what-you-are-agreeing-to}

Das funktioniert, indem sich Lemmary als einer der eigenen Codex-Clients von OpenAI
ausgibt: Es verwendet die Codex-Client-ID für die Anmeldung und den Codex-Header
`originator` für Anfragen. Diese Endpunkte sind nicht dokumentiert und den eigenen
Clients von OpenAI vorbehalten.

Drei Konsequenzen, über die Sie sich im Klaren sein sollten:

- **Das Risiko für das Konto tragen Sie.** OpenAI kann einen Drittanbieter-Client, der
  Abonnement-Zugangsdaten verwendet, als Verstoß gegen die Nutzungsbedingungen werten. Nichts
  hier verschleiert, was Lemmary tut, und niemand kann versprechen, wie OpenAI damit umgeht.
- **Es kann ohne Vorwarnung aufhören zu funktionieren.** Jede Änderung auf Seiten von OpenAI
  kann dazu führen, dass diese Anfragen abgelehnt werden. Der Fehler zeigt sich als
  Anbieterfehler beim nächsten Dokument, nicht als stillschweigendes Ausweichen auf einen
  kostenpflichtigen Anbieter.
- **Das Kontingent ist ein Zeitfenster, kein Zähler.** Das Kontingent eines Abonnements
  füllt sich über Zeitfenster von fünf Stunden und von einer Woche wieder auf. Deep Research
  verteilt sich über viele Dokumente und kann ein Zeitfenster schnell aufbrauchen; wenn das
  zum Problem wird, legen Sie zuerst die Zuweisung **Deep-Search-Helfer** wieder auf einen
  Anbieter mit Schlüssel – er erledigt den Großteil des Lesens.

Deshalb erscheint das SDK nie auf einer verwalteten Instanz: Dort ist der Mandant nicht
derjenige, dessen Konto auf dem Spiel stünde.

## Einschalten {#turning-it-on}

### 1. Die Anmeldung per Gerätecode im ChatGPT-Konto erlauben {#_1-allow-device-code-sign-in-on-the-chatgpt-account}

Die Anmeldung per Gerätecode ist standardmäßig für alle ausgeschaltet. Sie ermöglicht es,
eine Anmeldung von einem beliebigen Browser aus zu bestätigen, und das ist der einzige Weg,
wie dies auf einem Server funktioniert.

- **Persönliches Konto** – ChatGPT → **Settings** → **Security** → die Autorisierung per
  Gerätecode (device code authorization) aktivieren.
- **Team, Enterprise oder Edu** – ein Workspace-Administrator muss sie in den
  Workspace-Berechtigungen erteilen; der persönliche Schalter ist ausgegraut oder fehlt.

Wenn Sie diesen Schritt auslassen, schlägt die Anmeldung in Lemmary mit einer Meldung fehl,
die die Einstellung nennt.

### 2. Den Anbieter hinzufügen und anmelden {#_2-add-the-provider-and-sign-in}

1. **Einstellungen → Anbieter → Anbieter hinzufügen**, SDK **ChatGPT-Abonnement**. Es gibt
   kein Feld für einen API-Schlüssel. Speichern Sie.
2. Der gespeicherte Eintrag erhält eine Schaltfläche **Mit ChatGPT anmelden**. Klicken Sie
   darauf, und Lemmary zeigt einen kurzen Code und einen Link an.
3. Öffnen Sie den Link in einem beliebigen Browser, der beim ChatGPT-Konto angemeldet ist,
   geben Sie den Code ein und bestätigen Sie. Der Code ist etwa fünfzehn Minuten gültig.
4. Der Eintrag zeigt **angemeldet**, mit Konto und Tarif daneben.

### 3. Die Modelle zuweisen {#_3-bind-the-models}

Richten Sie unter **Einstellungen → Modelle** die **Allgemeine KI**, das **Erweiterte
Modell** oder die **OCR** auf den neuen Anbieter und wählen Sie ein Modell. Die
Auswahlliste wird lokal bereitgestellt – der Endpunkt veröffentlicht keinen Katalog –, und
das Feld **Eigene Modell-ID** nimmt alles an, was die Liste nicht nennt. Welche Modelle das
Konto tatsächlich nutzen darf, hängt von seinem Tarif ab; ein nicht erlaubtes Modell schlägt
bei der ersten Anfrage mit der eigenen Meldung des Backends fehl.

Für OCR wird dieselbe Liste angeboten wie für den Chat, weil der Codex-Katalog nichts darüber
aussagt, welche Modelle eine Datei lesen können. Wählen Sie ein größeres Modell, wenn die Scans
schlecht sind.

Lassen Sie **Embeddings**, wo es ist.

### Eine frische Instanz auf diese Weise einrichten {#setting-up-a-fresh-instance-this-way}

Der Einrichtungsassistent bietet ebenfalls **ChatGPT-Abonnement** an, sodass eine Instanz beim
ersten Start ganz ohne API-Schlüssel konfiguriert werden kann.
Wenn Sie es wählen, wird der Anbieter-Eintrag gespeichert und der Schritt dann für die
Anmeldung offen gehalten – das Token braucht einen Eintrag, unter dem es gespeichert wird –,
und die Einrichtung geht zu den Modellzuweisungen weiter, sobald Sie den Code bestätigt haben.

## Abmelden und als jemand anderes anmelden {#signing-out-and-signing-in-as-somebody-else}

**Abmelden** am Anbieter-Eintrag löscht das gespeicherte Token und lässt den Eintrag bestehen.
Die erneute Anmeldung sind dieselben zwei Klicks, daher braucht ein Kontowechsel keinen neuen
Anbieter.

Die Zuweisungen zeigen weiterhin auf den Eintrag, solange er abgemeldet ist; diese Funktionen
melden einen nicht konfigurierten Anbieter, bis sich wieder jemand anmeldet.

## Wo das Token liegt {#where-the-token-lives}

In der Spalte `oauth` des Anbieter-Eintrags, neben den API-Schlüsseln aller anderen Anbieter.
Wie ein Schlüssel wird es von der API nie zurückgegeben – die Einstellungen sehen nur, ob ein
Eintrag angemeldet ist, und den Kontonamen, mit dem er angemeldet wurde.

Das Access-Token ist etwa eine Stunde gültig und wird vor Ort erneuert, ungefähr einmal pro
Stunde, solange die Instanz läuft. Wenn Sie die
[Verschlüsselung im Ruhezustand](/de/encryption) verwenden, ist das Token genauso davon
erfasst wie ein API-Schlüssel.

## Fehlerbehebung {#troubleshooting}

**"device code sign-in is not enabled for this account"** – siehe Schritt 1 oben. In einem
Team-, Enterprise- oder Edu-Workspace kann nur ein Administrator sie erteilen.

**Der Code ist abgelaufen** – er ist etwa fünfzehn Minuten gültig. Klicken Sie erneut auf
**Mit ChatGPT anmelden**, um einen neuen zu erhalten.

**403 bei jeder Anfrage nach erfolgreicher Anmeldung** – meist deckt der Tarif des Kontos das
zugewiesene Modell nicht ab. Versuchen Sie das Standardmodell oder ein kleineres.

**Anfragen schlagen plötzlich für alle gleichzeitig fehl** – höchstwahrscheinlich das
Kontingent-Zeitfenster oder eine Änderung auf Seiten von OpenAI. Weisen Sie die Allgemeine KI
einem Anbieter mit Schlüssel zu, während Sie herausfinden, was davon zutrifft.

**OCR liefert leeren oder erfundenen Text** – das Modell hat die Datei erhalten, konnte sie
aber nicht lesen, oder es war ein kleines Modell bei einem schlechten Scan. Versuchen Sie zuerst
ein größeres Modell. `OCR_SDK=chatgpt` wird übrigens in `.env` abgelehnt: Die Umgebung kann
einen Schlüssel tragen, aber keine Anmeldung, daher wird OCR diesem Anbieter über die
Einstellungen zugewiesen.

**"LLM OCR does not support mime type ..."** – der Anhangsweg akzeptiert nur PDFs und Bilder,
genau wie bei OpenAI und OpenRouter. Alles andere braucht Mistral, Google Vision oder den
Docling-Sidecar.

**Die Extraktionsergebnisse wurden nach dem Wechsel schlechter** – die Extraktion fordert JSON
an, und ein Backend, das diese Anforderung nicht erfüllt, antwortet in reinem Text, der
nachsichtig geparst wird. Wenn die Metadaten dünner sind als vorher, legen Sie die **Allgemeine
KI** wieder auf einen Anbieter mit Schlüssel und lassen Sie das **Erweiterte Modell** auf dem
Abonnement.
