---
title: Documentation
description: "Guides for installing, configuring, and running Lemmary, the self-hosted document archive with OCR, AI metadata, and cited research."
---

# Lemmary docs

Setup and operation guides for Lemmary.

- [Screenshots](/screenshots) — a tour of every screen, from the setup wizard to Deep Research
- [Deep Research](/deep_research) — ask the archive a question and get a cited answer, and what a broad one costs
- [Lemmary vs Paperless-ngx vs Papra](/comparison) — compare focus, OCR, AI, sharing, automation, encryption, and licensing
- [Self-hosting with Docker](/self_hosting) — the image, volumes, proxies, backups, upgrades
- [Guided AI provider setup](/guided_ai_setup) — a free Mistral key for OCR and embeddings, Opencode Go for the language model
- [Configuration Guide](/setup) — environment variables, first launch, features, and troubleshooting
- [Development environment](/development) — host prerequisites, FAISS, and running from source
- [Storage](/storage) — where SQLite, local or S3 document files, embeddings, and search indexes live
- [AI providers and models](/ai_providers) — pick a provider, seed the keys, bind the models
- [Paperless-ngx API compatibility](/paperless_ngx) — connect third-party clients, migrate an existing library
- [Scanning from a network scanner](/scanning) — scan over eSCL/AirScan, and how the LAN search finds the device
- [Lemmary Local AI on a Mac](/local_ai_macos) — embeddings, OCR and a text model on an Apple Silicon Mac, natively rather than in Docker
- [Lemmary Local AI on Linux](/local_ai_linux) — the same on a machine with an NVIDIA GPU, as a program or a Docker image
- [Google Vision API key](/google_vision) — obtain a Cloud Vision API key for OCR
- [ChatGPT sign-in](/chatgpt_login) — run chat, extraction and Deep Research on a ChatGPT subscription instead of a metered key
- [OAuth2 / SSO sign-in](/oauth) — enable provider sign-in on the app login screen
- [Passkey sign-in](/passkeys) — sign in with a fingerprint, face, or device PIN instead of a password
- [Encryption at rest](/encryption) — keep the volume ciphertext-only, and what that costs

## License

Lemmary is source-available under the
[PolyForm Noncommercial License 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0):
free to self-host for personal and other noncommercial use, but business use
requires a commercial license. See
[LICENSE](https://github.com/buldezir/lemmary/blob/main/LICENSE).
