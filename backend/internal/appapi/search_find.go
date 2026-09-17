package appapi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/retrieval"
	"lemmary/backend/internal/strutil"
)

// findExcerptBytes is what the helper is shown of one candidate it reads:
// the whole text when it fits, else the chunks that best match the question.
// ponytail: 16 KB per survivor is the cost knob; a full-text second read is
// the upgrade if excerpts miss.
const findExcerptBytes = 16_000

// screenBatchBytes keeps a screen call to a page of catalogue entries; the
// entries are short, so this is hundreds of documents per call.
const screenBatchBytes = 120_000

// find backs find_documents: the fused retrieval, then the helper over every
// candidate in two passes. The screen judges from the catalogue entry alone
// and errs toward keeping; the read then sees the text of what survived and
// says what it holds and in which chunks. Missing a document is the failure
// this is built against, so no candidate is cut by rank.
func (r *agentRetriever) find(ctx context.Context, args ai.FindArgs, progress ai.FindProgress) (ai.FindResult, error) {
	if r.helper == nil {
		return ai.FindResult{}, fmt.Errorf("no helper model is configured for find_documents")
	}
	if r.idx == nil || !r.idx.Ready() {
		return ai.FindResult{}, fmt.Errorf("search index is not ready")
	}
	query := strings.TrimSpace(args.Query)
	question := strutil.FirstNonEmpty(strings.TrimSpace(args.Question), query)
	if query == "" {
		return ai.FindResult{}, fmt.Errorf("query is required")
	}
	// The keyword leg returns one page of at most MaxSearchLimit hits, so
	// that is also how many candidates one find can check.
	limit := args.MaxDocuments
	if limit <= 0 || limit > fulltext.MaxSearchLimit {
		limit = fulltext.MaxSearchLimit
	}
	if progress == nil {
		progress = func(string, int, int) {}
	}

	ftQuery, unresolved, err := r.resolveFilters(args.SearchArgs())
	if err != nil {
		return ai.FindResult{}, err
	}
	if len(unresolved) > 0 {
		return ai.FindResult{Unresolved: unresolved}, nil
	}
	cands, err := r.candidates(ctx, ftQuery, query, limit)
	if err != nil {
		return ai.FindResult{}, err
	}
	ids := retrieval.IDs(cands.fused)
	result := ai.FindResult{Candidates: len(ids)}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return result, nil
	}

	// One chunk-level search for the whole list, so the screen can show the
	// passage that matched rather than the top of the document.
	lexicalChunks := r.chunkTextHits(ctx, query, ids, 2*len(ids))
	budget := retrieval.PassageBudgetPerDoc(passageCapBytes, len(ids))

	type candidate struct {
		record *core.Record
		hit    ai.DocumentHit
	}
	loaded := make([]candidate, 0, len(ids))
	screen := make([]ai.ScreenDoc, 0, len(ids))
	for _, id := range ids {
		hit, ok := r.hydrate(id, cands.lexical[id], cands.denseChunks[id], lexicalChunks[id], query, budget)
		if !ok {
			continue
		}
		record, err := r.app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		loaded = append(loaded, candidate{record: record, hit: hit})
		passages := make([]string, 0, len(hit.Passages))
		for _, p := range hit.Passages {
			passages = append(passages, p.Text)
		}
		screen = append(screen, ai.ScreenDoc{
			ID:            record.Id,
			Title:         hit.Title,
			TitleOriginal: strings.TrimSpace(record.GetString("title_original")),
			DocumentDate:  hit.DocumentDate,
			DocumentType:  hit.DocumentType,
			Correspondent: hit.Correspondent,
			Tags:          hit.Tags,
			People:        models.PeopleOrOrganizations(record),
			Purpose:       strings.TrimSpace(record.GetString("purpose")),
			Summary:       strings.TrimSpace(record.GetString("summary")),
			PageCount:     record.GetInt("page_count"),
			Passages:      passages,
		})
	}

	progress("screen", 0, len(screen))
	verdicts := r.screenAll(ctx, question, screen, func(done int) { progress("screen", done, len(screen)) })
	result.Screened = len(screen)

	rank := r.focusRanker(ctx)
	docs := make([]ai.DistillDoc, 0, len(loaded))
	chunkCounts := make(map[string]int, len(loaded))
	hits := make(map[string]ai.DocumentHit, len(loaded))
	for _, c := range loaded {
		if verdicts[c.record.Id] == ai.VerdictNo {
			continue
		}
		text, count := chunkExcerpt(c.record.Id, c.record.GetString("ocr_text"), question, rank, findExcerptBytes)
		chunkCounts[c.record.Id] = count
		hits[c.record.Id] = c.hit
		docs = append(docs, ai.DistillDoc{
			ID:            c.record.Id,
			Title:         c.hit.Title,
			DocumentDate:  c.hit.DocumentDate,
			DocumentType:  c.hit.DocumentType,
			Correspondent: c.hit.Correspondent,
			Text:          text,
			Excerpted:     len(c.record.GetString("ocr_text")) > findExcerptBytes,
		})
	}
	result.Read = len(docs)
	if len(docs) == 0 {
		return result, nil
	}

	progress("read", 0, len(docs))
	rows, _ := r.distillAll(ctx, question, nil, docs, func(done int) { progress("read", done, len(docs)) })

	// A survivor the helper did not answer for is kept unverified, the way a
	// failed screen keeps its documents: a helper outage must not read as
	// "nothing found".
	for _, doc := range docs {
		row, ok := rows[doc.ID]
		if ok && !row.Relevant {
			continue
		}
		hit := hits[doc.ID]
		hit.Passages = nil
		found := ai.FindHit{
			ID:            doc.ID,
			Title:         doc.Title,
			DocumentDate:  doc.DocumentDate,
			DocumentType:  doc.DocumentType,
			Correspondent: doc.Correspondent,
			ChunkCount:    chunkCounts[doc.ID],
		}
		if ok {
			found.Notes, found.Quotes, found.Chunks = row.Notes, row.Quotes, row.Chunks
			if row.Notes != "" {
				hit.OCRSnippet = strutil.TruncateRunes(row.Notes, maxSnippetLen)
			}
		} else {
			found.Unverified = true
			result.Failed++
		}
		result.Hits = append(result.Hits, hit)
		result.Documents = append(result.Documents, found)
	}

	r.app.Logger().Info("deep search find",
		"candidates", result.Candidates,
		"screened", result.Screened,
		"read", result.Read,
		"found", len(result.Documents),
	)
	return result, nil
}

// screenAll runs the screen in batches and returns a verdict per document. A
// failed batch, or a document the helper left out, reads as maybe.
func (r *agentRetriever) screenAll(ctx context.Context, question string, docs []ai.ScreenDoc, progress func(done int)) map[string]string {
	verdicts := make(map[string]string, len(docs))
	for _, doc := range docs {
		verdicts[doc.ID] = ai.VerdictMaybe
	}

	var batches [][]ai.ScreenDoc
	var current []ai.ScreenDoc
	size := 0
	for _, doc := range docs {
		n := len(doc.Title) + len(doc.Purpose) + len(doc.Summary) + 200
		for _, p := range doc.Passages {
			n += len(p)
		}
		if len(current) > 0 && size+n > screenBatchBytes {
			batches = append(batches, current)
			current, size = nil, 0
		}
		current = append(current, doc)
		size += n
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}

	var (
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
		sem  = make(chan struct{}, helperConcurrency)
	)
	for _, batch := range batches {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []ai.ScreenDoc) {
			defer wg.Done()
			defer func() { <-sem }()
			result, err := r.helper.Screen(ctx, ai.ScreenRequest{Question: question, Docs: batch})
			mu.Lock()
			defer mu.Unlock()
			done += len(batch)
			if err != nil {
				r.app.Logger().Warn("deep search screen batch failed; keeping its documents",
					"documents", len(batch),
					slog.Any("error", err),
				)
			} else {
				for _, row := range result.Rows {
					verdicts[row.ID] = row.Verdict
				}
			}
			progress(done)
		}(batch)
	}
	wg.Wait()
	return verdicts
}
