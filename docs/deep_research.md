# Deep Research

Deep Research answers questions about the archive rather than listing documents
for you to read. Ask it something a folder cannot answer — *how much did I spend
on the car in 2024*, *what did the insurer write about the leak*, *how many
invoices are still unpaid* — and it finds the documents that bear on it, has each
one checked, reads the parts that matter, and writes an answer citing every
document it used. The finds, reads and surveys appear as they happen, so a long
run is watchable rather than a spinner.

It sits in the header, at `/rag/research`. Its sibling, **AI assisted search**
on the document list, is the one-round version: it finds documents and shows
them as cards, and it is the better tool when you know what you are looking for
and only want it found.

Asking well is mostly saying what you want back. Name the period, the
correspondent or the tag if you have one; say whether you want a figure, a date,
a list or an explanation; and ask a follow-up in the same chat rather than
starting over, because a chat keeps what earlier turns read and builds on it.
Chats are saved and each has its own URL.

## The two pages

One tool-calling agent over the Bleve full-text index serves two pages, one per path under `/rag`, on the same model binding.

- **AI assisted search** (`/rag/search`) sits beside **Upload document** on the document list — one round of `search_documents`, answered from titles, summaries and short OCR snippets. Results are shown as document cards.
- **Deep Research** (`/rag/research`) is the header entry — the agent locates evidence with `find_documents` (a search whose every match the General AI model verifies before the research model sees it), reads the chunks the finder pointed at (`read_chunks`) or a document whole (`read_documents` with `full`), surveys many at once when the question spans a topic (`survey_documents`), counts when asked how many (`count_documents`), and writes a markdown answer citing each document it used, with the documents it drew on listed under the answer. `search_documents` is not offered to the research model. Progress streams over `POST /api/app/search/stream` (server-sent events), so each find, read, survey and count appears as it happens.

Deep Research has no round or document limit. It keeps searching and reading until it can answer, the model stops making progress, or a completion is rejected because the conversation exceeded the model's context window. Without a language-model provider, both pages return a configuration error — see [AI providers and models](/ai_providers).

## How it finds documents

Both pages run the same retrieval — the search page as `search_documents`, the
research page inside `find_documents` — and it runs up to two searches for every
query. Only documents with processing status
`completed` are searched, surveyed or counted: one still pending, failed,
cancelled or waiting in the Inbox for review is invisible to both pages until
it completes. The same holds for the [MCP](/mcp) search, read and count tools.

- **Keywords (BM25)** over the documents index, [relaxed in two
  rungs](/setup#full-text-search) rather than the strict AND the Documents page keeps:
  there the query is a filter you typed, here it is a guess the model made from
  a question.
- **Meaning (kNN)** over `bleve/chunks`, a second index holding one entry per
  embedded passage with its vector, searched by cosine similarity to the
  embedded question. This half exists only when `AI_EMBEDDING_MODEL` is set and
  documents have actually been embedded; only completed documents are, so a
  document still pending or waiting for review takes no place in the vector
  list. The model can be a hosted one or [one you run yourself](/local_embeddings).

The two lists are fused by reciprocal rank fusion: a document scores the sum of
`1/(60 + rank)` over the lists it appears in. Only positions are read, never
scores — BM25 and cosine are not on the same scale, and normalising one against
the other would be guesswork. A document found by either signal survives; one
found by both rises.

The passages the model is shown come from the same chunk index: a keyword search
over the passages of exactly the documents being returned, narrowed to the
sentence around the match. Without a chunk index they are cut from the OCR text
around the query's terms instead, and failing that from the index's own
highlight — the model always gets verbatim text, whatever the retrieval was.

A document is addressed in **chunks**: the same deterministic split of its OCR
text that the embeddings use (about 1,100 characters each, numbered from 0), so
"chunk 12" names the same bytes for the helper that read it and for the research
model that asks for it. `find_documents` returns, per document, the chunk numbers
holding the evidence; `read_chunks` returns exactly those chunks, neighbouring
numbers merged into one passage. A plain `read_documents` of a long document is
still an excerpt — its head plus the passages ranked against the read's `focus`,
or against the user's question when the model named none — but `full: true`
returns the whole text when it fits what is left of the model's context window
(one document per call, refused with its size and chunk count when it does not).

With an embedding model set, the prompt tells the model that one search already
crosses languages and not to repeat a search translated into another one; the
`DEEP_SEARCH_LANGUAGES` list is then only a hint for spelling exact terms.
Without one, the model is asked to search once per configured language, since
translation is the only thing carrying an English question to a German invoice.

Filters — a tag, a type, a correspondent, a date range — are properties of a
document, and the chunk index deliberately carries none of them, so that
renaming a tag never rewrites a vector. They are resolved against the documents
index instead and applied to the passage search as a list of document ids.

What it costs: one embedding request per distinct query string per turn (the
result is reused for the rest of that turn, including a focused read with the
same words), and one or two extra index searches. Everything on the dense path
degrades rather than fails: no model bound, an embedding call that errored, an
index still rebuilding — the search is the keyword search it has always been.
Each call logs one line, `deep search retrieval lexical=… dense=… fused=…
embedded=…`, which is where to look when an answer seems to have missed a
document.

## How Research covers a topic

Reading documents one call at a time is right for a needle question and wrong
for a topic: two hundred documents read into one conversation is two hundred
documents re-sent on every later round. Four things keep a broad question
affordable, and the first also keeps a narrow one from missing a document.

- **`find_documents`.** The research model's only way into the archive. The
  retrieval above runs for its query and filters, and then the **General AI**
  model checks *every* candidate, not a top few — missing a document is the
  failure this guards against. It works in two passes. The **screen** shows the
  helper each candidate's catalogue entry — date, type, correspondent, tags,
  people, title, purpose, summary, page count and the passages the search
  matched — and asks for *yes*, *maybe* or *no*, told to say *no* only when the
  document is clearly unrelated; a candidate it does not mention is kept. The
  **read** then shows it the text of every *yes* and *maybe*: whole when it
  fits 16 KB, else the chunks that best match the question, marked with their
  numbers. What comes back to the research model is only the documents that
  held something: notes, quotes and the chunk numbers to read. A survivor the
  reader gave no answer for — a helper outage, a dropped id — comes back
  *unverified* rather than vanishing, and the model is told to read it before
  citing or dismissing it. Candidates are one keyword page, the 500 best
  matches, plus what the meaning search adds; `max_documents` can only narrow
  that. Cost per call is about one kilobyte of helper input per candidate for
  the screen plus up to 16 KB per survivor for the read, in batched calls; a
  find over five hundred candidates is a handful of screen calls and as many
  read calls as survivors fill. The progress line shows both passes:
  *Screened 120 of 400 documents*, then *Read 30 of 41 documents*.
- **Distilled reads.** When a `read_documents` call would put more than about
  32 KB of text, or more than five documents, into the conversation, the
  documents are read by the **General AI** model instead. The research
  model gets, per document, notes on what it says about the focus, verbatim
  quotes to cite, any requested values, and the chunk numbers to read — never
  the text. Smaller reads pass through as excerpts, because on a needle question
  the exact wording is the point. The reader is shown each document whole, up
  to 400 KB, and several short documents share one call.
- **`survey_documents`.** For "everything about X" or "the total of Y over the
  year": the same retrieval as a search, kept to 300 documents by default and
  1000 at most, every document read by the helper for one question, one compact
  row back per document. Number fields (`fields: [{name, type: "number"}]`) are
  summed, averaged and bounded on the server, per currency, and the model is
  told to report those figures rather than add rows itself. Progress streams as
  "Surveyed 120 of 300 documents".
- **`count_documents`.** For "how many" and "how are they distributed": filters
  alone are answered by the database (`COUNT(*)`, optionally `GROUP BY` type,
  correspondent, year, month or tag); with query text the index reports the
  exact strict-match total, and a grouped breakdown takes the index's ids to the
  database (marked approximate past 5000 matches). A filter naming a type,
  correspondent or tag that does not exist counts zero and says which name did
  not resolve, rather than matching everything.

The reasoning loop itself runs on the **Advanced model** (`AI_RESEARCH_MODEL`,
or **Advanced model** in Settings) while the bulk reads above stay on **General
AI**, because that work is many cheap calls where the loop is a few expensive
ones; with no Advanced model bound, the whole of research runs on General AI at
its price. A General AI model that rejects JSON mode is retried in plain text
and parsed leniently. Every completion logs its token usage
(`ai completion usage prompt_tokens=… cached_tokens=… completion_tokens=…`) and a
research run logs its total, which is where to look when checking what a question
cost.

Models that emit tool calls in their content rather than natively (the DSML
path) are told to answer after one round of tool results, so they cannot chain a
find into a read; they get one tool round — a find's notes and quotes are enough
for many questions — and the answer.

Only the tool call and its result enter the transcript. The helper's screen and
read calls are a separate conversation: they are not replayed on the next turn,
are not resumed when a run is picked back up, and do not count toward the
context meter the page shows.

## Paths, chats and settings

Each page is its own path — `/rag/search` and `/rag/research` — so which one a chat belongs to is carried by the URL and survives a reload, the back button, a bookmark and a shared link. `/rag` on its own redirects to AI assisted search.

A chat stays on the page it started on. A transcript is a sequence: its answers were produced by one of the two, and the next turn replays them to the model as its own prior work, so switching underneath would answer a later question in a way the earlier ones do not support. There is no control that moves a chat across; opening `/rag/search/<research-chat>` redirects to the path that matches, and a turn sent to the page a chat does not belong to is a 409. A saved chat reopens on the path it ran on.

Configure **General AI**, the **Advanced model** and **Deep search languages** in [Settings](/ai_providers#binding-models-in-settings).

How a chat is stored, resumed and cancelled is in [Chat sessions](/setup#chat-sessions).
