package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/dbutils"

	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/retrieval"
)

// RelatedLinking configures the link_related step. The zero value is off.
type RelatedLinking struct {
	Enabled bool
	// Cosine similarity at or above which two documents are linked on their
	// embeddings alone; 0 links on reference numbers only.
	Threshold float64
	// Nil without embeddings, which leaves reference numbers.
	Chunks retrieval.ChunkSearcher
}

// ponytail: fixed caps on similarity links. Tune with the threshold, or make
// them settings, if the archive's real neighbourhoods turn out wider.
const (
	maxSimilarLinks  = 5
	similarCandidate = 50
)

type LinkRelatedStep struct {
	RelatedLinking
}

func (s *LinkRelatedStep) Name() string { return models.StepLinkRelated }

func (s *LinkRelatedStep) ShouldSkip(state *StepState) (bool, error) {
	if !s.Enabled || state.Document == nil {
		return true, nil
	}
	return state.Document.GetString("duplicate_of") != "", nil
}

// ponytail: like detect_duplicates, a document only finds documents processed
// before it. Each link is found by the later of the two, so with
// WORKER_CONCURRENCY above 1 two documents processed at the same moment can
// miss each other until one is reprocessed.
//
// Only ever adds: a link the owner made stays, and one they removed comes
// back on the next reprocess.
func (s *LinkRelatedStep) Run(ctx context.Context, state *StepState) error {
	// Fresh rather than the job's copy, which a save here would write back
	// whole, over any link the owner made while the job ran.
	doc, err := state.App.FindRecordById("documents", state.Document.Id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStepSoft, err)
	}
	linked, err := linkedDocumentIDs(state.App, doc)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStepSoft, err)
	}
	byReference, err := documentsSharingReferences(state.App, doc)
	if err != nil {
		return fmt.Errorf("%w: reference numbers: %w", ErrStepSoft, err)
	}
	bySimilarity, err := s.similarDocuments(ctx, state.App, doc)
	if err != nil {
		return fmt.Errorf("%w: similarity: %w", ErrStepSoft, err)
	}

	related := doc.GetStringSlice("related")
	limit := maxRelatedDocuments(doc)
	added := 0
	for _, id := range append(byReference, bySimilarity...) {
		if len(related) >= limit {
			break
		}
		if _, ok := linked[id]; ok || id == doc.Id {
			continue
		}
		linked[id] = struct{}{}
		related = append(related, id)
		added++
	}
	state.Logger.Info("related documents",
		"by_reference", len(byReference), "by_similarity", len(bySimilarity), "linked", added)
	if added == 0 {
		return nil
	}
	doc.Set("related", related)
	if err := state.App.Save(doc.IgnoreUnchangedFields(true)); err != nil {
		return fmt.Errorf("%w: save links: %w", ErrStepSoft, err)
	}
	// Without apply_metadata in the job, finishRun saves the job's copy whole,
	// and would put back the related it was loaded with.
	state.Document.Set("related", related)
	return nil
}

func maxRelatedDocuments(doc *core.Record) int {
	if field, ok := doc.Collection().Fields.GetByName("related").(*core.RelationField); ok {
		return field.MaxSelect
	}
	return 0
}

// Both directions: a link is stored on whichever document made it.
func linkedDocumentIDs(app core.App, doc *core.Record) (map[string]struct{}, error) {
	linked := map[string]struct{}{}
	for _, id := range doc.GetStringSlice("related") {
		linked[id] = struct{}{}
	}
	var incoming []string
	err := app.DB().NewQuery(
		"SELECT d.id FROM documents d, " + dbutils.JSONEach("d.related") + " r WHERE r.value = {:id}",
	).Bind(dbx.Params{"id": doc.Id}).Column(&incoming)
	if err != nil {
		return nil, err
	}
	for _, id := range incoming {
		linked[id] = struct{}{}
	}
	return linked, nil
}

// Newest first, so a matter with more documents than related can hold keeps
// the latest ones. A customer number the model should have left out links
// every bill that carries it; the prompt is the guard against that.
func documentsSharingReferences(app core.App, doc *core.Record) ([]string, error) {
	var refs []string
	if err := doc.UnmarshalJSONField("reference_numbers", &refs); err != nil || len(refs) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	var ids []string
	err = app.DB().NewQuery(
		"SELECT DISTINCT d.id, d.created FROM documents d, " + dbutils.JSONEach("d.reference_numbers") + " r" +
			" WHERE d.user = {:user} AND d.id != {:id} AND d.duplicate_of = ''" +
			" AND r.value IN (SELECT value FROM json_each({:refs}))" +
			" ORDER BY d.created DESC",
	).Bind(dbx.Params{"user": doc.GetString("user"), "id": doc.Id, "refs": string(encoded)}).Column(&ids)
	return ids, err
}

// Documents compare by their chunks averaged, one vector each: a single chunk
// matches on boilerplate (a vendor's terms, a letterhead) as readily as on
// substance, and an average scored against single chunks stays low however
// alike two long documents are. The chunk index only nominates candidates.
//
// ponytail: candidates are the documents owning the chunks nearest this one's
// average, so a match whose every chunk ranks outside them is missed. Widen
// similarCandidate if that shows.
func (s *LinkRelatedStep) similarDocuments(ctx context.Context, app core.App, doc *core.Record) ([]string, error) {
	if s.Chunks == nil || s.Threshold <= 0 {
		return nil, nil
	}
	centroid, err := documentCentroid(app, doc.Id)
	if err != nil || centroid == nil {
		return nil, err
	}
	hits, err := s.Chunks.SearchChunks(ctx, retrieval.ChunkQuery{
		Vector: centroid,
		UserID: doc.GetString("user"),
		K:      similarCandidate,
	})
	if err != nil {
		return nil, err
	}

	candidates := make([]any, 0, len(hits))
	for _, hit := range hits {
		if hit.DocumentID != doc.Id {
			candidates = append(candidates, hit.DocumentID)
		}
	}
	var eligible []string
	err = app.DB().Select("id").From("documents").
		Where(dbx.In("id", candidates...)).
		AndWhere(dbx.HashExp{"duplicate_of": ""}).
		Column(&eligible)
	if err != nil {
		return nil, err
	}

	score := map[string]float64{}
	for _, id := range eligible {
		other, err := documentCentroid(app, id)
		if err != nil {
			return nil, err
		}
		score[id] = retrieval.Cosine(centroid, other)
	}
	ids := make([]string, 0, len(score))
	for id, similarity := range score {
		if similarity >= s.Threshold {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return score[ids[i]] > score[ids[j]] })
	if len(ids) > maxSimilarLinks {
		ids = ids[:maxSimilarLinks]
	}
	return ids, nil
}

func documentCentroid(app core.App, documentID string) ([]float32, error) {
	chunks, err := embedstore.Chunks(app.DB(), documentID)
	if err != nil {
		return nil, err
	}
	return meanVector(chunks), nil
}

func meanVector(chunks []embedstore.Chunk) []float32 {
	var sum []float32
	count := 0
	for _, chunk := range chunks {
		if sum == nil && len(chunk.Vector) > 0 {
			sum = make([]float32, len(chunk.Vector))
		}
		if len(chunk.Vector) == 0 || len(chunk.Vector) != len(sum) {
			continue
		}
		for i, v := range chunk.Vector {
			sum[i] += v
		}
		count++
	}
	if count == 0 {
		return nil
	}
	for i := range sum {
		sum[i] /= float32(count)
	}
	return sum
}

// "INV-2024/0042" and "inv 2024 0042" are the same number to a person, so
// they are one to the matcher. A value without a digit is a word, and one
// under five characters matches too much to mean anything.
func normalizeReferences(raw []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range raw {
		var b strings.Builder
		digit := false
		for _, r := range strings.ToUpper(value) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
				digit = digit || unicode.IsDigit(r)
			}
		}
		ref := b.String()
		if _, dup := seen[ref]; dup || !digit || len([]rune(ref)) < 5 {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}
