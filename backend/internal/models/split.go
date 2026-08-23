package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SuggestedPart is one detected sub-document inside a multi-document PDF.
// From and To are inclusive 1-based page numbers.
type SuggestedPart struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Title string `json:"title,omitempty"`
}

// SplitSuggestion is the raw boundary proposal returned by the model.
type SplitSuggestion struct {
	Parts []SuggestedPart `json:"parts"`
}

// ParseSplitSuggestion decodes a model reply into a boundary proposal. It
// reuses the extraction normalizer, so fenced JSON and reasoning preambles are
// tolerated here for the same reasons they are there.
func ParseSplitSuggestion(raw string) (*SplitSuggestion, error) {
	normalized := normalizeExtractionJSON(raw)

	var suggestion SplitSuggestion
	if err := json.Unmarshal([]byte(normalized), &suggestion); err != nil {
		return nil, fmt.Errorf("invalid split JSON: %w", err)
	}
	return &suggestion, nil
}

// Normalize turns a proposal into a contiguous cover of pages 1..pageCount.
//
// A model answer cannot be trusted to be sorted, in range, gap-free or
// overlap-free, and the split API only accepts an exact cover. Rather than
// repairing ranges pairwise, only the cut positions the proposal implies are
// kept and the parts are rebuilt from them — any set of cuts describes a valid
// cover, so the result is always acceptable. An unusable proposal degrades to a
// single part spanning the whole file.
func (s *SplitSuggestion) Normalize(pageCount int) []SuggestedPart {
	if pageCount < 1 {
		return nil
	}

	cuts := map[int]struct{}{}
	titleByEnd := map[int]string{}
	if s != nil {
		for _, part := range s.Parts {
			if part.From < 1 || part.To < part.From || part.From > pageCount {
				continue
			}
			end := min(part.To, pageCount)
			if title := strings.TrimSpace(part.Title); title != "" {
				if _, seen := titleByEnd[end]; !seen {
					titleByEnd[end] = title
				}
			}
			if end < pageCount {
				cuts[end] = struct{}{}
			}
		}
	}

	boundaries := make([]int, 0, len(cuts)+1)
	for cut := range cuts {
		boundaries = append(boundaries, cut)
	}
	sort.Ints(boundaries)
	boundaries = append(boundaries, pageCount)

	parts := make([]SuggestedPart, 0, len(boundaries))
	from := 1
	for _, to := range boundaries {
		parts = append(parts, SuggestedPart{From: from, To: to, Title: titleByEnd[to]})
		from = to + 1
	}
	return parts
}
