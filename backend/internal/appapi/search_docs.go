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
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/retrieval"
	"lemmary/backend/internal/strutil"
)

const (
	maxSummaryLen  = 300
	maxSnippetLen  = 220
	snippetContext = 80
)

// agentRetriever is one per request, shared by the search and read closures so
// the per-turn work is paid for once. The dense fields are nil until an
// embedding provider is configured, and a failure on that path is logged and
// dropped: answering from keywords alone beats erroring out.
type agentRetriever struct {
	app    retrieverApp
	idx    *fulltext.Index
	userID string

	embedQuery func(ctx context.Context, text string) ([]float32, error)
	chunks     retrieval.ChunkSearcher
	// helper is nil when every read is passed through as text.
	helper ai.Helper

	// vectors memoizes this turn's query embeddings: a run repeats the same
	// phrase across searches and reads.
	mu      sync.Mutex
	vectors map[string][]float32
}

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

// maxSearchDocuments is the only bound on one agent search, set by what a hit
// costs (a record read plus a share of the passage budget) rather than by the
// model. Recall past it is the next search's job.
const maxSearchDocuments = 60

// denseCandidateFactor is how many chunks the dense leg asks for per document:
// the best chunks of the archive can easily all belong to one file, so a chunk
// budget the size of the document budget returns one document.
const denseCandidateFactor = 4

// maxPreFilterIDs is the largest id list sent to the chunk index as a
// pre-filter. Past it the filters are applied to the dense result instead: a
// disjunction of thousands of terms costs more than the search it guards.
const maxPreFilterIDs = 1024

// passageCapBytes is the total one search may quote, divided across its hits:
// enough to answer a simple question, not enough to make the list a read.
const passageCapBytes = 6000

// focusExcerptBytes is how much of a document a read returns. A
// passage-selection size, not a guess at a context window: it is what makes
// "the relevant parts" a handful of passages rather than the document again.
const focusExcerptBytes = 12000

func (r *agentRetriever) search(ctx context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if r.idx == nil || !r.idx.Ready() {
		return nil, fmt.Errorf("search index is not ready")
	}

	ftQuery, unresolved, err := r.resolveFilters(args)
	if err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		// A filter naming something that does not exist matches nothing,
		// rather than everything.
		return []ai.DocumentHit{}, nil
	}

	cands, err := r.candidates(ctx, ftQuery, query, maxSearchDocuments)
	if err != nil {
		return nil, err
	}

	// Hydration drops documents deleted or changed hands since indexing, so
	// the candidates are walked until maxSearchDocuments survive rather than
	// cut to that many first: a stale entry would shorten the list.
	want := maxSearchDocuments
	if len(cands.fused) < want {
		want = len(cands.fused)
	}
	// One chunk-level search for the whole result list, so each hit can quote
	// the passage that matched rather than the top of the document.
	lexicalChunks := r.chunkTextHits(ctx, query, retrieval.IDs(cands.fused), 2*maxSearchDocuments)

	budget := retrieval.PassageBudgetPerDoc(passageCapBytes, want)
	hits := make([]ai.DocumentHit, 0, want)
	for _, item := range cands.fused {
		if len(hits) == maxSearchDocuments {
			break
		}
		hit, ok := r.hydrate(item.ID, cands.lexical[item.ID], cands.denseChunks[item.ID], lexicalChunks[item.ID], query, budget)
		if !ok {
			continue
		}
		hits = append(hits, hit)
	}

	// embedded says whether the question reached a vector, which is not the
	// same as dense finding something; the two cases need different fixing.
	r.app.Logger().Info("deep search retrieval",
		"lexical", len(cands.lexical),
		"dense", cands.dense,
		"fused", len(hits),
		"embedded", r.embeddedQuery(query),
	)
	return hits, nil
}

// Only completed documents are searched: a pending or failed one has no
// trustworthy text or metadata yet, and the chunk index embeds those too.
//
// unresolved lists the names that matched nothing, which callers treat as
// "matches no document": a misspelt tag must not widen a search to the archive.
func (r *agentRetriever) resolveFilters(args ai.SearchDocumentsArgs) (fulltext.Query, []string, error) {
	// args.Limit is decoded for schema compatibility and then ignored: fusion
	// can only reorder what it was given, so the index is asked for a page.
	ftQuery := fulltext.Query{
		Text:   strings.TrimSpace(args.Query),
		UserID: r.userID,
		// The agent's query is a guess, not a filter the user typed, so this
		// is the one caller that relaxes matching.
		Relaxed:          true,
		ProcessingStatus: models.DocStatusCompleted,
		DateFrom:         strings.TrimSpace(args.DateFrom),
		DateTo:           strings.TrimSpace(args.DateTo),
		Limit:            fulltext.MaxSearchLimit,
	}
	var unresolved []string

	if typeName := strings.TrimSpace(args.DocumentType); typeName != "" {
		typeIDs, err := findNamedEntityIDs(r.app, "document_types", typeName, r.userID)
		if err != nil {
			return ftQuery, nil, err
		}
		if len(typeIDs) == 0 {
			unresolved = append(unresolved, "document_type: "+typeName)
		}
		ftQuery.DocumentTypeIDs = typeIDs
	}

	if corrName := strings.TrimSpace(args.Correspondent); corrName != "" {
		corrIDs, err := findNamedEntityIDs(r.app, "correspondents", corrName, r.userID)
		if err != nil {
			return ftQuery, nil, err
		}
		if len(corrIDs) == 0 {
			unresolved = append(unresolved, "correspondent: "+corrName)
		}
		ftQuery.CorrespondentIDs = corrIDs
	}

	if tagNames := normalizeTagNames(args.Tags); len(tagNames) > 0 {
		tagIDs, err := findTagIDsByNames(r.app, tagNames, r.userID)
		if err != nil {
			return ftQuery, nil, err
		}
		if len(tagIDs) == 0 {
			unresolved = append(unresolved, "tags: "+strings.Join(tagNames, ", "))
		}
		ftQuery.TagIDs = tagIDs
	}
	return ftQuery, unresolved, nil
}

type candidateSet struct {
	fused       []retrieval.Ranked
	lexical     map[string]fulltext.Hit
	denseChunks map[string][]retrieval.ChunkHit
	dense       int
}

// candidates runs both legs and fuses them. want is how many documents the
// caller means to keep, not how many chunks the dense leg asks for.
func (r *agentRetriever) candidates(ctx context.Context, ftQuery fulltext.Query, query string, want int) (candidateSet, error) {
	result, err := r.idx.Search(ftQuery)
	if err != nil {
		return candidateSet{}, fmt.Errorf("search documents: %w", err)
	}

	lexical := make([]retrieval.Ranked, 0, len(result.Hits))
	byID := make(map[string]fulltext.Hit, len(result.Hits))
	for _, hit := range result.Hits {
		lexical = append(lexical, retrieval.Ranked{ID: hit.ID, Score: hit.Score})
		byID[hit.ID] = hit
	}

	// Dense finds documents that say the same thing in other words, and in
	// other languages. Nil until configured; fusion then has one list.
	var dense []retrieval.Ranked
	var denseChunks map[string][]retrieval.ChunkHit
	if chunkHits := r.searchChunks(ctx, ftQuery, query, want*denseCandidateFactor); len(chunkHits) > 0 {
		dense, denseChunks = retrieval.GroupChunks(chunkHits, retrieval.MaxPassagesPerDocument)
	}

	return candidateSet{
		fused:       retrieval.RRF(lexical, dense),
		lexical:     byID,
		denseChunks: denseChunks,
		dense:       len(dense),
	}, nil
}

// searchChunks runs the dense leg. Any failure returns nothing, and the caller
// carries on with the lexical list alone.
func (r *agentRetriever) searchChunks(ctx context.Context, ftQuery fulltext.Query, query string, k int) []retrieval.ChunkHit {
	if r.chunks == nil || r.embedQuery == nil {
		return nil
	}
	vector, err := r.queryVector(ctx, query)
	if err != nil || len(vector) == 0 {
		return nil
	}

	// The chunk index carries only ownership, so document filters are resolved
	// here and handed down as ids: a pre-filter while the list is small enough
	// to send, a post-filter otherwise. Skipped entirely on error, since a dense
	// list ignoring a tag filter would answer from the excluded documents.
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

// chunkTextHits is a second query rather than a reuse of the dense list: the
// two answer different questions, which documents are about this and which
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

// keepEligible is only reached when there were too many eligible documents to
// send as a pre-filter.
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

// queryVector embeds a string once per turn; see the vectors field.
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

func (r *agentRetriever) embeddedQuery(text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.vectors[text]) > 0
}

// A dimension mismatch is louder than the rest: the index and the configured
// model disagree, which no retry fixes and which silently halves retrieval.
func (r *agentRetriever) logChunkFailure(err error) {
	if errors.Is(err, fulltext.ErrVectorDims) {
		r.app.Logger().Error("deep search chunk index is built for other dimensions", slog.Any("error", err))
		return
	}
	r.app.Logger().Warn("deep search chunk search failed", slog.Any("error", err))
}

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

	// The snippet stays filled whatever the passages did: the stored turn
	// and the result card want one line.
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

// documentPassages tries three lexical sources in order of how well each can
// point at the match: chunk hits (offsets, narrowed to the matching sentence),
// then windows cut from the raw text, then the Bleve highlight, the only one
// that can locate a fuzzy match the text does not literally spell.
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

// fragmentChunks adapts Bleve highlight fragments to chunk hits. A fragment is
// formatted text, not a slice, so it has no offsets and is quoted as it came.
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

// read backs the agent's read_documents tool. With a helper model bound, a
// large read is distilled to notes and quotes; a small read passes through as
// excerpts, because for a needle question the exact wording is the point.
func (r *agentRetriever) read(ctx context.Context, req ai.ReadRequest) ([]ai.DocumentContent, error) {
	if req.Full || len(req.Chunks) > 0 {
		return readExact(r.app, r.userID, req)
	}
	if r.helper == nil {
		return readUserDocuments(r.app, r.userID, req, r.focusRanker(ctx), focusExcerptBytes)
	}
	docs, err := readUserDocuments(r.app, r.userID, req, r.focusRanker(ctx), helperInputBytes)
	if err != nil {
		return nil, err
	}
	if !shouldDistill(docs) {
		return trimToExcerpts(r.app, r.userID, req, r.focusRanker(ctx), docs), nil
	}
	question := strutil.FirstNonEmpty(strings.TrimSpace(req.Focus), strings.TrimSpace(req.Question))
	return r.distillDocuments(ctx, question, nil, docs), nil
}

// readExact serves the two reads that bypass excerpting and distillation: the
// named chunks of one document, or the whole of one document when it fits the
// caller's budget. Neither touches the helper; both are what the model asked
// for, verbatim.
func readExact(app documentLookup, userID string, req ai.ReadRequest) ([]ai.DocumentContent, error) {
	if len(req.IDs) != 1 {
		return nil, fmt.Errorf("one document at a time")
	}
	record, err := app.FindRecordById("documents", req.IDs[0])
	if err != nil || (userID != "" && record.GetString("user") != userID) {
		return []ai.DocumentContent{}, nil
	}
	full := record.GetString("ocr_text")
	doc := ai.DocumentContent{
		ID:            record.Id,
		Title:         strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
		DocumentDate:  truncateDate(record.GetString("document_date")),
		DocumentType:  relatedName(app, "document_types", record.GetString("document_type")),
		Correspondent: relatedName(app, "correspondents", record.GetString("correspondent")),
		Tags:          documentTagNames(app, record),
	}
	if len(req.Chunks) > 0 {
		text, total, unknown := sliceChunks(full, req.Chunks)
		if text == "" {
			return nil, fmt.Errorf("no such chunks: the document has %d (0-%d)", total, max(total-1, 0))
		}
		doc.Text = text
		doc.Excerpted = true
		doc.ChunkCount = total
		doc.UnknownChunks = unknown
		for _, ord := range req.Chunks {
			if ord >= 0 && ord < total {
				doc.Chunks = append(doc.Chunks, ord)
			}
		}
		return []ai.DocumentContent{doc}, nil
	}
	if req.MaxBytes > 0 && len(full) > req.MaxBytes {
		return nil, fmt.Errorf("document is %d KB in %d chunks and does not fit the remaining context; read_chunks the parts you need",
			len(full)/1000, len(splitChunks(full)))
	}
	doc.Text = full
	doc.ChunkCount = len(splitChunks(full))
	return []ai.DocumentContent{doc}, nil
}

// trimToExcerpts brings documents read at the helper's cap back to the agent's
// own, so a passthrough read cannot carry a whole document into the chat.
func trimToExcerpts(app documentLookup, userID string, req ai.ReadRequest, rank focusRanker, docs []ai.DocumentContent) []ai.DocumentContent {
	needsTrim := false
	for _, doc := range docs {
		if len(doc.Text) > focusExcerptBytes {
			needsTrim = true
			break
		}
	}
	if !needsTrim {
		return docs
	}
	trimmed, err := readUserDocuments(app, userID, req, rank, focusExcerptBytes)
	if err != nil {
		return docs
	}
	return trimmed
}

// focusChunkK only has to be comfortably more passages than an excerpt fits.
const focusChunkK = 40

// focusRanker ranks one document's stored chunks by meaning and keyword at
// once. Nil without a chunk index, and nil in effect when a document has no
// usable chunks; the caller then falls back to term overlap over the text.
func (r *agentRetriever) focusRanker(ctx context.Context) focusRanker {
	if r.chunks == nil {
		return nil
	}
	return func(documentID, ocrText, focus string) ([]retrieval.Window, []retrieval.Ranked) {
		// A failed embedding is not fatal: the same call with no vector is a
		// keyword search, still better than windows cut by byte count.
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
			// Offsets that no longer fit the text chunked an older revision.
			// Dropped rather than clamped.
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

type focusRanker func(documentID, ocrText, focus string) ([]retrieval.Window, []retrieval.Ranked)

// readUserDocuments returns text for documents the caller owns. A document
// longer than excerptBytes comes back as its head plus the passages most
// relevant to the focus, gaps marked; the user's question stands in for an
// absent focus, and the document says so in FocusUsed. A run that outgrows the
// model is a provider error, not something truncated here.
func readUserDocuments(app documentLookup, userID string, req ai.ReadRequest, rank focusRanker, excerptBytes int) ([]ai.DocumentContent, error) {
	if len(req.IDs) == 0 {
		return []ai.DocumentContent{}, nil
	}
	focus := strings.TrimSpace(req.Focus)
	focusUsed := ""
	if focus == "" {
		focus = strings.TrimSpace(req.Question)
		focusUsed = focus
	}

	docs := make([]ai.DocumentContent, 0, len(req.IDs))
	for _, id := range req.IDs {
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		// Re-check ownership per record: this is the boundary that has to hold.
		if userID != "" && record.GetString("user") != userID {
			continue
		}

		// The raw column, never a trimmed copy: every stored byte offset is
		// measured from byte 0 of documents.ocr_text as stored, so trimming the
		// leading whitespace here would shift a focused read off its passage.
		full := record.GetString("ocr_text")

		doc := ai.DocumentContent{
			ID:            record.Id,
			Title:         strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate:  truncateDate(record.GetString("document_date")),
			DocumentType:  relatedName(app, "document_types", record.GetString("document_type")),
			Correspondent: relatedName(app, "correspondents", record.GetString("correspondent")),
			Tags:          documentTagNames(app, record),
		}

		if len(full) > excerptBytes {
			text, omitted := excerptDocument(record.Id, full, focus, rank, excerptBytes)
			doc.Text = text
			doc.Excerpted = true
			doc.PassagesOmitted = omitted
			doc.FocusUsed = focusUsed
		} else {
			doc.Text = full
		}

		docs = append(docs, doc)
	}
	return docs, nil
}

// excerptDocument with an empty focus has nothing to rank by and returns the
// head alone, gap marked.
func excerptDocument(documentID, full, focus string, rank focusRanker, budget int) (string, int) {
	var windows []retrieval.Window
	var ranked []retrieval.Ranked
	if focus != "" && rank != nil {
		windows, ranked = rank(documentID, full, focus)
	}
	if len(windows) == 0 {
		// No chunk index, or none of its offsets still fit the text.
		windows = retrieval.Windows(full, nil)
		ranked = retrieval.TermOverlap(full, windows, focus)
	}
	return retrieval.Excerpt(full, windows, ranked, budget)
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

// findTagIDsByNames scopes to userID; empty means unscoped, for superusers.
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
			// Substring match, so near-exact agent inputs still work.
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
