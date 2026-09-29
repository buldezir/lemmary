# Scannen mit einem Netzwerkscanner {#scanning-from-a-network-scanner}

Hochladen → **Scannen** scannt direkt von einem Scanner in Ihrem lokalen Netzwerk. Kein
Treiber, keine Scan-Software, kein Computer dazwischen: Lemmary spricht direkt mit dem
Gerät, über das Protokoll, das Apple AirScan und die Standards eSCL nennen. Die meisten
Scanner und Multifunktionsdrucker, die seit etwa 2015 verkauft werden, beherrschen es
ab Werk – Brother, Canon, Epson, HP, Lexmark, Ricoh, Xerox –, meist hinter einer
Einstellung namens „AirPrint“, „Mopria“ oder „treiberlos“.

Seiten werden in A4, 300 dpi, Farbe gescannt. Scannen Sie so viele, wie das Dokument
hat – jede wird ans Ende angehängt –, und fügen Sie das Ganze dann als ein einziges
Dokument Ihrer Bibliothek hinzu, das anschließend wie jeder andere Upload per OCR
erfasst und gelesen wird.

## Lemmary auf den Scanner richten {#pointing-lemmary-at-the-scanner}

Klicken Sie auf **Scanner suchen**. Alles, was antwortet, erscheint in einer Liste;
wählen Sie ein Gerät aus, und die Adresse wird eingetragen. Der Browser merkt sich
das zuletzt verwendete.

Wenn nichts gefunden wird, geben Sie die Adresse des Scanners in das Feld ein –
`192.168.1.9` genügt. Die Adresse steht auf der Netzwerkeinstellungsseite des Geräts
selbst oder in der Liste der verbundenen Geräte Ihres Routers.

Nur private Adressen werden akzeptiert: `10.x`, `172.16–31.x` und `192.168.x`.
Lemmary scannt nicht von einer öffentlichen Adresse, nicht von `localhost` und nicht
von einer Link-Local-Adresse `169.254.x` – unter Letzterer betreiben Cloud-Anbieter
ihren Metadatendienst, und eine App, die jede ihr genannte Adresse abruft, ist eine
App, die man darauf richten kann.

## Wie die beiden Suchen funktionieren und wann welche scheitert {#how-the-two-searches-work-and-when-each-fails}

**Scanner suchen** führt zwei Suchen gleichzeitig aus.

**mDNS** ist die protokolleigene Lösung. Scanner melden sich im lokalen Netzsegment
als `_uscan._tcp` an, und eine Abfrage erhält den Modellnamen, den Port und den
richtigen URL-Pfad direkt vom Gerät. Dafür muss Multicast-Verkehr Lemmary erreichen,
was der Fall ist, wenn die App direkt auf dem Host läuft – aber **nicht** über das
Standard-Bridge-Netzwerk von Docker, in dem überhaupt nichts im LAN per Multicast
erreichbar ist. Mit `docker compose up` im ausgelieferten Zustand findet mDNS nichts.

Um mDNS durchzulassen, legen Sie den Container in das eigene Netzwerk des Hosts:

```yaml
services:
  app:
    image: ghcr.io/buldezir/lemmary:latest
    network_mode: host   # gives the container the host's LAN, mDNS included
    env_file: [.env]
    volumes: [app_data:/app/pb_data]
```

`network_mode: host` ersetzt die `ports:`-Zuordnung – die App lauscht dann auf
`PORT` (standardmäßig 80; setzen Sie `PORT=8090` in `.env`, um die gewohnte Adresse
beizubehalten). Das funktioniert nur unter Linux: Docker Desktop unter macOS und
Windows führt Container in einer virtuellen Maschine aus, die keinen Zugang zum
Multicast-Verkehr des LANs hat.

**Der Suchlauf** ist das, was überall sonst funktioniert. Er fragt jede Adresse in
einem Netzwerkbereich, ob sie einen eSCL-Endpunkt hat; das ist eine gewöhnliche
HTTP-Anfrage und wird wie jede andere aus einem Bridge-Netzwerk hinausgeleitet. Ein
/24 dauert etwa zwei Sekunden.

Standardmäßig werden drei Bereiche durchsucht: der, aus dem sich Ihr Browser verbunden
hat, sowie `192.168.1.0/24` und `192.168.0.0/24`, wo ein Heimnetz fast immer liegt.
Der erste ist nur eine Vermutung – ein Container sieht das Bridge-Gateway statt Ihrer
Adresse, und hinter einem Reverse Proxy sieht er den Proxy –, weshalb die beiden
üblichen in jedem Fall durchsucht werden.

Wenn Ihr Netzwerk woanders liegt, geben Sie seinen Bereich unter **Bereiche** ein und
suchen Sie erneut. Mehrere Bereiche, durch Kommas getrennt, werden gemeinsam
durchsucht. Ein Bereich größer als /22 oder eine Liste mit insgesamt mehr als 1024
Adressen wird abgelehnt: 65.000 Adressen per Knopfdruck abzusuchen, sollte nicht
versehentlich passieren.

Ein Scanner in einem anderen Subnetz als die App ist für mDNS unsichtbar, lässt sich
aber finden, indem man seinen Bereich durchsucht.

## Vorlagenglas oder Einzug {#glass-or-feeder}

**Vorlagenglas** scannt die eine Seite auf dem Glas. Klicken Sie für jedes weitere Blatt
auf **Weitere Seite scannen**.

**Einzug** zieht alle Blätter im Fach in einem einzigen Auftrag ein; das ist die richtige
Wahl für einen Stapel. Manche Geräte liefern ein PDF pro Blatt, andere ein PDF für den
ganzen Stapel; in beiden Fällen kommt es als ein Dokument an.

## Grenzen {#limits}

Ein Dokument darf höchstens 47 MB groß sein, dieselbe Obergrenze wie bei jedem Upload;
bei 300 dpi in Farbe entspricht das etwa fünfundzwanzig bis fünfzig Seiten gewöhnlichen
Papiers. Über 20 MB wird die OCR langsamer und unzuverlässiger, daher ist ein kleineres
Dokument immer noch das bessere. Wenn der Scan diese Grenze erreicht, fügen Sie das
Vorhandene Ihrer Bibliothek hinzu und beginnen ein zweites Dokument.

Ein halb fertiger Scan wird 30 Minuten nach der zuletzt gescannten Seite aufbewahrt.
Danach wird er verworfen, und die Seiten müssen erneut gescannt werden – die Datei liegt
am selben Ort wie jeder andere vorbereitete Upload und ist daher durch die
[Verschlüsselung im Ruhezustand](/de/encryption) geschützt, wenn diese eingeschaltet ist.

## Wenn es nicht funktioniert {#when-it-does-not-work}

**"Could not reach the scanner"** – das Gerät ist im Ruhezustand, in einem anderen
Subnetz oder spricht kein eSCL. Prüfen Sie von einem Rechner im selben Netzwerk, ob es
unter `http://<address>/eSCL/ScannerCapabilities` antwortet: Eine Seite mit XML
bedeutet, dass das Protokoll aktiv ist.

**"The scanner is busy with another job"** – etwas anderes scannt
gerade, oder ein vorheriger Auftrag wurde nie abgeschlossen. Die meisten Geräte geben
ihn nach einer Minute frei; hartnäckige brauchen einen Neustart durch Aus- und
Einschalten.

**"The scanner returned no pages"** – der Einzug ist leer, oder das Blatt wurde
nicht eingezogen.
