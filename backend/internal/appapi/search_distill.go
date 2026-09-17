package appapi

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"lemmary/backend/internal/ai"
)

// The helper model's share of a read: choices about what the research model
// should carry, not guesses at any model's context window. Under both
// thresholds a read passes through as text, since on a needle question the
// exact wording is what the answer quotes.
const (
	// distillThresholdBytes is the most text one read may put into the
	// research conversation as-is. About eight thousand tokens.
	distillThresholdBytes = 32000
	// distillMinDocs is the most documents one read may pass through as text,
	// however short: past a handful the model is surveying, and notes serve
	// a survey better.
	distillMinDocs = 5

	// helperInputBytes assumes a helper window of 250k tokens or more, so
	// most documents go in whole; only past this is one excerpted first.
	helperInputBytes = 400_000
	// helperBatchBytes lets several short documents share a call, so a read
	// of twenty letters is a few calls, not twenty.
	helperBatchBytes  = 300_000
	helperConcurrency = 4
)

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

// distillDocuments returns documents with notes, quotes and values in place of
// text. One the helper failed on keeps its text, cut to the agent's excerpt
// size, so a helper outage costs the saving and not the read.
func (r *agentRetriever) distillDocuments(ctx context.Context, question string, fields []ai.SurveyField, docs []ai.DocumentContent) []ai.DocumentContent {
	inputs := make([]ai.DistillDoc, 0, len(docs))
	chunkCounts := make(map[string]int, len(docs))
	for _, doc := range docs {
		// Markers only on text read whole: an excerpt's offsets are not the
		// document's, so its chunk numbers would point at the wrong bytes.
		text := doc.Text
		if !doc.Excerpted {
			text = markChunks(doc.Text)
			chunkCounts[doc.ID] = len(splitChunks(doc.Text))
		}
		inputs = append(inputs, ai.DistillDoc{
			ID:            doc.ID,
			Title:         doc.Title,
			DocumentDate:  doc.DocumentDate,
			DocumentType:  doc.DocumentType,
			Correspondent: doc.Correspondent,
			Text:          text,
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
		// Only text the helper saw with markers yields chunk numbers; on an
		// offset-marked excerpt any integers it wrote point at nothing.
		if count, marked := chunkCounts[doc.ID]; marked {
			doc.Chunks = row.Chunks
			doc.ChunkCount = count
		}
		out = append(out, doc)
	}
	return out
}

// distillAll returns the rows by document id and the summed usage. Failed
// batches are logged and their documents are simply absent from the result.
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

// packDistillBatches keeps each call under budgetBytes; a single document over
// it travels alone. Order is preserved so batches are deterministic.
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

func numberField(f ai.SurveyField) bool {
	return strings.EqualFold(strings.TrimSpace(f.Type), "number")
}
