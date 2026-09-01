package appapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildTimeline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rows []timelineRow
		want documentsTimeline
	}{
		{
			name: "empty library",
			rows: nil,
			want: documentsTimeline{Months: []timelineMonth{}},
		},
		{
			name: "keeps the query's newest-first order",
			rows: []timelineRow{
				{Month: "2026-06", Count: 1},
				{Month: "2025-10", Count: 4},
				{Month: "2024-01", Count: 2},
			},
			want: documentsTimeline{
				Months: []timelineMonth{
					{Month: "2026-06", Count: 1},
					{Month: "2025-10", Count: 4},
					{Month: "2024-01", Count: 2},
				},
			},
		},
		{
			// substr('', 1, 7) is '', so documents with no document_date arrive
			// as the empty bucket -- last, since '' sorts below every month.
			name: "empty bucket becomes the undated count",
			rows: []timelineRow{
				{Month: "2025-03", Count: 2},
				{Month: "", Count: 5},
			},
			want: documentsTimeline{
				Months:  []timelineMonth{{Month: "2025-03", Count: 2}},
				Undated: 5,
			},
		},
		{
			name: "only undated documents",
			rows: []timelineRow{{Month: "", Count: 3}},
			want: documentsTimeline{Months: []timelineMonth{}, Undated: 3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildTimeline(tc.rows)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildTimeline() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An empty library must serialise to [] rather than null: the client maps over
// months without a guard.
func TestBuildTimelineEncodesEmptyMonthsAsArray(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(buildTimeline(nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `{"months":[],"undated":0}`; got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}
