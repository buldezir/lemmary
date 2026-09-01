package retrieval

import "sort"

// RRFConstant is the k of reciprocal rank fusion. 60 is the value the original
// paper settled on and every later comparison has kept: large enough that the
// top of a list is not allowed to dominate, small enough that rank 1 still
// clearly beats rank 10.
const RRFConstant = 60.0

// Ranked is one entry of a ranked list: an id and the score that ordered it.
type Ranked struct {
	ID    string
	Score float64
}

// RRF fuses ranked lists by reciprocal rank: an id scores Σ 1/(60+rank) over
// the lists it appears in, ranks being 1-based and taken in the order given.
//
// Scores are deliberately not read. The lists come from different retrievers —
// BM25 and cosine similarity are not on the same scale and normalising them
// against each other is guesswork — so only the positions are comparable.
//
// Ties are broken by how many lists found the id, then by its rank in the
// earliest list that did, then by id, so the output is a total order and two
// runs over the same input agree.
func RRF(lists ...[]Ranked) []Ranked {
	type entry struct {
		id        string
		score     float64
		lists     int
		firstList int
		firstRank int
	}

	byID := map[string]*entry{}
	order := make([]*entry, 0)

	for listIdx, list := range lists {
		seen := map[string]struct{}{}
		rank := 0
		for _, item := range list {
			if item.ID == "" {
				continue
			}
			// A list that repeats an id must not pay for it twice.
			if _, dup := seen[item.ID]; dup {
				continue
			}
			seen[item.ID] = struct{}{}
			rank++

			e, ok := byID[item.ID]
			if !ok {
				e = &entry{id: item.ID, firstList: listIdx, firstRank: rank}
				byID[item.ID] = e
				order = append(order, e)
			}
			e.score += 1 / (RRFConstant + float64(rank))
			e.lists++
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.lists != b.lists {
			return a.lists > b.lists
		}
		if a.firstList != b.firstList {
			return a.firstList < b.firstList
		}
		if a.firstRank != b.firstRank {
			return a.firstRank < b.firstRank
		}
		return a.id < b.id
	})

	out := make([]Ranked, 0, len(order))
	for _, e := range order {
		out = append(out, Ranked{ID: e.id, Score: e.score})
	}
	return out
}

// GroupChunks collapses chunk hits to documents: the returned list orders
// documents by their best chunk, and the map holds up to perDoc chunks per
// document, best first.
//
// Chunk-level scores are comparable here because they all come from the same
// retriever, which is why this ranks by best score rather than by rank.
func GroupChunks(hits []ChunkHit, perDoc int) ([]Ranked, map[string][]ChunkHit) {
	if perDoc <= 0 {
		perDoc = 1
	}
	byDoc := map[string][]ChunkHit{}
	order := make([]string, 0)
	best := map[string]float64{}

	for _, hit := range hits {
		if hit.DocumentID == "" {
			continue
		}
		if _, ok := byDoc[hit.DocumentID]; !ok {
			order = append(order, hit.DocumentID)
			best[hit.DocumentID] = hit.Score
		} else if hit.Score > best[hit.DocumentID] {
			best[hit.DocumentID] = hit.Score
		}
		byDoc[hit.DocumentID] = append(byDoc[hit.DocumentID], hit)
	}

	for id, docHits := range byDoc {
		sort.SliceStable(docHits, func(i, j int) bool {
			if docHits[i].Score != docHits[j].Score {
				return docHits[i].Score > docHits[j].Score
			}
			return docHits[i].Ord < docHits[j].Ord
		})
		if len(docHits) > perDoc {
			docHits = docHits[:perDoc]
		}
		byDoc[id] = docHits
	}

	// Stable on the input order so two documents whose best chunk scored the
	// same keep the retriever's own ordering.
	sort.SliceStable(order, func(i, j int) bool { return best[order[i]] > best[order[j]] })

	docs := make([]Ranked, 0, len(order))
	for _, id := range order {
		docs = append(docs, Ranked{ID: id, Score: best[id]})
	}
	return docs, byDoc
}

// Rank builds a ranked list from ids already in order.
func Rank(ids []string) []Ranked {
	out := make([]Ranked, 0, len(ids))
	for i, id := range ids {
		out = append(out, Ranked{ID: id, Score: 1 / (RRFConstant + float64(i+1))})
	}
	return out
}

// IDs projects a ranked list back to bare ids.
func IDs(ranked []Ranked) []string {
	out := make([]string, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.ID)
	}
	return out
}
