# Verschlüsselung im Ruhezustand {#encryption-at-rest}

Standardmäßig aus, in jedem Build. Setzen Sie `VAULT_ENABLED=1`, und das
persistente Volume enthält nur noch Chiffretext.

## Konfiguration {#configuration}

All dies sind Umgebungsvariablen, die gelesen werden, bevor PocketBase existiert –
ein in `app_settings` gespeicherter Schalter läge in genau der Datenbank, die er
schützen soll, und könnte nicht abgefragt werden, bevor diese Datenbank
entschlüsselt ist. Der auskommentierte Block in `.env.example` enthält sie alle.

| Variable | Standard | Bedeutung |
| --- | --- | --- |
| `VAULT_ENABLED` | nicht gesetzt (aus) | Auf `1` setzen, um das Datenverzeichnis im Ruhezustand zu verschlüsseln. Die Instanz startet dann **gesperrt** und wartet auf eine Anmeldung. |
| `VAULT_DIR` | `pb_data` | Wo der verschlüsselte Tresor liegt (das persistente Volume). |
| `VAULT_WORKDIR` | `pb_work` neben `VAULT_DIR` | Wohin das Archiv entschlüsselt wird. Muss ein speicherbasiertes Dateisystem (tmpfs) sein; andernfalls schlägt der Start fehl. |
| `VAULT_PASSPHRASE` | nicht gesetzt | Entsperrt ohne das Webformular, für CLI-Unterbefehle und Tests. Nicht die Art, wie ein Server normalerweise laufen sollte: Die Passphrase liegt neben dem Chiffretext, den sie schützt. |
| `VAULT_ALLOW_DISK_WORKDIR` | nicht gesetzt | Erlaubt ein Arbeitsverzeichnis, das nicht speicherbasiert ist, **oder eines, das sich nicht prüfen lässt** – der Test des Dateisystemtyps funktioniert nur unter Linux, daher ist dies nötig, um überhaupt außerhalb von Linux zu laufen. Nur für die Entwicklung – es bedeutet Klartext auf der Festplatte. |
| `VAULT_ALLOW_SHRINK` | nicht gesetzt | Erlaubt ein Zurückschreiben, das mehr als die Hälfte des Archivs verwerfen würde. |
| `VAULT_ALLOW_INSECURE_GATE` | nicht gesetzt | Erlaubt, das Entsperrformular auf einer Adresse bereitzustellen, die von außerhalb dieses Hosts erreichbar ist. Nur, wenn Sie in Kauf nehmen, das Archivpasswort im Klartext zu senden. |
| `VAULT_KEEP_GENERATIONS` | `3` | Wie viele Generationen aufbewahrt werden, d. h. wie weit ein Rollback zurückreichen kann. |

Jeder der obigen `VAULT_*`-Schalter ist ein strikter Boolean: `1`/`true`/`yes`/`on`
oder `0`/`false`/`no`/`off`; alles andere verweigert den Start, statt zu raten.
Würde `VAULT_ENABLED=Y` als „aus“ gelesen, liefe eine Installation, die Sie für
verschlüsselt hielten, und füllte das Volume mit Klartext, während keine der
unten beschriebenen Garantien gälte – daher ist es ein Fehler und kein
Standardwert.

Auch der umgekehrte Fehler wird abgefangen: Ein Start **ohne** `VAULT_ENABLED` auf
einem Volume, das einen Tresor enthält, wird verweigert. Andernfalls fände
PocketBase dort keine Datenbank, legte eine an und zeigte neben dem Chiffretext
einen frischen Einrichtungsassistenten – ein Archiv, das scheinbar jedes Dokument
verloren hat, und eine Klartext-Datenbank, die nun in das Volume geschrieben wurde,
das nur Chiffretext enthalten sollte. Es genügt, `-f docker-compose.encrypted.yml`
zu vergessen, daher schlägt es lautstark fehl.


Mit `VAULT_ENABLED=1` enthält das persistente Volume nur Chiffretext: die
SQLite-Datenbanken sowie jedes hochgeladene Dokument und jede Vorschau. Eine
gestohlene Festplatte, ein geleakter Volume-Snapshot, ein `docker cp` aus einem
gestoppten Container oder ein Betreiber, der im Dateisystem stöbert – all das
liefert nichts. Der Volltextindex liegt in keiner Form auf dem Volume – er ist
abgeleitete Daten und wird daher bei jedem Entsperren im speicherbasierten
Arbeitsverzeichnis neu aufgebaut, statt verschlüsselt gespeichert zu werden;
dadurch bleibt eine Klartext-Schattenkopie des OCR-Texts jedes Dokuments
vollständig von der Festplatte fern.

Die Abschnittsvektoren von Deep Research folgen derselben Aufteilung. Die Vektoren
selbst liegen in `data.db`, werden auf dem Volume also wie jede andere Zeile
verschlüsselt und sind ohne zusätzlichen Schritt durch die Snapshots des Tresors
und die Sicherungen von PocketBase abgedeckt. Der daraus gebaute Vektorindex ist
wie der Textindex abgeleitete Daten: Er wird bei jedem Entsperren im
Arbeitsverzeichnis aus den gespeicherten Vektoren neu aufgebaut, ohne eine einzige
Anfrage an den Embedding-Anbieter – das Entsperren kostet also nichts und
funktioniert auch, wenn der Anbieter nicht erreichbar ist.

**Lesen Sie diesen gesamten Abschnitt, bevor Sie die Funktion aktivieren.** Sie
ändert, wie die Instanz startet, was passiert, wenn ein Passwort verloren geht,
und welche Funktionen verfügbar sind.

## Funktionsweise {#how-it-works}

Das Volume enthält einen Schlüsselbund, eine Kette versiegelter Manifeste und
einen inhaltsadressierten Speicher verschlüsselter Blobs – sonst nichts. Beim
Start ist die Instanz **gesperrt**: Sie liefert eine Entsperrseite aus und
beantwortet jede API-Route mit `423`. Die erste Anmeldung liefert die
Zugangsdaten, die den Hauptschlüssel entpacken, das Archiv wird in ein
speicherbasiertes Arbeitsverzeichnis entschlüsselt, und PocketBase wird darauf
gerichtet. Ab dann verhält sich die Instanz genau wie eine unverschlüsselte
Installation, bis sie beendet wird.

Änderungen werden etwa zehn Sekunden nach dem letzten Schreibvorgang, über einen
minütlichen Cron-Job und beim Herunterfahren auf das Volume zurückgeschrieben.

Der Hauptschlüssel wird einmal pro Zugangsdatum versiegelt, daher **kann jeder
Benutzer der Instanz sie entsperren** – das Passwort jedes Kontos wird automatisch
eingetragen, wenn es angelegt oder geändert wird, zusätzlich zu einem
Wiederherstellungscode. Die Verschlüsselung gilt instanzweit: Nach dem Entsperren
sehen alle Benutzer alle Daten, genau wie bisher. Für SaaS entsteht die
Isolation, indem jeder Kunde einen eigenen Container und ein eigenes Volume
erhält.

## Was sie schützt und was nicht {#what-it-protects-and-what-it-does-not}

Sie schützt vor Offline-Zugriff auf das Volume: Diebstahl oder Verlust der
Festplatte, ein geleakter Snapshot, eine vom Host kopierte Sicherung, ein
ausgemustertes Laufwerk.

Sie schützt **nicht** vor jemandem, der den laufenden Prozess kontrolliert. Nach
dem Entsperren liegt der Schlüssel im Speicher, und wer das Binary betreibt, kann
es so verändern, dass es den Schlüssel beim Entsperren abgreift. Dies ist
Verschlüsselung im Ruhezustand, nicht Zero-Knowledge, und sie sollte auch nicht
als Letzteres bezeichnet werden. Ende-zu-Ende-Verschlüsselung ist mit diesem
Produkt unvereinbar: OCR sendet Dokumente an Google Vision oder Mistral,
Extraktion, Chat und der Such-Agent senden OCR-Text an ein LLM, und sowohl die
Volltextsuche als auch die SQL-Filter brauchen Klartext auf dem Server.

Ebenfalls außerhalb dieser Grenze: Container-Logs auf dem Host bleiben Klartext.

**Das Entziehen eines Kontos entzieht nicht immer dessen Schlüssel zum Archiv.**
Der Schlüsselbund ist das, was das Volume entsperrt, und er ist zwangsläufig von
den Anmeldeeinstellungen getrennt: Er muss lesbar sein, bevor PocketBase existiert,
denn die Benutzer-Collection, die er sonst abfragen würde, liegt in der Datenbank,
die noch auf ihre Entschlüsselung wartet. Zwei Folgen muss ein Betreiber kennen,
weil keine davon in der Admin-Oberfläche sichtbar ist:

- Das Abschalten der Passwortauthentifizierung stoppt `POST /api/token`, Basic
  Auth und die Anmelderouten von PocketBase. Es verhindert **nicht**, dass
  dieselben Passwörter das Archiv an der Startschranke entsperren, die diese
  Einstellung nicht sehen kann.
- Das Löschen eines Benutzers entfernt normalerweise auch seine Schlüsselhülle.
  Das Löschen des **letzten** verbliebenen Kontos tut das nicht: Der
  Schlüsselbund weigert sich, sein einziges Zugangsdatum zu verwerfen, weil das
  das Archiv zerstören würde. Das Passwort eines ausgeschiedenen letzten Admins
  entschlüsselt das Volume daher weiterhin.

Wo eines von beidem wichtig ist, rotieren Sie: entsperren, das Zugangsdatum
eintragen, das Sie behalten möchten, und das Konto löschen, dessen Schlüssel nicht
mehr funktionieren soll, solange noch ein anderes Konto existiert.

Und das Volume selbst ist nicht undurchsichtig. Der Blob-Speicher verrät, wie
viele Dateien eine Instanz enthält, die Klartextgröße jeder einzelnen – die AEAD
ist längenerhaltend – und, da Inhaltsadressen innerhalb eines Tresors
deterministisch sind, ob zwei Blobs über Snapshots hinweg identisch sind. Das ist
einem inhaltsadressierten Speicher inhärent und nichts, was eine Passphrase
ändert.

## Den Port privat halten, bis der Tresor initialisiert ist {#keep-the-port-private-until-the-vault-is-initialised}

Ein nicht initialisierter Tresor legt sein Hauptpasswort anhand der ersten
Anfrage fest, die ihn erreicht, und übergibt diesem Besucher den
Wiederherstellungscode. Bis `vault init` gelaufen ist oder Sie das Passwort selbst
gesetzt haben, gehört die Instanz demjenigen, der den Port zuerst erreicht.

Veröffentlichen Sie ihn auf Loopback hinter einem TLS-terminierenden Proxy – das
tut `docker-compose.encrypted.yml` – oder führen Sie `vault init` bei der
Bereitstellung aus, sodass es kein Zeitfenster gibt. Über reines HTTP in einem LAN
verhindern die Same-Origin-Prüfungen der Schranke kein DNS-Rebinding, daher ist
„sonst ist niemand in diesem Netzwerk“ nicht die Garantie, nach der es klingt.

## Aktivieren bei einer Installation, die bereits Daten enthält {#enabling-it-on-an-install-that-already-has-data}

Das geht nicht. Der Start verweigert, einen Tresor in einem Verzeichnis zu
initialisieren, das bereits eine unverschlüsselte Installation enthält, denn das
würde mit einem leeren Archiv beginnen und die echten Dokumente im Klartext
daneben liegen lassen.

Migrieren Sie bewusst: Starten Sie die verschlüsselte Instanz mit einem **leeren**
`VAULT_DIR` auf einem **neuen** Volume, importieren Sie die Dokumente erneut und
vernichten Sie dann das alte Volume. Die alten Dateien an Ort und Stelle zu
löschen genügt nicht – ihr Inhalt bleibt aus dem freien Speicherplatz
wiederherstellbar, was in keinem sinnvollen Sinne Verschlüsselung im Ruhezustand
ist.

## Zugang verlieren {#losing-access}

Wer jedes Passwort **und** den Wiederherstellungscode verliert, verliert das
Archiv. Es gibt bewusst keine Möglichkeit für den Betreiber, das zu umgehen.

Der beim ersten Start erzeugte Code wird einmalig an den Browser zurückgegeben,
der den Tresor initialisiert hat. Er wird bewusst **nicht** ins Log geschrieben:
Container-Logs landen unverschlüsselt auf genau der Host-Festplatte, gegen die
diese Funktion schützt, und eine Wiederherstellungshülle lässt sich nachträglich
nicht widerrufen.

## Einen Tresor nicht-interaktiv anlegen {#creating-a-vault-non-interactively}

`lemmary vault init` legt den Schlüsselbund für eine brandneue Instanz an und ist
der einzige Weg zum Wiederherstellungscode, wenn kein Browser den Tresor
initialisiert hat:

```bash
VAULT_ENABLED=1 VAULT_PASSPHRASE='...' lemmary vault init
```

| Ergebnis | Exit-Code | Ausgabe |
| --- | --- | --- |
| Tresor angelegt | 0 | `vault-recovery-code: <code>` auf stdout |
| Ein Schlüsselbund ist bereits vorhanden | 0 | `vault: already initialised` auf stdout |
| Alles andere | 1 | Grund auf stderr |

Eine erneute Ausführung ist unbedenklich, sodass ein Bereitstellungsschritt es
ohne Sonderfall für „vielleicht hat es geklappt“ erneut versuchen kann. Er
initialisiert PocketBase **nicht** – keine Migrationen, keine Datenbank, kein
bleve –, und das ist beabsichtigt: Nichts sollte in genau dem Moment, in dem ein
frisches Archiv ohne Konto darin entsperrt wird, einen nicht initialisierten
Einrichtungsassistenten erreichen können.

Das hier angegebene Passwort versiegelt ein **Bootstrap-Zugangsdatum**, das
automatisch widerrufen wird, sobald das erste echte Konto eingetragen wird. Das
ist wichtig, weil dieses Passwort die Instanz über das erreicht, was sie
bereitgestellt hat – dessen Speicher und die Umgebung eines Containers, die jeder
mit Zugriff auf den Daemon-Socket einsehen kann –, und es darf daher nicht für die
gesamte Lebensdauer der Instanz ein gültiger Schlüssel zum Archiv bleiben. In der
Praxis: Verwenden Sie das Passwort des Kontos selbst als `VAULT_PASSPHRASE`, legen
Sie das Konto im selben Bereitstellungslauf an und notieren Sie dabei den
Wiederherstellungscode. Danach entsperrt sich die Instanz nur noch mit
Kontopasswörtern.

`VAULT_PASSPHRASE` danach gesetzt zu lassen, ist unschädlich. Sobald die
Bootstrap-Hülle widerrufen ist, öffnet die Passphrase nichts mehr, und statt den
Start fehlschlagen zu lassen – was den Container bei jedem Neustart in eine
Absturzschleife schicken würde –, protokolliert der Server, dass die Passphrase
nicht funktioniert hat, und zeigt wie gewohnt das Entsperrformular. Sie aus der
Umgebung zu entfernen ist trotzdem sauberer.

## Die Entsperrseite über TLS ausliefern {#serving-the-unlock-page-over-tls}

Das Entsperrformular überträgt das Passwort, das das gesamte Archiv
entschlüsselt, und darf daher nicht im Klartext ausgeliefert werden. Terminieren
Sie TLS davor und binden Sie die App lokal:

```bash
lemmary serve --http 127.0.0.1:8090   # behind nginx/Caddy/Traefik
```

Die Schranke, die dieses Formular ausliefert, läuft *bevor* PocketBase existiert,
spricht daher reines HTTP und hat keine TLS-Konfiguration, auf die sie
zurückgreifen könnte. Das Binden an eine **Loopback**-Adresse ist deshalb die
einzige Konfiguration, deren Sicherheit sie selbst überprüfen kann:

- `serve --http 127.0.0.1:8090` – erlaubt. Nur über einen Proxy auf diesem Host
  erreichbar, und dort gehört TLS hin.
- `serve yourdomain.com` (das eingebaute Autocert von PocketBase) – verweigert. In
  diesem Modus liefert PocketBase HTTPS auf `:443` aus und nutzt `:80` nur zur
  Weiterleitung, doch solange die Instanz gesperrt ist, lauscht nichts auf
  `:443`, sodass ein Browser, der `:80` erreicht, kein TLS hat, auf das er
  ausweichen könnte.
- jede andere Adresse, die nicht Loopback ist, einschließlich `--http
  0.0.0.0:8090` – verweigert, sofern nicht `VAULT_ALLOW_INSECURE_GATE=1` gesetzt
  ist.

**In einem Container ist der letzte Fall unvermeidlich und für sich genommen kein
Problem.** Die App muss innerhalb des Containers an `0.0.0.0` binden, sonst könnte
Docker den Port gar nicht veröffentlichen, und von dort aus kann sie einen auf
`127.0.0.1` veröffentlichten Port nicht von einem im ganzen LAN veröffentlichten
unterscheiden – diese Entscheidung steht in der Compose-Datei. Deshalb wird sie
dort festgehalten: `docker-compose.encrypted.yml` veröffentlicht den Port nur auf
`127.0.0.1` *und* setzt `VAULT_ALLOW_INSECURE_GATE=1`, um zu erklären, dass es das
getan hat. Die beiden Zeilen gehören zusammen. **Wenn Sie diese Portzuordnung
erweitern, entfernen Sie das Flag**, sonst übertragen Sie das Passwort des Archivs
im Klartext dorthin, wo der Port nun hinreicht.

Abgefangen wird damit die Konfiguration, die keines von beiden hat: den
`VAULT_*`-Block aus `.env.example` mit der Basisdatei `docker-compose.yml` zu
aktivieren, die auf jeder Host-Schnittstelle veröffentlicht. Das startete früher
und lieferte das Entsperrformular an das gesamte LAN aus, weil ein explizites
`--http` mit der Begründung von der Prüfung ausgenommen war, der Betreiber habe es
so gewählt – womit der Standard-Entrypoint und damit jede containerisierte
Installation ausgenommen war. Jetzt verweigert es den Start und sagt, was zu tun
ist.

## Voraussetzungen und Grenzen {#requirements-and-limits}

- `VAULT_WORKDIR` **muss** ein speicherbasiertes Dateisystem sein. Andernfalls
  verweigert die App den Start, denn eine Entschlüsselung auf die Festplatte würde
  die gesamte Funktion zunichtemachen. Bemessen Sie das tmpfs größer als das
  Archiv plus Reserve für das Rendern von PDF-Seiten, und halten Sie das
  Speicherlimit des Containers komfortabel über der Größe des tmpfs. Siehe
  `docker-compose.encrypted.yml`.
- **tmpfs kann ausgelagert werden, daher darf der Container nicht swappen.** Unter
  Speicherdruck lagert der Kernel das tmpfs – das entschlüsselte Archiv – auf das
  Swap-Gerät des Hosts aus, und das ist persistente Festplatte.
  `docker-compose.encrypted.yml` setzt `memswap_limit` gleich `mem_limit`, was dem
  Container Swap vollständig verweigert; behalten Sie diese Kopplung bei, wenn Sie
  eine der beiden Zahlen ändern. Die App kann das aus dem Container heraus nicht
  erkennen. Damit verwandt und ebenfalls außerhalb ihres Sichtfelds: Der
  **Ruhezustand (Hibernation)** des Hosts schreibt den gesamten RAM einschließlich
  des tmpfs auf die Festplatte – versetzen Sie keinen Host in den Ruhezustand, auf
  dem entsperrte Instanzen laufen.
- Das gesamte Archiv liegt im entsperrten Zustand im RAM, daher begrenzt der RAM,
  wie groß eine Instanz werden kann. **Embeddings vergrößern es doppelt**: einmal
  für die in `data.db` gespeicherten Vektoren und nochmals für den daraus unter
  `bleve/chunks` gebauten Vektorindex. Planen Sie für beides grob
  `chunks x dimensions x 4 bytes` ein, zuzüglich des Abschnittstexts, den der Index
  zum Zitieren speichert – ein Modell mit 1536 Dimensionen über einem Archiv von
  10.000 Dokumenten mit je ~5 Abschnitten ergibt etwa 300 MB Vektoren plus einen
  ähnlich großen Index, also ein halbes Gigabyte tmpfs, das eine Instanz ohne
  Embeddings nicht braucht. Ein Modell mit 1024 oder weniger Dimensionen kostet
  entsprechend weniger, weshalb man hier eines bevorzugen sollte, selbst wenn die
  Genauigkeit eines größeren Modells nichts extra kosten würde. Bemessen Sie das
  tmpfs in `docker-compose.encrypted.yml` entsprechend, bevor Sie
  `AI_EMBEDDING_MODEL` einschalten, und siehe
  [KI-Anbieter](/de/ai_providers#what-embeddings-cost).
- Ein [Embedding-Modell, das Sie selbst betreiben](/de/local_embeddings), kostet
  RAM an einer *zweiten*, separaten Stelle: im eigenen Container des Sidecars,
  ~2,2 GB Gewichte für das standardmäßige `BAAI/bge-m3`. Dieser Speicher liegt
  außerhalb des `mem_limit` des `app`-Containers und außerhalb des tmpfs, belastet
  also das Budget des Hosts, statt mit dem des Tresors zu konkurrieren – aber es
  ist derselbe RAM, und ein Host, der exakt für eine verschlüsselte Instanz
  bemessen ist, hat dafür nichts übrig. Die Modellwahl entscheidet außerdem über
  die oben genannte Anzahl der Dimensionen, und genau dort zahlt sich ein lokales
  Modell mit 1024 oder 384 Dimensionen doppelt aus.
- Nichts am Vektorindex kostet einen API-Aufruf. Wie der Textindex ist er
  abgeleitete Daten, die innerhalb des Tresors aus den Vektoren in `data.db` neu
  aufgebaut werden – ein Entsperren, eine Wiederherstellung oder ein geleertes
  Arbeitsverzeichnis kosten also einen Neuaufbau, nie einen zweiten Durchlauf beim
  Embedding-Anbieter. Die Vektoren selbst sind im Ruhezustand Chiffretext, da sie
  wie alles andere in `data.db` liegen.
- **Nur ein Prozess darf einen Tresor gleichzeitig verwenden.** `superuser
  upsert`, `migrate` und andere CLI-Unterbefehle können nicht gegen einen
  laufenden Server ausgeführt werden: Jeder Prozess würde seine eigene private
  Kopie entschlüsseln, und wer zuletzt zurückschreibt, gewinnt stillschweigend.
  Stoppen Sie zuerst den Server oder verwenden Sie die Admin-Oberfläche.
  CLI-Unterbefehle entsperren über `VAULT_PASSPHRASE`.
- Ein hartes Beenden (`kill -9`, OOM, Stromausfall) verliert die Schreibvorgänge
  seit dem letzten Zurückschreiben – Sekunden, nicht das Archiv. Ein sauberes
  Stoppen verliert nichts, sofern `stop_grace_period` lang genug für das
  Zurückschreiben beim Herunterfahren ist: Bei `SIGTERM` stoppt der Server den
  Job-Scheduler, wartet bis zu 20 s, bis laufende Anfragen und Worker-Jobs
  abgeschlossen sind, und versiegelt erst dann das Archiv – eine bereits mit `200`
  beantwortete Anfrage ist also darin enthalten. Läuft diese Wartezeit ab, meldet
  das Herunterfahren dies im Log und schreibt trotzdem zurück, denn zu hängen, bis
  Docker zu `SIGKILL` eskaliert, würde in jedem Fall mehr verlieren.
- Die eigenen Sicherungen von PocketBase und S3-Speicher werden verweigert,
  solange die Verschlüsselung aktiv ist: Beide würden unverschlüsselte Kopien
  außerhalb des Tresors schreiben. Das Tresorverzeichnis ist selbst eine
  konsistente verschlüsselte Sicherung – kopieren Sie es.

## Betrieb {#operating-it}

```bash
# Status, including generation, entry count and pending writes.
curl -H "Authorization: $TOKEN" localhost:8090/api/vault/status

# Force a flush before stopping (superuser).
curl -X POST -H "Authorization: $TOKEN" localhost:8090/api/vault/flush

# Mint another recovery code; shown once.
curl -X POST -H "Authorization: $TOKEN" localhost:8090/api/vault/recovery-code
```

Um zu überprüfen, dass kein Klartext auf das Volume gelangt:

```bash
docker compose stop
docker run --rm -v lemmary_app_data:/v alpine sh -c \
  'grep -rl "SQLite format 3" /v; grep -rl "%PDF-" /v' | wc -l   # must print 0
```

## Wie sie angebunden ist {#how-it-attaches}

`backend/internal/boot/vault.go` ist die gesamte Anbindung: Es beantwortet `vault
init` mit `Result.Handled`, öffnet den Tresor über `vault.Open`, richtet
`Result.DataDir` auf das entschlüsselte Arbeitsverzeichnis und übergibt `v.Close`
an `Result.Close` für das Zurückschreiben beim Herunterfahren.

`backend/main.go` weiß von alldem nichts. Es weiß nur, dass möglicherweise etwas
handeln muss, bevor die App konstruiert wird – und genau dafür ist `internal/boot`
da: Die Verschlüsselung im Ruhezustand ist die eine Funktion, die sich nicht wie
jede andere an eine laufende App anbinden lässt.
