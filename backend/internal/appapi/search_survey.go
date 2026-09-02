package appapi

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/retrieval"
	"lemmary/backend/internal/strutil"
)

// survey backs the agent's survey_documents tool: select documents, have the
// helper read every one of them for the question, and return a row each.
//
// The selection is the same retrieval a search runs, just kept past the
// search's page: fused ranking, then hydration until the cap. The reading is
// the helper's, batched and concurrent, and none of the text reaches the
// research model -- rows do. Number fields are added up here, because a model
// summing three hundred figures gets some of them wrong and the server does
// not.
func (r *agentRetriever) survey(ctx context.Context, args ai.SurveyArgs, progress func(done, total int)) (ai.SurveyResult, error) {
	if r.helper == nil {
		return ai.SurveyResult{}, fmt.Errorf("no helper model is configured for surveys")
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return ai.SurveyResult{}, fmt.Errorf("question is required")
	}
	limit := args.MaxDocuments
	if limit <= 0 {
		limit = ai.DefaultSurveyDocuments
	}
	if limit > ai.MaxSurveyDocuments {
		limit = ai.MaxSurveyDocuments
	}

	ids, candidates, err := r.surveyCandidates(ctx, args, limit)
	if err != nil {
		return ai.SurveyResult{}, err
	}
	result := ai.SurveyResult{Candidates: candidates, Skipped: max(candidates-len(ids), 0)}
	if len(ids) == 0 {
		return result, nil
	}

	// Load what the helper will read: the whole text up to its cap, an
	// excerpt around the question past it. Hits are built at the same time
	// for the rows' titles and dates and for the result list.
	docs := make([]ai.DistillDoc, 0, len(ids))
	hits := make(map[string]ai.DocumentHit, len(ids))
	rank := r.focusRanker(ctx)
	for _, id := range ids {
		record, err := r.app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		if r.userID != "" && record.GetString("user") != r.userID {
			continue
		}
		full := record.GetString("ocr_text")
		doc := ai.DistillDoc{
			ID:            record.Id,
			Title:         strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate:  truncateDate(record.GetString("document_date")),
			DocumentType:  relatedName(r.app, "document_types", record.GetString("document_type")),
			Correspondent: relatedName(r.app, "correspondents", record.GetString("correspondent")),
			Text:          full,
		}
		if len(full) > helperInputBytes {
			doc.Text, _ = excerptDocument(record.Id, full, question, rank, helperInputBytes)
			doc.Excerpted = true
		}
		docs = append(docs, doc)
		hits[record.Id] = ai.DocumentHit{
			ID:            record.Id,
			Title:         doc.Title,
			DocumentDate:  doc.DocumentDate,
			Summary:       strutil.TruncateRunes(strutil.FirstNonEmpty(record.GetString("summary"), record.GetString("purpose")), maxSummaryLen),
			OCRSnippet:    ocrSnippet(full, question),
			DocumentType:  doc.DocumentType,
			Correspondent: doc.Correspondent,
			Tags:          documentTagNames(r.app, record),
		}
	}
	if progress != nil {
		progress(0, len(docs))
	}

	rows, _ := r.distillAll(ctx, question, args.Fields, docs, func(done int) {
		if progress != nil {
			progress(done, len(docs))
		}
	})

	result.Surveyed = len(docs)
	result.Rows = make([]ai.SurveyRow, 0, len(docs))
	for _, doc := range docs {
		hit := hits[doc.ID]
		row, ok := rows[doc.ID]
		if !ok {
			result.Failed++
			continue
		}
		out := ai.SurveyRow{
			ID:           doc.ID,
			Title:        hit.Title,
			DocumentDate: hit.DocumentDate,
			Relevant:     row.Relevant,
			Values:       row.Values,
			Missing:      row.Missing,
		}
		if row.Relevant {
			out.Notes = row.Notes
			if len(row.Quotes) > 0 {
				out.Quote = strutil.TruncateRunes(row.Quotes[0], maxSnippetLen*2)
			}
			if row.Notes != "" {
				hit.OCRSnippet = strutil.TruncateRunes(row.Notes, maxSnippetLen)
			}
			result.Documents = append(result.Documents, hit)
		}
		result.Rows = append(result.Rows, out)
	}
	ai.SortSurveyRows(result.Rows)
	result.Totals, result.Missing = surveyTotals(args.Fields, result.Rows)
	return result, nil
}

// surveyCandidates picks the documents to survey: the given ids, or the fused
// ranking for the query, cut to limit. candidates is how many there were
// before the cut.
func (r *agentRetriever) surveyCandidates(ctx context.Context, args ai.SurveyArgs, limit int) ([]string, int, error) {
	if len(args.IDs) > 0 {
		ids := args.IDs
		total := len(ids)
		if len(ids) > limit {
			ids = ids[:limit]
		}
		return ids, total, nil
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return nil, 0, fmt.Errorf("query or ids is required")
	}
	if r.idx == nil || !r.idx.Ready() {
		return nil, 0, fmt.Errorf("search index is not ready")
	}
	ftQuery, unresolved, err := r.resolveFilters(args.SearchArgs())
	if err != nil {
		return nil, 0, err
	}
	if len(unresolved) > 0 {
		return nil, 0, fmt.Errorf("no such %s", strings.Join(unresolved, "; "))
	}
	cands, err := r.candidates(ctx, ftQuery, query, limit)
	if err != nil {
		return nil, 0, err
	}
	ids := retrieval.IDs(cands.fused)
	total := len(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, total, nil
}

// surveyTotals adds up every number field over the relevant rows, per
// currency, and counts the relevant rows that had no value for each field.
func surveyTotals(fields []ai.SurveyField, rows []ai.SurveyRow) ([]ai.SurveyTotal, map[string]int) {
	type acc struct {
		count    int
		sum      float64
		min, max float64
	}
	totals := map[string]*acc{}
	missing := map[string]int{}
	var order []string

	for _, f := range fields {
		if !numberField(f) {
			continue
		}
		name := strings.TrimSpace(f.Name)
		for _, row := range rows {
			if !row.Relevant {
				continue
			}
			raw, ok := row.Values[name]
			if !ok {
				missing[name]++
				continue
			}
			v, ok := parseNumber(raw)
			if !ok {
				missing[name]++
				continue
			}
			currency := strings.ToUpper(strings.TrimSpace(row.Values[name+"_currency"]))
			key := name + "\x00" + currency
			a, seen := totals[key]
			if !seen {
				a = &acc{min: v, max: v}
				totals[key] = a
				order = append(order, key)
			}
			a.count++
			a.sum += v
			a.min = math.Min(a.min, v)
			a.max = math.Max(a.max, v)
		}
	}

	sort.Strings(order)
	out := make([]ai.SurveyTotal, 0, len(order))
	for _, key := range order {
		a := totals[key]
		name, currency, _ := strings.Cut(key, "\x00")
		out = append(out, ai.SurveyTotal{
			Field:    name,
			Currency: currency,
			Count:    a.count,
			Sum:      round2(a.sum),
			Avg:      round2(a.sum / float64(a.count)),
			Min:      round2(a.min),
			Max:      round2(a.max),
		})
	}
	if len(missing) == 0 {
		missing = nil
	}
	return out, missing
}

// parseNumber reads what a model calls a number: "1.234,56", "1,234.56",
// "EUR 1234.56", "-12". When both separators appear the later one is the
// decimal point. A lone comma or dot followed by exactly three digits is read
// as a thousands group only when it is a comma -- "1,234" -- since "1.234"
// with one dot is far more often a decimal in the sources this reads.
func parseNumber(raw string) (float64, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',', r == '-':
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" || s == "-" {
		return 0, false
	}
	dots, commas := strings.Count(s, "."), strings.Count(s, ",")
	switch {
	case dots > 0 && commas > 0:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case commas > 1:
		s = strings.ReplaceAll(s, ",", "")
	case commas == 1:
		if i := strings.Index(s, ","); len(s)-i-1 == 3 {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.Replace(s, ",", ".", 1)
		}
	case dots > 1:
		s = strings.ReplaceAll(s, ".", "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
