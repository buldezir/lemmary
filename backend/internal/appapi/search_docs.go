package appapi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	app    core.App
	idx    *fulltext.Index
	userID string

	// embedQuery turns the query into a vector. The production embedder
	// reports token usage too, so it is adapted to this shape at the wiring
	// point rather than imported here.
	embedQuery func(ctx context.Context, text string) ([]float32, error)
	// chunks is the passage-level index searched by vector.
	chunks retrieval.ChunkSearcher
}

// searchCandidateFactor widens the lexical list before fusion: the document
// that answers the question is often not in the lexical top ten, and fusion can
// only reorder what it was given.
const searchCandidateFactor = 3

// maxSearchCandidates caps that widening. Each candidate costs a record read
// during hydration, so this is what keeps one tool call bounded.
const maxSearchCandidates = 60

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
	if chunkHits := r.searchChunks(ctx, query, candidates*4); len(chunkHits) > 0 {
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
	budget := retrieval.PassageBudgetPerDoc(passageCapBytes, want)
	hits := make([]ai.DocumentHit, 0, want)
	for _, item := range fused {
		if len(hits) == limit {
			break
		}
		hit, ok := r.hydrate(item.ID, byID[item.ID], denseChunks[item.ID], query, budget)
		if !ok {
			continue
		}
		hits = append(hits, hit)
	}

	r.app.Logger().Info("deep search retrieval",
		"lexical", len(lexical),
		"dense", len(dense),
		"fused", len(hits),
		"embedded", len(dense) > 0,
	)
	return hits, nil
}

// searchChunks runs the dense leg. Any failure — no embedder, an embedding
// call that errored, an index that is not ready — returns nothing, and the
// caller carries on with the lexical list alone.
func (r *agentRetriever) searchChunks(ctx context.Context, query string, k int) []retrieval.ChunkHit {
	if r.chunks == nil || r.embedQuery == nil {
		return nil
	}
	vector, err := r.embedQuery(ctx, query)
	if err != nil || len(vector) == 0 {
		if err != nil {
			r.app.Logger().Warn("deep search query embedding failed", slog.Any("error", err))
		}
		return nil
	}
	hits, err := r.chunks.SearchChunks(ctx, retrieval.ChunkQuery{
		Vector: vector,
		Text:   query,
		UserID: r.userID,
		K:      k,
	})
	if err != nil {
		r.app.Logger().Warn("deep search chunk search failed", slog.Any("error", err))
		return nil
	}
	return hits
}

// hydrate turns one fused id into the hit the model sees, with the verbatim
// passages that justify it. It reports false for a document that vanished or
// belongs to someone else.
func (r *agentRetriever) hydrate(
	id string,
	lexical fulltext.Hit,
	dense []retrieval.ChunkHit,
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
	passages := documentPassages(id, ocrText, query, dense, lexical.OCRFragments, passageBudget)

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

// documentPassages picks what one hit quotes.
//
// Term-centred windows come first: they carry real offsets and there are
// several of them, where a Bleve highlight is a single formatted fragment. The
// highlight is the fallback for what the windows cannot see — a fuzzy match,
// where the query's word does not literally occur in the text at all, so
// substring scanning finds nothing and only the index knows where it matched.
func documentPassages(
	documentID, ocrText, query string,
	dense []retrieval.ChunkHit,
	fragments []string,
	budget int,
) []retrieval.Passage {
	lexical := retrieval.LexicalChunks(documentID, ocrText, query, retrieval.MaxPassagesPerDocument)
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
func (r *agentRetriever) read(_ context.Context, req ai.ReadRequest) ([]ai.DocumentContent, error) {
	return readUserDocuments(r.app, r.userID, req)
}

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
func readUserDocuments(app documentLookup, userID string, req ai.ReadRequest) ([]ai.DocumentContent, error) {
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
			windows := retrieval.Windows(full, nil)
			text, omitted := retrieval.Excerpt(full, windows, retrieval.TermOverlap(full, windows, focus), limit)
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

func findNamedEntityIDs(app core.App, collection, name, userID string) ([]string, error) {
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
func findTagIDsByNames(app core.App, names []string, userID string) ([]string, error) {
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

func findTagsByNameFilter(app core.App, filter, name, userID string) ([]*core.Record, error) {
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
