package appapi

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"lemmary/backend/internal/ai"
)

// The helper model's share of a read.
//
// These are choices about what the research model should have to carry, not
// guesses at any model's context window. A read that stays under both
// thresholds passes through as text, because on a needle question the exact
// wording is what the answer quotes. Past either, the documents are read by
// the helper and the research model gets what they say about the question.
const (
	// distillThresholdBytes is the most text one read may put into the
	// research conversation as-is. About eight thousand tokens.
	distillThresholdBytes = 32000
	// distillMinDocs is the most documents one read may pass through as
	// text, however short they are: past a handful, the model is surveying
	// rather than reading, and notes serve a survey better.
	distillMinDocs = 5

	// helperInputBytes is how much of one document the helper is shown.
	// Helpers are assumed to have windows of 250k tokens or more, so most
	// documents go in whole; only past this is the document excerpted
	// around the question first.
	helperInputBytes = 400_000
	// helperBatchBytes is how much text one helper call carries. Several
	// short documents share a call so a read of twenty letters is a few
	// calls, not twenty.
	helperBatchBytes = 300_000
	// helperConcurrency is how many helper calls run at once.
	helperConcurrency = 4
)

// shouldDistill decides whether a read is small enough to pass through raw.
func shouldDistill(docs []ai.DocumentContent) bool {
	if len(docs) > distillMinDocs {
		return true
	}
	total := 0
	for _, doc := range docs {
		total += len(doc.Text)
	}
	return total > distillThresholdBytes
}

// distillDocuments has the helper read the documents and returns them with
// notes, quotes and values in place of text. A document the helper failed on
// -- the call errored, or the answer left it out -- keeps its text, cut to the
// agent's own excerpt size, so a helper outage costs the saving and not the
// read.
func (r *agentRetriever) distillDocuments(ctx context.Context, question string, fields []ai.SurveyField, docs []ai.DocumentContent) []ai.DocumentContent {
	inputs := make([]ai.DistillDoc, 0, len(docs))
	for _, doc := range docs {
		inputs = append(inputs, ai.DistillDoc{
			ID:            doc.ID,
			Title:         doc.Title,
			DocumentDate:  doc.DocumentDate,
			DocumentType:  doc.DocumentType,
			Correspondent: doc.Correspondent,
			Text:          doc.Text,
			Excerpted:     doc.Excerpted,
		})
	}
	rows, _ := r.distillAll(ctx, question, fields, inputs, nil)

	out := make([]ai.DocumentContent, 0, len(docs))
	for _, doc := range docs {
		row, ok := rows[doc.ID]
		if !ok {
			if len(doc.Text) > focusExcerptBytes {
				text, omitted := excerptDocument(doc.ID, doc.Text, question, r.focusRanker(ctx), focusExcerptBytes)
				doc.Text = text
				doc.Excerpted = true
				doc.PassagesOmitted = omitted
			}
			out = append(out, doc)
			continue
		}
		doc.Text = ""
		doc.Excerpted = false
		doc.PassagesOmitted = 0
		doc.FocusUsed = ""
		doc.Distilled = true
		doc.Relevant = row.Relevant
		doc.Notes = row.Notes
		doc.Quotes = row.Quotes
		doc.Values = row.Values
		out = append(out, doc)
	}
	return out
}

// distillAll runs the helper over every document, packed into batches by
// size and run helperConcurrency at a time. It returns the rows by document
// id and the summed usage. Failed batches are logged and their documents are
// simply absent from the result. progress, when given, is called with the
// running count of documents finished.
func (r *agentRetriever) distillAll(ctx context.Context, question string, fields []ai.SurveyField, docs []ai.DistillDoc, progress func(done int)) (map[string]ai.DistillRow, ai.Usage) {
	batches := packDistillBatches(docs, helperBatchBytes)

	var (
		mu    sync.Mutex
		rows  = make(map[string]ai.DistillRow, len(docs))
		usage ai.Usage
		done  int
		wg    sync.WaitGroup
		sem   = make(chan struct{}, helperConcurrency)
	)
	for _, batch := range batches {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(batch []ai.DistillDoc) {
			defer wg.Done()
			defer func() { <-sem }()
			result, err := r.helper.Distill(ctx, ai.DistillRequest{Question: question, Fields: fields, Docs: batch})
			mu.Lock()
			defer mu.Unlock()
			usage.Add(result.Usage)
			done += len(batch)
			if err != nil {
				r.app.Logger().Warn("deep search helper batch failed; passing documents through",
					"documents", len(batch),
					slog.Any("error", err),
				)
			} else {
				for _, row := range result.Rows {
					rows[row.ID] = row
				}
			}
			if progress != nil {
				progress(done)
			}
		}(batch)
	}
	wg.Wait()

	r.app.Logger().Info("deep search helper run",
		"documents", len(docs),
		"batches", len(batches),
		"rows", len(rows),
		"prompt_tokens", usage.Prompt,
		"cached_tokens", usage.Cached,
		"completion_tokens", usage.Completion,
	)
	return rows, usage
}

// packDistillBatches groups documents so no call carries more than
// budgetBytes of text. Documents are taken in order; a single document over
// the budget travels alone. Order is preserved so batches are deterministic
// for the same input.
func packDistillBatches(docs []ai.DistillDoc, budgetBytes int) [][]ai.DistillDoc {
	var batches [][]ai.DistillDoc
	var current []ai.DistillDoc
	size := 0
	for _, doc := range docs {
		n := len(doc.Text)
		if len(current) > 0 && size+n > budgetBytes {
			batches = append(batches, current)
			current = nil
			size = 0
		}
		current = append(current, doc)
		size += n
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// numberField reports whether a survey field is numeric, by declared type.
func numberField(f ai.SurveyField) bool {
	return strings.EqualFold(strings.TrimSpace(f.Type), "number")
}
