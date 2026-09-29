Um einen API-Schlüssel für Google Cloud Vision zu erhalten, sind einige bestimmte Schritte in der Google Cloud Console nötig. Da die Vision API ein kostenpflichtiger Dienst ist (wenn auch mit einem großzügigen kostenlosen Kontingent von 1.000 Anfragen pro Monat), müssen Sie ein Rechnungskonto einrichten, um sie zu aktivieren.

Hier ist eine Schritt-für-Schritt-Anleitung zum Erzeugen und Absichern Ihres API-Schlüssels.

---

## Schritt 1: Ein Google-Cloud-Projekt erstellen {#step-1-create-a-google-cloud-project}

Alle Google-Cloud-APIs müssen an ein bestimmtes Projekt gebunden sein, damit Nutzung, Kontingente und Abrechnung verwaltet werden können.

1. Rufen Sie die [Google Cloud Console](https://console.cloud.google.com/) auf.
2. Melden Sie sich mit Ihrem Google-Konto an.
3. Klicken Sie in der Kopfleiste oben links (neben dem „Google Cloud“-Logo) auf das Menü **Project Dropdown**.
4. Klicken Sie oben rechts im Pop-up-Fenster auf **New Project**.
5. Geben Sie einen **Project Name** ein (z. B. `My-Vision-App`) und klicken Sie auf **Create**.
6. Warten Sie einige Sekunden, bis das Projekt bereitgestellt ist, und stellen Sie dann sicher, dass es im Dropdown-Menü oben ausgewählt ist.

---

## Schritt 2: Ein Rechnungskonto einrichten {#step-2-set-up-a-billing-account}

Google verlangt ein verknüpftes Rechnungskonto, um die Vision API zu aktivieren, auch wenn Sie innerhalb des kostenlosen Kontingents bleiben.

1. Klicken Sie auf das **Navigation Menu** (die drei waagerechten Linien bzw. das Hamburger-Symbol oben links).
2. Wählen Sie **Billing**.
3. Wenn Sie noch kein Rechnungskonto haben, klicken Sie auf **Link a billing account** oder **Create account**.
4. Folgen Sie den Anweisungen, um Ihr Land, Ihr Profil und eine gültige Kreditkarte einzugeben.

> **Hinweis:** Google gewährt neuen Konten ein kostenloses Testguthaben von 300 $, und Ihnen wird nichts berechnet, solange Sie nicht vom Free Tier upgraden

---

## Schritt 3: Die Cloud Vision API aktivieren {#step-3-enable-the-cloud-vision-api}

Nachdem Ihr Projekt nun mit einem Rechnungskonto verknüpft ist, müssen Sie die eigentliche Vision-Funktion einschalten.

1. Öffnen Sie das Navigation Menu und gehen Sie zu **APIs & Services** > **Library**.
2. Geben Sie in der Suchleiste **Cloud Vision API** ein und drücken Sie die Eingabetaste.
3. Klicken Sie auf das Ergebnis **Cloud Vision API**.
4. Klicken Sie auf die blaue Schaltfläche **Enable**. (Das kann einen Moment dauern.)

---

## Schritt 4: Ihren API-Schlüssel erzeugen {#step-4-generate-your-api-key}

Sobald die API aktiviert ist, können Sie Ihre eigentlichen Zugangsdaten erzeugen.

1. Kehren Sie zum Navigation Menu zurück und wählen Sie **APIs & Services** > **Credentials**.
2. Klicken Sie oben auf dem Bildschirm auf die Schaltfläche **+ Create Credentials**.
3. Wählen Sie **API key** aus der Dropdown-Liste.
4. Ein Pop-up zeigt Ihren neuen API-Schlüssel an (eine lange Zeichenfolge aus Buchstaben und Ziffern).
5. Kopieren Sie diesen Schlüssel in die Zwischenablage. Fügen Sie ihn unter **Einstellungen → Anbieter** als `google_vision`-Anbieter ein oder vor dem ersten Start in `.env` als `OCR_API_KEY=`, zusammen mit `OCR_SDK=google_vision`.
