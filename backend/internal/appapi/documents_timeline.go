package appapi

import (
	"log/slog"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// timelineMonth is one calendar month that holds documents.
type timelineMonth struct {
	// Month is "YYYY-MM".
	Month string `json:"month"`
	Count int    `json:"count"`
}

// documentsTimeline is the shape of the archive in time: how many documents sit
// in each month, newest month first.
type documentsTimeline struct {
	Months []timelineMonth `json:"months"`
	// Undated counts the documents with no document_date. They cannot be
	// reached by any date range, so the sidebar shows them as a separate,
	// unclickable total rather than silently dropping them.
	Undated int `json:"undated"`
}

// timelineRow is one GROUP BY bucket. The undated documents arrive as the empty
// bucket, because substr('', 1, 7) is ''.
type timelineRow struct {
	Month string `db:"month"`
	Count int    `db:"count"`
}

// handleDocumentsTimeline reports the caller's documents grouped by the month
// on the document itself.
//
// The counts deliberately ignore the list's other filters: the sidebar is a map
// of the whole archive, so it stays still while you narrow the list rather than
// rearranging itself under the cursor. That also keeps it to one query, fetched
// once per change to the library instead of once per keystroke.
func handleDocumentsTimeline(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		var rows []timelineRow
		// substr rather than a date function: a PocketBase DateField column is
		// TEXT and holds both the "YYYY-MM-DD" that NormalizeDocumentDate writes
		// and the "YYYY-MM-DD HH:MM:SS.sssZ" PocketBase writes itself. The first
		// seven characters are the month under either shape, and an empty date
		// truncates to "", which is the undated bucket -- so one pass covers
		// both.
		//
		// RecordQuery rather than a bare DB().NewQuery so this inherits
		// PocketBase's lock-retry and query timeout, the same reasoning as
		// limits.Measure.
		err = app.RecordQuery("documents").
			Select("substr(COALESCE(document_date, ''), 1, 7) AS month", "COUNT(*) AS count").
			AndWhere(dbx.HashExp{"user": ownerID}).
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

// buildTimeline splits the undated bucket out of the grouped rows, preserving
// the query's newest-first order.
func buildTimeline(rows []timelineRow) documentsTimeline {
	timeline := documentsTimeline{Months: []timelineMonth{}}
	for _, row := range rows {
		if row.Month == "" {
			timeline.Undated += row.Count
			continue
		}
		timeline.Months = append(timeline.Months, timelineMonth{Month: row.Month, Count: row.Count})
	}
	return timeline
}
