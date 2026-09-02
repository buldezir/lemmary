package appapi

import (
	"context"
	"testing"

	"lemmary/backend/internal/ai"
)

func TestSurveyTotalsSumPerCurrencyAndCountMissing(t *testing.T) {
	fields := []ai.SurveyField{{Name: "amount", Type: "number"}, {Name: "vendor", Type: "string"}}
	rows := []ai.SurveyRow{
		{ID: "a", Relevant: true, Values: map[string]string{"amount": "1.234,50", "amount_currency": "EUR"}},
		{ID: "b", Relevant: true, Values: map[string]string{"amount": "100", "amount_currency": "eur"}},
		{ID: "c", Relevant: true, Values: map[string]string{"amount": "$ 40.25", "amount_currency": "USD"}},
		{ID: "d", Relevant: true, Values: map[string]string{"vendor": "ACME"}},
		{ID: "e", Relevant: false, Values: map[string]string{"amount": "999"}},
	}
	totals, missing := surveyTotals(fields, rows)
	if len(totals) != 2 {
		t.Fatalf("totals = %+v, want one per currency", totals)
	}
	eur, usd := totals[0], totals[1]
	if eur.Currency != "EUR" || eur.Count != 2 || eur.Sum != 1334.5 || eur.Avg != 667.25 || eur.Min != 100 || eur.Max != 1234.5 {
		t.Fatalf("EUR total = %+v", eur)
	}
	if usd.Currency != "USD" || usd.Count != 1 || usd.Sum != 40.25 {
		t.Fatalf("USD total = %+v", usd)
	}
	if missing["amount"] != 1 {
		t.Fatalf("missing = %v, want the one relevant row without an amount", missing)
	}
	if _, ok := missing["vendor"]; ok {
		t.Fatalf("string fields are not totalled and should not be counted missing: %v", missing)
	}
}

func TestParseNumberReadsWhatModelsWrite(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1234.56", 1234.56, true},
		{"1.234,56", 1234.56, true},
		{"1,234.56", 1234.56, true},
		{"1,234", 1234, true},
		{"12,5", 12.5, true},
		{"1.234.567", 1234567, true},
		{"EUR 99.90", 99.9, true},
		{"-12", -12, true},
		{"n/a", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseNumber(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseNumber(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestSurveyReadsEveryCandidateAndReturnsRows runs a survey over the hybrid
// fixture: both documents are candidates for the query, the helper reads
// both, the rows carry the helper's values and the totals add them up.
func TestSurveyReadsEveryCandidateAndReturnsRows(t *testing.T) {
	r := hybridRetriever(t, nil)
	helper := &fakeHelper{values: map[string]map[string]string{
		"lexical": {"premium": "240", "premium_currency": "EUR"},
		"dense":   {"premium": "240", "premium_currency": "EUR"},
	}}
	r.helper = helper

	var progress [][2]int
	result, err := r.survey(context.Background(), ai.SurveyArgs{
		Query:    "insurance premium",
		Question: "What is the yearly premium?",
		Fields:   []ai.SurveyField{{Name: "premium", Type: "number"}},
	}, func(done, total int) { progress = append(progress, [2]int{done, total}) })
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if result.Candidates != 2 || result.Surveyed != 2 || result.Skipped != 0 || result.Failed != 0 {
		t.Fatalf("result counts = %+v", result)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows = %+v", result.Rows)
	}
	for _, row := range result.Rows {
		if !row.Relevant || row.Notes == "" || row.Quote == "" || row.Values["premium"] != "240" || row.Title == "" {
			t.Fatalf("row = %+v", row)
		}
	}
	if len(result.Totals) != 1 || result.Totals[0].Sum != 480 || result.Totals[0].Currency != "EUR" {
		t.Fatalf("totals = %+v", result.Totals)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("relevant rows should come back as hits: %+v", result.Documents)
	}
	if len(progress) == 0 || progress[len(progress)-1] != [2]int{2, 2} {
		t.Fatalf("progress = %v, want to end at 2 of 2", progress)
	}
}

func TestSurveyHonoursTheCapAndReportsTheRest(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.helper = &fakeHelper{}
	result, err := r.survey(context.Background(), ai.SurveyArgs{
		Query:        "insurance premium",
		Question:     "premium?",
		MaxDocuments: 1,
	}, nil)
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if result.Surveyed != 1 || result.Skipped != 1 || result.Candidates != 2 {
		t.Fatalf("result = %+v, want one surveyed and one skipped", result)
	}
}

func TestSurveyByIDsSkipsTheSearch(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.idx = nil // no index: ids must be enough
	r.helper = &fakeHelper{}
	result, err := r.survey(context.Background(), ai.SurveyArgs{IDs: []string{"dense"}, Question: "premium?"}, nil)
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if result.Surveyed != 1 || result.Rows[0].ID != "dense" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSurveyRefusesAnUnresolvedFilter(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.helper = &fakeHelper{}
	_, err := r.survey(context.Background(), ai.SurveyArgs{Query: "premium", Question: "q", Tags: []string{"nonexistent"}}, nil)
	if err == nil {
		t.Fatal("a tag that resolves to nothing should be an error, not a survey of the whole archive")
	}
}

func TestSurveyWithoutAHelperIsRefused(t *testing.T) {
	r := hybridRetriever(t, nil)
	if _, err := r.survey(context.Background(), ai.SurveyArgs{Query: "x", Question: "q"}, nil); err == nil {
		t.Fatal("no helper, no survey")
	}
}
