---
title: Dokumentation
description: "Anleitungen zur Installation, Konfiguration und zum Betrieb von Lemmary, dem selbst gehosteten Dokumentenarchiv mit OCR, KI-Metadaten und Recherche mit Quellenangaben."
---

# Lemmary-Dokumentation {#lemmary-docs}

Anleitungen zur Einrichtung und zum Betrieb von Lemmary.

- [Screenshots](/de/screenshots) — ein Rundgang durch jeden Bildschirm, vom Einrichtungsassistenten bis zu Deep Research
- [Deep Research](/de/deep_research) — dem Archiv eine Frage stellen und eine Antwort mit Quellenangaben erhalten, und was eine breit angelegte Frage kostet
- [Lemmary vs Paperless-ngx vs Papra](/de/comparison) — Schwerpunkt, OCR, KI, Freigabe, Automatisierung, Verschlüsselung und Lizenzierung im Vergleich
- [Self-Hosting mit Docker](/de/self_hosting) — das Image, Volumes, Proxys, Backups, Upgrades
- [Geführte Einrichtung der KI-Anbieter](/de/guided_ai_setup) — ein kostenloser Mistral-Schlüssel für OCR und Embeddings, Opencode Go für das Sprachmodell
- [Konfigurationsleitfaden](/de/setup) — Umgebungsvariablen, erster Start, Funktionen und Fehlerbehebung
- [Entwicklungsumgebung](/de/development) — Voraussetzungen auf dem Host, FAISS und Ausführung aus dem Quellcode
- [Speicher](/de/storage) — wo SQLite, lokale oder S3-Dokumentdateien, Embeddings und Suchindizes liegen
- [KI-Anbieter und Modelle](/de/ai_providers) — einen Anbieter wählen, die Schlüssel vorbelegen, die Modelle zuweisen
- [Paperless-ngx-API-Kompatibilität](/de/paperless_ngx) — Drittanbieter-Clients anbinden, eine bestehende Bibliothek migrieren
- [Scannen mit einem Netzwerkscanner](/de/scanning) — über eSCL/AirScan scannen, und wie die LAN-Suche das Gerät findet
- [Lokale OCR](/de/local_ocr) — die OCR-Engine selbst betreiben, damit Scans den Host nie verlassen
- [Lokale Embeddings](/de/local_embeddings) — das Embedding-Modell selbst betreiben, damit Deep Research keine Tokens kostet
- [Lokale OCR und Embeddings auf einem Mac](/de/local_ai_macos) — beides auf der GPU eines Macs mit M-Chip, nativ statt in Docker
- [Google-Vision-API-Schlüssel](/de/google_vision) — einen Cloud-Vision-API-Schlüssel für OCR beschaffen
- [Anmeldung mit ChatGPT](/de/chatgpt_login) — Chat, Extraktion und Deep Research mit einem ChatGPT-Abonnement statt mit einem nutzungsbasiert abgerechneten Schlüssel betreiben
- [Anmeldung per OAuth2 / SSO](/de/oauth) — die Anmeldung über einen Anbieter auf dem Anmeldebildschirm der App aktivieren
- [Anmeldung mit Passkey](/de/passkeys) — mit Fingerabdruck, Gesicht oder Geräte-PIN statt mit einem Passwort anmelden
- [Verschlüsselung im Ruhezustand](/de/encryption) — das Volume enthält nur Chiffretext, und was das kostet

## Lizenz {#license}

Lemmary ist quelloffen einsehbar (source-available) unter der
[PolyForm Noncommercial License 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0):
Das Self-Hosting ist für die private und sonstige nichtkommerzielle Nutzung kostenlos,
die geschäftliche Nutzung erfordert jedoch eine kommerzielle Lizenz. Siehe
[LICENSE](https://github.com/buldezir/lemmary/blob/main/LICENSE).
