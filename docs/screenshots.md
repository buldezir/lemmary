# Screenshots

A tour of every screen in Lemmary, in the order you would meet them.

The library shown throughout is a demo archive of invented documents — invoices,
payslips, contracts, statements and receipts addressed to a fictional
"Robin Marsh". Every name, amount, address and reference number in these images
is made up; the metadata, OCR text and Deep Research answers around them are real
output from the pipeline reading those files.

Every image on this page opens on click. The captures are taken at twice the
size, so **Full size** in the viewer shows the interface exactly as it appears
on screen — which is the only way the text in them is readable. Once open, `←`
and `→` walk the whole tour, `z` toggles the size, and `Esc` closes.

## First launch

The in-app wizard runs once, on an instance with no admin account. It creates
that account and then collects the OCR and LLM credentials the pipeline cannot
start without.

![Setup wizard, admin account step](./screenshots/setup-admin.png)

Then two keys, not a provider catalogue: Mistral reads the documents and powers
meaning-based search, and one other provider does the thinking. The step links
the guide that opens both accounts, for whoever arrives without them.

![Setup wizard, the two provider keys](./screenshots/setup-providers.png)

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

Full-text search runs over titles, OCR text, tags, purposes and summaries
through a Bleve index.

![Searching the library](./screenshots/documents-search.png)

Document type and correspondent are typeahead filters built from the taxonomy
the extraction step created; tags narrow the same list and stack, and picking a
period in the timeline writes the date range. Every filter lives in the query
string, so a filtered list survives a reload and can be linked to.

![Document type filter open](./screenshots/documents-filter-type.png)

Filtering to failed documents turns the cards into a selection, so a batch can
be queued for another attempt.

![Failed documents selected for reprocessing](./screenshots/documents-failed.png)

Everything reflows to one column on a narrow screen, with the header links —
and the less-travelled pages behind the gear menu — stacked behind the menu
button.

![Documents list on a phone](./screenshots/documents-mobile.png)

## Tags

A tag exists only once you create it here. Processing assigns from this list and
never invents anything, which is what keeps a library of 200 documents from
ending up with 200 tags. Applying a new tag to documents already in the archive
means reading them again, so the page prices that run before offering it.

![The tag vocabulary](./screenshots/tags.png)

## The tray and the queue

With review always required, a finished document waits to be read rather than
joining the library unseen. The Inbox is that tray — waiting, still processing
and failed together — and the header carries what it holds.

![The Inbox](./screenshots/inbox.png)

Activity is the queue itself: what the worker is doing now, with what is left to
do under it, and whatever was cancelled or failed in the last day still listed.

![The processing queue](./screenshots/activity.png)

## One document

The detail page is where extraction gets reviewed. The file sits beside its
metadata -- a PDF in the browser's own viewer, so you can read page three while
correcting the fields it belongs to -- and the **Preview** button hides that
column when the fields need the width. Fields the model wrote in the document's
own language keep the original underneath the translation, so a German invoice
reads in English without losing what it actually said.

![Document detail](./screenshots/document-detail.png)

**Unlock editing** turns those fields into a form. Corrections are saved back
onto the document, and the taxonomy follows: a new type or correspondent typed
here is created and reused from then on. Tags are different -- they are picked
from a list you keep under **Tags**, so the editor offers what exists rather
than creating one from whatever you type.

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

![Files staged for upload](./screenshots/upload-staged.png)

A network scanner is a source of its own: the page speaks eSCL to the device
over the LAN, so nothing is installed and no computer sits in between.

![Scanning from a network scanner](./screenshots/upload-scan.png)

A zip you packed yourself is read before anything is created, folders inside it
kept as a name prefix.

![A zip archive previewed](./screenshots/upload-zip.png)

A scanner that produced one PDF from a stack of unrelated paper can be cut back
apart — by hand, or with the cuts the model proposes.

![A four-page scan cut into three documents](./screenshots/upload-split.png)

An Amazon "Your Orders" export is previewed before anything is created: how many
invoice PDFs it holds, how many are new, and how many of its other files are
being ignored.

![Amazon order export previewed](./screenshots/upload-amazon.png)

## AI search and research

Two pages, two ways in. **AI assisted search**, reached from the document
list, finds documents and lists them as cards.

![AI assisted search, with hits](./screenshots/deep-search.png)

**Deep Research**, the header entry, reads what it found, counts and totals
across the archive, and writes an answer that links to its sources.

![A cited answer in Deep Research](./screenshots/deep-search-research.png)

The steps it took stream in while the run is live and collapse behind a summary
when it finishes, so a long run stays legible.

![Research steps expanded](./screenshots/deep-search-steps.png)

An answer worth keeping can be branched: the fork copies the chat up to that
answer and carries on there, leaving the original as it was. Chats are saved,
listed in the sidebar and resumable by URL.

![A research chat forked at one of its answers](./screenshots/deep-search-fork.png)

## Backup, restore and migration

The whole library — original files, OCR text, metadata, thumbnails and taxonomy
— downloads as one zip.

![Export page](./screenshots/export.png)

That zip restores into this instance or another one. It is previewed first, and
documents already present are skipped, so restoring twice is safe. Restoring
"as it was" sends nothing to OCR or the AI provider.

![A Lemmary archive previewed before restoring](./screenshots/import-archive-preview.png)

An existing paperless-ngx server can be pulled across directly, either keeping
its metadata or re-running the pipeline over the files.

![Import from paperless-ngx](./screenshots/import-ngx.png)

## Administration

Settings are runtime configuration, stored in the database rather than the
environment, and split into a tab each. Appearance names the instance and picks
the accent the whole interface takes.

![Settings, appearance](./screenshots/settings-appearance.png)

AI holds the providers and which model does what. API keys are write-only: the
page reports that a key is set, never what it is.

![Settings, the AI tab in full](./screenshots/settings.png)

Processing covers the timeouts, the language results come back in, whether every
document waits for review, and house rules appended to the extraction prompt —
the place to say that "Rechnung" is an invoice, or that these three senders are
the same company.

![Settings, processing](./screenshots/settings-processing.png)

The worker's own limits: how long one job may run, and how often a failed step
is retried.

![Settings, worker](./screenshots/settings-worker.png)

Exact duplicates are always refused on upload. Near-duplicate matching compares
OCR text instead, which costs a pass over the library, so it is off until asked
for.

![Settings, duplicates](./screenshots/settings-duplicates.png)

Library-wide maintenance: reprocess failed documents in batches, scan for
duplicates, delete taxonomy nothing points at any more, rebuild the search
index, and embed whatever is missing a vector.

![Maintenance page](./screenshots/maintenance.png)

A scratch page for checking a provider and model against one file, without
creating a document.

![OCR test page](./screenshots/ocr-test.png)
