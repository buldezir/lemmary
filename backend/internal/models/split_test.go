package models

import "testing"

func TestParseSplitSuggestionTolerantOfModelWrapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"plain", `{"parts":[{"from":1,"to":2,"title":"Invoice"}]}`},
		{"fenced", "```json\n{\"parts\":[{\"from\":1,\"to\":2,\"title\":\"Invoice\"}]}\n```"},
		{"reasoning preamble", "<think>page 3 starts a new letterhead</think>\n{\"parts\":[{\"from\":1,\"to\":2,\"title\":\"Invoice\"}]}"},
		{"prose preamble", "Here are the parts:\n{\"parts\":[{\"from\":1,\"to\":2,\"title\":\"Invoice\"}]}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suggestion, err := ParseSplitSuggestion(tc.raw)
			if err != nil {
				t.Fatalf("ParseSplitSuggestion() error: %v", err)
			}
			if len(suggestion.Parts) != 1 {
				t.Fatalf("expected 1 part, got %d", len(suggestion.Parts))
			}
			if suggestion.Parts[0].To != 2 || suggestion.Parts[0].Title != "Invoice" {
				t.Fatalf("unexpected part %+v", suggestion.Parts[0])
			}
		})
	}
}

func TestParseSplitSuggestionRejectsNonJSON(t *testing.T) {
	t.Parallel()

	if _, err := ParseSplitSuggestion("I could not tell where the documents start."); err == nil {
		t.Fatal("expected an error for a reply carrying no JSON")
	}
}

func TestSplitSuggestionNormalize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		parts     []SuggestedPart
		pageCount int
		want      []SuggestedPart
	}{
		{
			name:      "already an exact cover",
			parts:     []SuggestedPart{{From: 1, To: 1, Title: "Invoice"}, {From: 2, To: 4, Title: "Statement"}},
			pageCount: 4,
			want:      []SuggestedPart{{From: 1, To: 1, Title: "Invoice"}, {From: 2, To: 4, Title: "Statement"}},
		},
		{
			name:      "unsorted parts",
			parts:     []SuggestedPart{{From: 3, To: 5}, {From: 1, To: 2}},
			pageCount: 5,
			want:      []SuggestedPart{{From: 1, To: 2}, {From: 3, To: 5}},
		},
		{
			name:      "gap is absorbed by the following part",
			parts:     []SuggestedPart{{From: 1, To: 2}, {From: 5, To: 6}},
			pageCount: 6,
			want:      []SuggestedPart{{From: 1, To: 2}, {From: 3, To: 6}},
		},
		{
			name:      "overlap collapses to the earlier boundary",
			parts:     []SuggestedPart{{From: 1, To: 3}, {From: 2, To: 5}},
			pageCount: 5,
			want:      []SuggestedPart{{From: 1, To: 3}, {From: 4, To: 5}},
		},
		{
			name:      "trailing pages the model forgot are covered",
			parts:     []SuggestedPart{{From: 1, To: 2}},
			pageCount: 6,
			want:      []SuggestedPart{{From: 1, To: 2}, {From: 3, To: 6}},
		},
		{
			name:      "out of range parts are dropped",
			parts:     []SuggestedPart{{From: 0, To: 2}, {From: 7, To: 9}, {From: 2, To: 3}},
			pageCount: 4,
			want:      []SuggestedPart{{From: 1, To: 3}, {From: 4, To: 4}},
		},
		{
			name:      "a part running past the end is clamped",
			parts:     []SuggestedPart{{From: 1, To: 2}, {From: 3, To: 99, Title: "Contract"}},
			pageCount: 4,
			want:      []SuggestedPart{{From: 1, To: 2}, {From: 3, To: 4, Title: "Contract"}},
		},
		{
			name:      "no usable parts leaves the file whole",
			parts:     nil,
			pageCount: 3,
			want:      []SuggestedPart{{From: 1, To: 3}},
		},
		{
			name:      "reversed range is ignored",
			parts:     []SuggestedPart{{From: 4, To: 2}},
			pageCount: 5,
			want:      []SuggestedPart{{From: 1, To: 5}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&SplitSuggestion{Parts: tc.parts}).Normalize(tc.pageCount)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d parts %+v, want %d %+v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("part %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSplitSuggestionNormalizeNilReceiver(t *testing.T) {
	t.Parallel()

	var suggestion *SplitSuggestion
	got := suggestion.Normalize(3)
	if len(got) != 1 || got[0] != (SuggestedPart{From: 1, To: 3}) {
		t.Fatalf("expected one whole-file part, got %+v", got)
	}
}
