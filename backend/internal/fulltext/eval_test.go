package fulltext

import (
	"sort"
	"strings"
	"testing"

	"lemmary/backend/internal/retrieval/testdata"
)

// This file is the lexical half of the retrieval evaluation: a real Bleve index
// over the synthetic corpus, scored on the queries in testdata.Cases.
//
// It exists because "did the search get better" is otherwise unanswerable. Unit
// tests pin behaviour one query at a time and pass just as happily when the
// ranking as a whole has got worse; this measures the whole set at once, so a
// change that fixes one query by breaking three is visible.
//
// The floors below were calibrated on the first run of this harness and sit a
// little under what was measured, so ordinary noise does not fail the build
// while a real regression does. Measured at calibration:
//
//	strict:  recall@5 0.511  MRR 0.522
//	relaxed: recall@5 0.772  MRR 0.783
//
// The gap between those two lines is what relaxing bought: typos, inflections
// and queries with one keyword the document does not carry. What neither
// reaches is the three paraphrases and the two compounds — a term index cannot
// match words that are not there, and those five cases are the headroom the
// dense path exists to take.
//
// Raise them when a change raises the numbers; a floor that is never revised
// stops measuring anything. Never lower one to make a change pass.
const (
	strictRecallFloor  = 0.48
	strictMRRFloor     = 0.49
	relaxedRecallFloor = 0.74
	relaxedMRRFloor    = 0.75
)

// evalK is the cut-off recall is measured at: what an agent actually looks at
// out of one search_documents call.
const evalK = 5

func evalIndex(t *testing.T) *Index {
	t.Helper()
	idx := testIndex(t)
	for _, doc := range testdata.Documents() {
		fields := map[string]any{
			FieldUser:              doc.User,
			FieldProcessingStatus:  "completed",
			FieldTitle:             doc.Title,
			FieldTitleOriginal:     doc.TitleOriginal,
			FieldPurpose:           doc.Purpose,
			FieldSummary:           doc.Summary,
			FieldOCRText:           doc.Text,
			FieldTagNames:          strings.Join(doc.Tags, " "),
			FieldDocumentTypeName:  doc.DocumentType,
			FieldCorrespondentName: doc.Correspondent,
			FieldDocumentType:      doc.DocumentType,
			FieldCorrespondent:     doc.Correspondent,
			FieldTags:              doc.Tags,
			FieldDocumentDate:      doc.Date,
			FieldAll: strings.Join([]string{
				doc.Title, doc.TitleOriginal, doc.Purpose, doc.Summary,
				strings.Join(doc.Tags, " "), doc.DocumentType, doc.Correspondent, doc.Text,
			}, " "),
		}
		mustPut(t, idx, doc.ID, fields)
	}
	return idx
}

func caseQuery(c testdata.Case, relaxed bool) Query {
	q := Query{
		Text:     c.Query,
		UserID:   "u1",
		Relaxed:  relaxed,
		Limit:    evalK,
		DateFrom: c.Filters.DateFrom,
		DateTo:   c.Filters.DateTo,
	}
	if c.Filters.DocumentType != "" {
		q.DocumentTypeIDs = []string{c.Filters.DocumentType}
	}
	if c.Filters.Correspondent != "" {
		q.CorrespondentIDs = []string{c.Filters.Correspondent}
	}
	q.TagIDs = c.Filters.Tags
	return q
}

// score reports mean recall@k and mean reciprocal rank over the cases, plus the
// per-case recall so a failure can name what it lost.
func score(t *testing.T, idx *Index, cases []testdata.Case, relaxed bool) (recall, mrr float64, perCase map[string]float64) {
	t.Helper()
	perCase = map[string]float64{}
	for _, c := range cases {
		ids := searchIDs(t, idx, caseQuery(c, relaxed))
		if len(ids) > evalK {
			ids = ids[:evalK]
		}
		found := 0
		best := 0
		for _, want := range c.Want {
			for rank, id := range ids {
				if id != want {
					continue
				}
				found++
				if best == 0 || rank+1 < best {
					best = rank + 1
				}
				break
			}
		}
		caseRecall := float64(found) / float64(len(c.Want))
		perCase[c.Name] = caseRecall
		recall += caseRecall
		if best > 0 {
			mrr += 1 / float64(best)
		}
	}
	n := float64(len(cases))
	return recall / n, mrr / n, perCase
}

func TestSearchEvalMeetsFloors(t *testing.T) {
	idx := evalIndex(t)
	cases := testdata.Cases()

	strictRecall, strictMRR, strictPer := score(t, idx, cases, false)
	relaxedRecall, relaxedMRR, relaxedPer := score(t, idx, cases, true)

	t.Logf("strict:  recall@%d %.3f  MRR %.3f", evalK, strictRecall, strictMRR)
	t.Logf("relaxed: recall@%d %.3f  MRR %.3f", evalK, relaxedRecall, relaxedMRR)
	for _, name := range sortedNames(relaxedPer) {
		t.Logf("  %-34s strict %.2f  relaxed %.2f", name, strictPer[name], relaxedPer[name])
	}

	if strictRecall < strictRecallFloor {
		t.Errorf("strict recall@%d %.3f below the %.2f floor", evalK, strictRecall, strictRecallFloor)
	}
	if strictMRR < strictMRRFloor {
		t.Errorf("strict MRR %.3f below the %.2f floor", strictMRR, strictMRRFloor)
	}
	if relaxedRecall < relaxedRecallFloor {
		t.Errorf("relaxed recall@%d %.3f below the %.2f floor", evalK, relaxedRecall, relaxedRecallFloor)
	}
	if relaxedMRR < relaxedMRRFloor {
		t.Errorf("relaxed MRR %.3f below the %.2f floor", relaxedMRR, relaxedMRRFloor)
	}

	// The point of relaxing: it may reorder, it may not lose documents.
	if relaxedRecall < strictRecall {
		t.Errorf("relaxed recall %.3f is below strict recall %.3f — relaxing must not cost recall",
			relaxedRecall, strictRecall)
	}
	for _, name := range sortedNames(strictPer) {
		if relaxedPer[name] < strictPer[name] {
			t.Errorf("%q: relaxed recall %.2f below strict %.2f", name, relaxedPer[name], strictPer[name])
		}
	}
}

// TestSearchEvalRelaxedRescuesMissedQueries names the classes strict matching
// cannot serve at all, so the reason relaxed exists stays measured rather than
// only asserted in a comment.
func TestSearchEvalRelaxedRescuesMissedQueries(t *testing.T) {
	idx := evalIndex(t)

	rescued := map[testdata.Kind]int{}
	for _, c := range testdata.Cases() {
		strict := searchIDs(t, idx, caseQuery(c, false))
		relaxed := searchIDs(t, idx, caseQuery(c, true))
		for _, want := range c.Want {
			if !containsID(strict, want) && containsID(relaxed, want) {
				rescued[c.Kind]++
			}
		}
	}
	t.Logf("rescued by relaxing: %v", rescued)

	// Fuzzy matching is what rescues a typo; min-should-match is what rescues
	// a query where one guessed keyword is simply not in the document.
	if rescued[testdata.KindTypo] == 0 {
		t.Error("no typo query was rescued by fuzzy matching")
	}
	if rescued[testdata.KindMorphology] == 0 {
		t.Error("no inflected query was rescued")
	}
	if rescued[testdata.KindExact] == 0 {
		t.Error("no partly-matching query was rescued by min-should-match")
	}

	// Paraphrases stay unrescued here, and that is the honest result: a query
	// of eleven words, most of them function words, cannot be served by term
	// matching however it is relaxed. It is what the dense path is for, and
	// the floors above are set knowing these three cases score zero.
	if rescued[testdata.KindParaphrase] != 0 {
		t.Logf("a paraphrase was rescued lexically; revisit the floors")
	}
}

// Scoping is not negotiable, whatever the ranking does: the corpus holds one
// document belonging to another account, with the same wording as one of ours.
func TestSearchEvalKeepsOwnerScoping(t *testing.T) {
	idx := evalIndex(t)
	for _, relaxed := range []bool{false, true} {
		ids := searchIDs(t, idx, Query{Text: "Rechnung Badezimmer", UserID: "u1", Relaxed: relaxed, Limit: 20})
		if containsID(ids, "other-owner") {
			t.Fatalf("relaxed=%v leaked another account's document: %v", relaxed, ids)
		}
	}
}

func sortedNames(scores map[string]float64) []string {
	names := make([]string, 0, len(scores))
	for name := range scores {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
