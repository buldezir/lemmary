package appapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/retrieval"
	"lemmary/backend/internal/strutil"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 20
	maxSummaryLen      = 300
	maxSnippetLen      = 220
	snippetContext     = 80

	// minReadCharsPerDocument keeps a read of many documents at once from
	// returning slices too short to carry a figure and its label. Bytes, like
	// the research budget it is spent against.
	minReadCharsPerDocument = 500
)

// agentRetriever is what the Deep Search tools run against: one per request,
// shared by the search and read closures so the per-turn work — and later the
// query vector — is paid for once.
//
// The dense fields are nil until an embedding provider is configured. Every
// branch that uses them is skipped when they are, and a failure on that path is
// logged and dropped rather than returned: a retrieval tool that errors out
// because the vector store is unhappy is worse than one that answers from
// keywords alone.
type agentRetriever struct {
	app    retrieverApp
	idx    *fulltext.Index
	userID string

	// embedQuery turns the query into a vector. The production embedder
	// reports token usage too, so it is adapted to this shape at the wiring
	// point rather than imported here.
	embedQuery func(ctx context.Context, text string) ([]float32, error)
	// chunks is the passage-level index searched by vector.
	chunks retrieval.ChunkSearcher

	// vectors memoizes the query embeddings this turn has already paid for.
	// A research run searches several times and often repeats a phrase, and a
	// focused read embeds the same focus string the search just used.
	mu      sync.Mutex
	vectors map[string][]float32
}

// retrieverApp is the slice of core.App the agent tools use: records by id,
// records by filter, and somewhere to log a degraded retrieval to.
type retrieverApp interface {
	documentLookup
	Logger() *slog.Logger
	FindRecordsByFilter(
		collectionModelOrIdentifier any,
		filter string,
		sort string,
		limit int,
		offset int,
		params ...dbx.Params,
	) ([]*core.Record, error)
}

// searchCandidateFactor widens the lexical list before fusion: the document
// that answers the question is often not in the lexical top ten, and fusion can
// only reorder what it was given.
const searchCandidateFactor = 3

// maxSearchCandidates caps that widening. Each candidate costs a record read
// during hydration, so this is what keeps one tool call bounded.
const maxSearchCandidates = 60

// denseCandidateFactor is how many chunks the dense leg asks for per document
// candidate. A document is many passages, and the four best chunks of the
// archive can easily all belong to one file, so a chunk budget the size of the
// document budget would return a single document's table of contents.
const denseCandidateFactor = 4

// maxPreFilterIDs is the largest id list sent to the chunk index as a
// pre-filter. Past it the filters are applied to the dense result instead: a
// disjunction of thousands of terms costs more than the search it guards.
const maxPreFilterIDs = 1024

// passageCapBytes is the total the passages of one search may quote, divided
// across its hits. Roughly a tenth of a tool result: enough to answer a simple
// question from the result list, not enough to make the list a read.
const passageCapBytes = 6000

func (r *agentRetriever) search(ctx context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if r.idx == nil || !r.idx.Ready() {
		return nil, fmt.Errorf("search index is not ready")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	candidates := limit * searchCandidateFactor
	if candidates > maxSearchCandidates {
		candidates = maxSearchCandidates
	}

	ftQuery := fulltext.Query{
		Text:   query,
		UserID: r.userID,
		// The agent's query is a guess the model made from a question, not a
		// filter the user typed. A near miss is worth far more here than an
		// empty list, so this is the one caller that relaxes matching.
		Relaxed:  true,
		DateFrom: strings.TrimSpace(args.DateFrom),
		DateTo:   strings.TrimSpace(args.DateTo),
		Limit:    candidates,
	}

	if typeName := strings.TrimSpace(args.DocumentType); typeName != "" {
		typeIDs, err := findNamedEntityIDs(r.app, "document_types", typeName, r.userID)
		if err != nil {
			return nil, err
		}
		if len(typeIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.DocumentTypeIDs = typeIDs
	}

	if corrName := strings.TrimSpace(args.Correspondent); corrName != "" {
		corrIDs, err := findNamedEntityIDs(r.app, "correspondents", corrName, r.userID)
		if err != nil {
			return nil, err
		}
		if len(corrIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.CorrespondentIDs = corrIDs
	}

	if tagNames := normalizeTagNames(args.Tags); len(tagNames) > 0 {
		tagIDs, err := findTagIDsByNames(r.app, tagNames, r.userID)
		if err != nil {
			return nil, err
		}
		if len(tagIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.TagIDs = tagIDs
	}

	result, err := r.idx.Search(ftQuery)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}

	lexical := make([]retrieval.Ranked, 0, len(result.Hits))
	byID := make(map[string]fulltext.Hit, len(result.Hits))
	for _, hit := range result.Hits {
		lexical = append(lexical, retrieval.Ranked{ID: hit.ID, Score: hit.Score})
		byID[hit.ID] = hit
	}

	// Dense retrieval finds documents that say the same thing in other words —
	// and in other languages — which is exactly what the lexical list misses.
	// Nil until it is configured; the fusion below then has one list and
	// returns it in order.
	var dense []retrieval.Ranked
	var denseChunks map[string][]retrieval.ChunkHit
	if chunkHits := r.searchChunks(ctx, ftQuery, query, candidates*denseCandidateFactor); len(chunkHits) > 0 {
		dense, denseChunks = retrieval.GroupChunks(chunkHits, retrieval.MaxPassagesPerDocument)
	}

	fused := retrieval.RRF(lexical, dense)

	// Hydration drops documents that were deleted or changed hands since they
	// were indexed, so the candidates are walked until `limit` survive rather
	// than cut to `limit` first — otherwise a stale index entry silently
	// shortens the result list.
	want := limit
	if len(fused) < want {
		want = len(fused)
	}
	// Asked for once, for the whole result list: a chunk-level keyword search
	// over exactly the documents about to be returned, so each hit can quote
	// the passage that matched rather than the top of the document.
	lexicalChunks := r.chunkTextHits(ctx, query, retrieval.IDs(fused), 2*limit)

	budget := retrieval.PassageBudgetPerDoc(passageCapBytes, want)
	hits := make([]ai.DocumentHit, 0, want)
	for _, item := range fused {
		if len(hits) == limit {
			break
		}
		hit, ok := r.hydrate(item.ID, byID[item.ID], denseChunks[item.ID], lexicalChunks[item.ID], query, budget)
		if !ok {
			continue
		}
		hits = append(hits, hit)
	}

	// embedded says whether the question reached a vector, which is not the
	// same as dense finding something: an archive with no embedded document
	// yet answers every query with an empty dense list, and the two cases need
	// different fixing.
	r.app.Logger().Info("deep search retrieval",
		"lexical", len(lexical),
		"dense", len(dense),
		"fused", len(hits),
		"embedded", r.embeddedQuery(query),
	)
	return hits, nil
}

// searchChunks runs the dense leg. Any failure — no embedder, an embedding
// call that errored, an index that is not ready — returns nothing, and the
// caller carries on with the lexical list alone.
func (r *agentRetriever) searchChunks(ctx context.Context, ftQuery fulltext.Query, query string, k int) []retrieval.ChunkHit {
	if r.chunks == nil || r.embedQuery == nil {
		return nil
	}
	vector, err := r.queryVector(ctx, query)
	if err != nil || len(vector) == 0 {
		return nil
	}

	// The agent's filters are document properties; the chunk index carries only
	// ownership. So they are resolved against the documents index and handed
	// down as ids — as a pre-filter while the list is small enough to send, and
	// as a post-filter over the far shorter dense list when it is not. Skipped
	// entirely on error: a dense list that quietly ignored a tag filter would
	// answer from the documents the question excluded.
	var eligible []string
	postFilter := false
	if fulltext.HasDocumentFilters(ftQuery) {
		ids, complete, err := r.idx.EligibleIDs(ftQuery, maxPreFilterIDs)
		switch {
		case err != nil:
			r.app.Logger().Warn("deep search filter resolution failed", slog.Any("error", err))
			return nil
		case complete && len(ids) == 0:
			return nil
		case complete:
			eligible = ids
		default:
			postFilter = true
		}
	}

	hits, err := r.chunks.SearchChunks(ctx, retrieval.ChunkQuery{
		Vector:      vector,
		Text:        query,
		UserID:      r.userID,
		DocumentIDs: eligible,
		K:           k,
	})
	if err != nil {
		r.logChunkFailure(err)
		return nil
	}
	if postFilter {
		hits = r.keepEligible(ftQuery, hits)
	}
	return hits
}

// chunkTextHits is the chunk-level keyword search behind the passages, grouped
// by document. A second query rather than a reuse of the dense list, because
// the two answer different questions: which documents are about this, and which
// sentences say it.
func (r *agentRetriever) chunkTextHits(ctx context.Context, query string, ids []string, max int) map[string][]retrieval.ChunkHit {
	if r.chunks == nil || len(ids) == 0 || max <= 0 {
		return nil
	}
	if len(ids) > max {
		ids = ids[:max]
	}
	hits, err := r.chunks.SearchChunks(ctx, retrieval.ChunkQuery{
		Text:        query,
		UserID:      r.userID,
		DocumentIDs: ids,
		K:           len(ids) * retrieval.MaxPassagesPerDocument,
	})
	if err != nil {
		r.logChunkFailure(err)
		return nil
	}
	_, byDoc := retrieval.GroupChunks(hits, retrieval.MaxPassagesPerDocument)
	return byDoc
}

// keepEligible drops dense hits whose document does not satisfy the search's
// filters. Only reached when there were too many eligible documents to send as
// a pre-filter.
func (r *agentRetriever) keepEligible(ftQuery fulltext.Query, hits []retrieval.ChunkHit) []retrieval.ChunkHit {
	ids := make([]string, 0, len(hits))
	seen := map[string]struct{}{}
	for _, hit := range hits {
		if _, ok := seen[hit.DocumentID]; ok {
			continue
		}
		seen[hit.DocumentID] = struct{}{}
		ids = append(ids, hit.DocumentID)
	}
	allowed, err := r.idx.KeepEligible(ftQuery, ids)
	if err != nil {
		r.app.Logger().Warn("deep search filter check failed", slog.Any("error", err))
		return nil
	}
	keep := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		keep[id] = struct{}{}
	}
	out := make([]retrieval.ChunkHit, 0, len(hits))
	for _, hit := range hits {
		if _, ok := keep[hit.DocumentID]; ok {
			out = append(out, hit)
		}
	}
	return out
}

// queryVector embeds a string once per turn. A research run searches several
// times around one question and then reads with the same phrase as its focus,
// so without this the same sentence is billed three or four times.
func (r *agentRetriever) queryVector(ctx context.Context, text string) ([]float32, error) {
	if r.embedQuery == nil {
		return nil, nil
	}
	r.mu.Lock()
	cached, ok := r.vectors[text]
	r.mu.Unlock()
	if ok {
		return cached, nil
	}

	vector, err := r.embedQuery(ctx, text)
	if err != nil {
		r.app.Logger().Warn("deep search query embedding failed", slog.Any("error", err))
		return nil, err
	}
	r.mu.Lock()
	if r.vectors == nil {
		r.vectors = map[string][]float32{}
	}
	r.vectors[text] = vector
	r.mu.Unlock()
	return vector, nil
}

// embeddedQuery reports whether this turn holds a vector for text.
func (r *agentRetriever) embeddedQuery(text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.vectors[text]) > 0
}

// logChunkFailure reports a chunk search that did not run. A dimension mismatch
// is louder than the rest: it means the index and the configured model
// disagree, which no retry fixes and which silently removes half the retrieval
// until somebody reindexes.
func (r *agentRetriever) logChunkFailure(err error) {
	if errors.Is(err, fulltext.ErrVectorDims) {
		r.app.Logger().Error("deep search chunk index is built for other dimensions", slog.Any("error", err))
		return
	}
	r.app.Logger().Warn("deep search chunk search failed", slog.Any("error", err))
}

// hydrate turns one fused id into the hit the model sees, with the verbatim
// passages that justify it. It reports false for a document that vanished or
// belongs to someone else.
func (r *agentRetriever) hydrate(
	id string,
	lexical fulltext.Hit,
	dense, lexicalChunks []retrieval.ChunkHit,
	query string,
	passageBudget int,
) (ai.DocumentHit, bool) {
	record, err := r.app.FindRecordById("documents", id)
	if err != nil {
		return ai.DocumentHit{}, false
	}
	if r.userID != "" && record.GetString("user") != r.userID {
		return ai.DocumentHit{}, false
	}

	ocrText := record.GetString("ocr_text")
	passages := documentPassages(id, ocrText, query, dense, lexicalChunks, lexical.OCRFragments, passageBudget)

	hit := ai.DocumentHit{
		ID:           record.Id,
		Title:        strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
		DocumentDate: truncateDate(record.GetString("document_date")),
		Summary:      strutil.TruncateRunes(strutil.FirstNonEmpty(record.GetString("summary"), record.GetString("purpose")), maxSummaryLen),
		Passages:     toolPassages(passages),
	}

	// The snippet stays filled whatever the passages did: it is what the
	// stored turn and the result card show, and a card wants one line.
	switch {
	case len(passages) > 0:
		hit.OCRSnippet = strutil.TruncateRunes(passages[0].Text, maxSnippetLen)
	case lexical.OCRSnippet != "":
		hit.OCRSnippet = lexical.OCRSnippet
	default:
		hit.OCRSnippet = ocrSnippet(ocrText, query)
	}

	hit.DocumentType = relatedName(r.app, "document_types", record.GetString("document_type"))
	hit.Correspondent = relatedName(r.app, "correspondents", record.GetString("correspondent"))
	hit.Tags = documentTagNames(r.app, record)
	return hit, true
}

// documentPassages picks what one hit quotes, from three lexical sources in
// order of how well each one can point at the match.
//
// Chunk hits from the passage index come first: they are ranked by BM25 over
// the same text the vectors were cut from, they carry offsets, and they are
// narrowed here to the sentence around the query's words. Term-centred windows
// over the raw text are next, for an instance with no chunk index at all.
// The Bleve highlight is last, and covers what neither can see — a fuzzy match,
// where the query's word does not literally occur in the text, so substring
// scanning finds nothing and only the index knows where it matched.
func documentPassages(
	documentID, ocrText, query string,
	dense, chunkHits []retrieval.ChunkHit,
	fragments []string,
	budget int,
) []retrieval.Passage {
	lexical := retrieval.Narrow(ocrText, query, chunkHits)
	if len(lexical) == 0 {
		lexical = retrieval.LexicalChunks(documentID, ocrText, query, retrieval.MaxPassagesPerDocument)
	}
	if len(lexical) == 0 {
		lexical = fragmentChunks(documentID, fragments)
	}
	return retrieval.SelectPassages(ocrText, dense, lexical, budget)
}

// fragmentChunks adapts Bleve highlight fragments to chunk hits. They carry no
// offsets — a fragment is formatted text, not a slice — so they are ranked by
// the order the highlighter put them in and quoted as they came.
func fragmentChunks(documentID string, fragments []string) []retrieval.ChunkHit {
	hits := make([]retrieval.ChunkHit, 0, len(fragments))
	for i, fragment := range fragments {
		if strings.TrimSpace(fragment) == "" {
			continue
		}
		hits = append(hits, retrieval.ChunkHit{
			DocumentID: documentID,
			// Negative, so a fragment can never collide with a real chunk
			// ordinal when the two lists are fused.
			Ord:   -1 - i,
			Score: 1 / float64(i+1),
			Text:  fragment,
		})
	}
	return hits
}

func toolPassages(passages []retrieval.Passage) []ai.Passage {
	if len(passages) == 0 {
		return nil
	}
	out := make([]ai.Passage, 0, len(passages))
	for _, p := range passages {
		out = append(out, ai.Passage{Page: p.Page, Text: p.Text})
	}
	return out
}

// relatedName resolves a relation id to its display name, tolerating a missing
// or deleted record the way the hit hydration always has.
func relatedName(app documentLookup, collection, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		return ""
	}
	return record.GetString("name")
}

func documentTagNames(app documentLookup, record *core.Record) []string {
	names := []string{}
	for _, tagID := range record.GetStringSlice("tags") {
		if tagID == "" {
			continue
		}
		tagRec, err := app.FindRecordById("tags", tagID)
		if err != nil {
			continue
		}
		if name := strings.TrimSpace(tagRec.GetString("name")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// read backs the agent's read_documents tool. The retriever holds it as a
// method so the dense ranking used by a focused read has somewhere to live.
func (r *agentRetriever) read(ctx context.Context, req ai.ReadRequest) ([]ai.DocumentContent, error) {
	return readUserDocuments(r.app, r.userID, req, r.focusRanker(ctx))
}

// focusChunkK is how many of a document's passages a focused read ranks. A
// document is at most a few thousand chunks and an excerpt can show a handful,
// so this only has to be comfortably more than fits.
const focusChunkK = 40

// focusRanker ranks one document's stored chunks against the focus, by meaning
// and by keyword at once. Nil when there is no chunk index, and nil in effect
// whenever a document has no usable chunks: the caller then falls back to
// term overlap over windows derived from the text.
func (r *agentRetriever) focusRanker(ctx context.Context) focusRanker {
	if r.chunks == nil {
		return nil
	}
	return func(documentID, ocrText, focus string) ([]retrieval.Window, []retrieval.Ranked) {
		// A failed embedding is not fatal here: the same call with no vector is
		// a chunk-level keyword search, which is still better than windows cut
		// by byte count.
		vector, _ := r.queryVector(ctx, focus)
		hits, err := r.chunks.SearchChunks(ctx, retrieval.ChunkQuery{
			Vector:      vector,
			Text:        focus,
			UserID:      r.userID,
			DocumentIDs: []string{documentID},
			K:           focusChunkK,
		})
		if err != nil {
			r.logChunkFailure(err)
			return nil, nil
		}

		windows := make([]retrieval.Window, 0, len(hits))
		ranked := make([]retrieval.Ranked, 0, len(hits))
		for _, hit := range hits {
			// Offsets that no longer fit the text come from a chunking of an
			// older revision. Dropped rather than clamped, and if that leaves
			// nothing the caller derives its own windows.
			if hit.StartByte < 0 || hit.EndByte <= hit.StartByte || hit.EndByte > len(ocrText) {
				continue
			}
			windows = append(windows, retrieval.Window{
				Ord:       hit.Ord,
				Page:      hit.Page,
				StartByte: hit.StartByte,
				EndByte:   hit.EndByte,
			})
			ranked = append(ranked, retrieval.Ranked{ID: strconv.Itoa(hit.Ord), Score: hit.Score})
		}
		if len(windows) == 0 {
			return nil, nil
		}
		return windows, ranked
	}
}

// focusRanker is how a focused read decides which parts of a document to show.
// The signature is the fallback's own: windows to quote from, and a ranking of
// them by their ordinal.
type focusRanker func(documentID, ocrText, focus string) ([]retrieval.Window, []retrieval.Ranked)

// readUserDocuments returns document text for documents the caller owns,
// divided across req.MaxTotalChars so a research run cannot overflow the model
// context window.
//
// ocr_text holds up to models.MaxOCRTextRunes runes, so not returning all of it
// is not an edge case — it is the normal path for anything longer than a few
// pages, and it used to mean the tail of a long document was unreachable
// forever. Two ways past that, and the request picks one:
//
//   - Focus assembles the parts of the document that match a question, with the
//     head and tail always included and the gaps marked.
//   - Offset continues a straight read where the last one stopped, so the
//     concatenation of successive reads is the document, byte for byte.
//
// Focus wins when both are given: a question is a better guide to what to
// return than a position, and the excerpt already includes the head.
// rank is the dense/lexical chunk ranking for a focused read, or nil when the
// document's own text is all there is to rank.
func readUserDocuments(app documentLookup, userID string, req ai.ReadRequest, rank focusRanker) ([]ai.DocumentContent, error) {
	if len(req.IDs) == 0 {
		return []ai.DocumentContent{}, nil
	}
	if req.MaxTotalChars <= 0 {
		return nil, fmt.Errorf("no context budget left to read documents")
	}

	perDoc := req.MaxTotalChars / len(req.IDs)
	if perDoc < minReadCharsPerDocument {
		perDoc = minReadCharsPerDocument
	}
	focus := strings.TrimSpace(req.Focus)

	docs := make([]ai.DocumentContent, 0, len(req.IDs))
	spent := 0
	for _, id := range req.IDs {
		if spent >= req.MaxTotalChars {
			break
		}
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		// Re-check ownership per record. The agent only passes ids it saw from
		// search_documents, but this is the boundary that has to hold.
		if userID != "" && record.GetString("user") != userID {
			continue
		}

		full := strings.TrimSpace(record.GetString("ocr_text"))
		// Bytes, not runes: MaxTotalChars comes from the research loop's
		// budget, which counts len() everywhere. Truncating to `limit` *runes*
		// hands back up to four times that many bytes — for Cyrillic or Greek
		// OCR, reliably twice — so the reader would overshoot the window the
		// caller had just reserved for it.
		limit := perDoc
		if remaining := req.MaxTotalChars - spent; limit > remaining {
			limit = remaining
		}

		doc := ai.DocumentContent{
			ID:            record.Id,
			Title:         strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate:  truncateDate(record.GetString("document_date")),
			DocumentType:  relatedName(app, "document_types", record.GetString("document_type")),
			Correspondent: relatedName(app, "correspondents", record.GetString("correspondent")),
			Tags:          documentTagNames(app, record),
			TotalChars:    len(full),
		}

		if focus != "" && len(full) > limit {
			var windows []retrieval.Window
			var ranked []retrieval.Ranked
			if rank != nil {
				windows, ranked = rank(record.Id, full, focus)
			}
			if len(windows) == 0 {
				// No chunk index, or none of its offsets still fit the text:
				// cut windows out of the text itself and rank them by how much
				// of the question they carry.
				windows = retrieval.Windows(full, nil)
				ranked = retrieval.TermOverlap(full, windows, focus)
			}
			text, omitted := retrieval.Excerpt(full, windows, ranked, limit)
			doc.Text = text
			doc.Truncated = true
			doc.Excerpted = true
			doc.PassagesOmitted = omitted
		} else {
			start := alignRuneStart(full, req.Offset)
			text := full[start:]
			if len(text) > limit {
				text = strutil.Truncate(text, limit)
				doc.Truncated = true
			}
			doc.Text = text
			if next := start + len(text); next < len(full) {
				doc.NextOffset = next
			}
		}

		spent += len(doc.Text)
		docs = append(docs, doc)
	}
	return docs, nil
}

// alignRuneStart clamps an offset into s and moves it onto a rune boundary, so
// a continued read never starts in the middle of a character.
func alignRuneStart(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset >= len(s) {
		return len(s)
	}
	for offset < len(s) && !utf8.RuneStart(s[offset]) {
		offset++
	}
	return offset
}

func findNamedEntityIDs(app retrieverApp, collection, name, userID string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	filter := "name ~ {:name} || name_original ~ {:name}"
	params := dbx.Params{"name": name}
	if userID != "" {
		filter = "user = {:userId} && (" + filter + ")"
		params["userId"] = userID
	}
	records, err := app.FindRecordsByFilter(
		collection,
		filter,
		"name",
		20,
		0,
		params,
	)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", collection, err)
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Id)
	}
	return ids, nil
}

// findTagIDsByNames resolves agent-supplied tag names to ids, scoped to
// userID (empty means unscoped, for superusers).
func findTagIDsByNames(app retrieverApp, names []string, userID string) ([]string, error) {
	ids := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		records, err := findTagsByNameFilter(app, "name = {:name}", name, userID)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			// Fall back to substring match so near-exact agent inputs still work.
			records, err = findTagsByNameFilter(app, "name ~ {:name}", name, userID)
			if err != nil {
				return nil, err
			}
		}
		for _, record := range records {
			if _, ok := seen[record.Id]; ok {
				continue
			}
			seen[record.Id] = struct{}{}
			ids = append(ids, record.Id)
		}
	}
	return ids, nil
}

func findTagsByNameFilter(app retrieverApp, filter, name, userID string) ([]*core.Record, error) {
	params := dbx.Params{"name": name}
	if userID != "" {
		filter = "user = {:userId} && (" + filter + ")"
		params["userId"] = userID
	}
	records, err := app.FindRecordsByFilter("tags", filter, "name", 5, 0, params)
	if err != nil {
		return nil, fmt.Errorf("lookup tags: %w", err)
	}
	return records, nil
}

func normalizeTagNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func ocrSnippet(ocrText, query string) string {
	ocrText = strings.TrimSpace(ocrText)
	if ocrText == "" {
		return ""
	}
	lowerOCR := strings.ToLower(ocrText)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	idx := -1
	if lowerQuery != "" {
		idx = strings.Index(lowerOCR, lowerQuery)
	}
	if idx < 0 {
		return strutil.TruncateRunes(ocrText, maxSnippetLen)
	}

	start := idx - snippetContext
	if start < 0 {
		start = 0
	}
	// Align to rune boundaries roughly by walking back if mid-rune.
	for start > 0 && !utf8.RuneStart(ocrText[start]) {
		start--
	}
	end := idx + len(query) + snippetContext
	if end > len(ocrText) {
		end = len(ocrText)
	}
	for end < len(ocrText) && !utf8.RuneStart(ocrText[end]) {
		end++
	}

	snippet := strings.TrimSpace(ocrText[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(ocrText) {
		snippet += "…"
	}
	return strutil.TruncateRunes(snippet, maxSnippetLen)
}

func truncateDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}
