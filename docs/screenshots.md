# Screenshots

A tour of every screen in Lemmary, in the order you would meet them.

The library shown throughout is a demo archive of invented documents — invoices,
payslips, contracts, statements and receipts addressed to a fictional
"Robin Marsh". Every name, amount, address and reference number in these images
is made up; the metadata, OCR text and Deep Search answers around them are real
output from the pipeline reading those files.

## First launch

The in-app wizard runs once, on an instance with no admin account. It creates
that account and then collects the OCR and LLM credentials the pipeline cannot
start without.

![Setup wizard, admin account step](./screenshots/setup-admin.png)

With a provider saved, the last step picks which model does what. The catalogue
is fetched from the provider, so the list is whatever your key can actually
reach.

![Setup wizard, model selection step](./screenshots/setup-models.png)

## Signing in

Password, [passkey](/passkeys) and [OAuth2](/oauth) sign-in sit on one screen.
The passkey button appears once any account on the instance has enrolled one;
the provider buttons appear for whatever is enabled in PocketBase.

![Login screen with password, passkey and OAuth2](./screenshots/login.png)

Each account manages its own passkeys, one per device.

![Account page with an enrolled passkey](./screenshots/account.png)

## The documents list

Cards carry what the AI extracted: title, document type, correspondent, summary
and tags, with the processing status and the document's own date. The timeline
down the left side counts documents per year and month.

![Documents list](./screenshots/documents.png)

The whole page, pager included:

![Documents list, full page](./screenshots/documents-full.png)

Full-text search runs over titles, OCR text, tags, purposes and summaries
through a Bleve index.

![Searching the library](./screenshots/documents-search.png)

Document type and correspondent are typeahead filters built from the taxonomy
the extraction step created.

![Document type filter open](./screenshots/documents-filter-type.png)

![Documents filtered to one type](./screenshots/documents-filtered.png)

Picking a period in the timeline writes the date range, so the highlight and
the filtered list are the same piece of state — and both survive a reload,
because they live in the query string.

![Documents filtered to one month by the timeline](./screenshots/documents-timeline.png)

Filtering to failed documents turns the cards into a selection, so a batch can
be queued for another attempt.

![Failed documents selected for reprocessing](./screenshots/documents-failed.png)

Everything reflows to one column on a narrow screen, with the header links
stacked behind the menu button.

![Documents list on a phone](./screenshots/documents-mobile.png)

![Navigation panel on a phone](./screenshots/mobile-nav.png)

The less-travelled pages sit behind the header's gear menu.

![The More menu](./screenshots/nav-more.png)

## One document

The detail page is where extraction gets reviewed. Fields the model wrote in the
document's own language keep the original underneath the translation, so a
German invoice reads in English without losing what it actually said.

![Document detail](./screenshots/document-detail.png)

![Document detail, top of the page](./screenshots/document-detail-top.png)

Corrections are saved back onto the document, and the taxonomy follows: a new
tag, type or correspondent typed here is created and reused from then on.

![Editing a document's metadata](./screenshots/document-detail-edit.png)

Every run is recorded step by step — preview, OCR, duplicate detection,
extraction, apply, embed — with the provider and model each step used.

![Processing job detail](./screenshots/document-processing.png)

When a step fails, the panel says which one and why, and offers exactly the
steps to re-run.

![A failed document, with the error and the reprocess picker](./screenshots/document-failed.png)

Ask AI answers questions about a single document, using its OCR text as the
context. The chat is saved.

![Asking questions about one document](./screenshots/document-ask.png)

## Adding documents

PDFs, images, plain text, CSV, Word and Excel. Text formats are read locally and
skip OCR entirely.

![Upload page](./screenshots/upload-files.png)

![Files staged for upload](./screenshots/upload-staged.png)

A scanner that produced one PDF from a stack of unrelated paper can be cut back
apart — by hand, or with the cuts the model proposes.

![Split documents, empty](./screenshots/upload-split-empty.png)

![A four-page scan cut into three documents](./screenshots/upload-split.png)

An Amazon "Your Orders" export is previewed before anything is created: how many
invoice PDFs it holds, how many are new, and how many of its other files are
being ignored.

![Amazon order import, empty](./screenshots/upload-amazon-empty.png)

![Amazon order export previewed](./screenshots/upload-amazon.png)

![Amazon order export imported](./screenshots/upload-amazon-done.png)

## Deep Search

Two modes on two paths. **Search** finds documents and lists them as cards.

![Deep Search, empty](./screenshots/deep-search-empty.png)

![Deep Search in Search mode](./screenshots/deep-search.png)

**Research** reads what it found, counts and totals across the archive, and
writes an answer that links to its sources.

![Deep Search, Research mode empty](./screenshots/deep-search-research-empty.png)

![A cited answer in Research mode](./screenshots/deep-search-research.png)

The steps it took stream in while the run is live and collapse behind a summary
when it finishes, so a long run stays legible.

![Research steps expanded](./screenshots/deep-search-steps.png)

Chats are saved, listed in a sidebar and resumable by URL.

![Saved chats in the sidebar](./screenshots/deep-search-sessions.png)

## Backup, restore and migration

The whole library — original files, OCR text, metadata, thumbnails and taxonomy
— downloads as one zip.

![Export page](./screenshots/export.png)

![Archive download started](./screenshots/export-started.png)

That zip restores into this instance or another one. It is previewed first, and
documents already present are skipped, so restoring twice is safe. Restoring
"as it was" sends nothing to OCR or the AI provider.

![Import page](./screenshots/import-archive.png)

![A Lemmary archive previewed before restoring](./screenshots/import-archive-preview.png)

An existing paperless-ngx server can be pulled across directly, either keeping
its metadata or re-running the pipeline over the files.

![Import from paperless-ngx](./screenshots/import-ngx.png)

## Administration

Providers, models, processing and worker timeouts are runtime settings, stored
in the database rather than the environment. API keys are write-only: the page
reports that a key is set, never what it is.

![Settings, providers and models](./screenshots/settings-top.png)

![Settings, full page](./screenshots/settings.png)

Library-wide maintenance: reprocess failed documents in batches, scan for
duplicates, delete taxonomy nothing points at any more, rebuild the search
index, and embed whatever is missing a vector.

![Management page](./screenshots/management.png)

A scratch page for checking a provider and model against one file, without
creating a document.

![OCR test page](./screenshots/ocr-test.png)
