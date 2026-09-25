package appapi

import (
	"log/slog"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type timelineMonth struct {
	// Month is "YYYY-MM".
	Month string `json:"month"`
	Count int    `json:"count"`
}

// documentsTimeline is newest month first.
type documentsTimeline struct {
	Months []timelineMonth `json:"months"`
	// Undated counts the documents no date range can reach, which the sidebar
	// shows as a row filtering by ?undated=true rather than by a From/To.
	Undated int `json:"undated"`
}

// The undated documents arrive as the empty bucket, since the month substring
// of an empty date is empty.
type timelineRow struct {
	Month string `db:"month"`
	Count int    `db:"count"`
}

// handleDocumentsTimeline ignores the list's other filters: the sidebar is a
// map of the whole archive, so it stays still while you narrow the list, and it
// stays one query fetched per change to the library rather than per keystroke.
func handleDocumentsTimeline(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		var rows []timelineRow
		// substr rather than a date function: a DateField column is TEXT and
		// holds both "YYYY-MM-DD" and "YYYY-MM-DD HH:MM:SS.sssZ", and the first
		// seven characters are the month under either shape.
		//
		// RecordQuery rather than a bare DB().NewQuery, so this inherits
		// PocketBase's lock-retry and query timeout.
		err = app.RecordQuery("documents").
			Select("substr(COALESCE(document_date, ''), 1, 7) AS month", "COUNT(*) AS count").
			AndWhere(dbx.NewExp(ReadableDocumentsSQL("documents", "owner"), dbx.Params{"owner": ownerID})).
			GroupBy("month").
			OrderBy("month DESC").
			All(&rows)
		if err != nil {
			app.Logger().Error("document timeline query failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to load the timeline.")
		}

		return writeJSON(e, http.StatusOK, buildTimeline(rows))
	}
}

// buildTimeline preserves the query's newest-first order.
func buildTimeline(rows []timelineRow) documentsTimeline {
	timeline := documentsTimeline{Months: []timelineMonth{}}
	for _, row := range rows {
		if row.Month == "" {
			timeline.Undated += row.Count
			continue
		}
		timeline.Months = append(timeline.Months, timelineMonth(row))
	}
	return timeline
}
