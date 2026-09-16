package limits

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

type Usage struct {
	Documents       int64
	DocumentPages   int64
	StorageBytes    int64
	AdditionalUsers int64
}

// Measure counts from live rows rather than a stored counter, so deleting a
// document frees its allowance with no bookkeeping: there is no custom delete
// hook on documents and PocketBase frees the blob in a fire-and-forget
// goroutine, so a counter would drift on the hardest path to observe.
//
// Documents predating the page_count / size_bytes migration contribute 0 to the
// two sums; see the migration for why they are not backfilled.
func Measure(app core.App) (Usage, error) {
	var documents, pages, bytes int64
	// One index-only scan of idx_documents_usage; see the migration for why that
	// index is not optional. COALESCE covers the empty library, and the CAST the
	// column type: a NumberField is NUMERIC and PocketBase writes float64 into it,
	// so SUM can come back REAL.
	//
	// RecordQuery rather than DB().NewQuery, so this inherits the lock-retry and
	// query timeout and, inside a transaction, reads that transaction's own
	// uncommitted rows, which is what makes a batched importer accumulate.
	// importer accumulate correctly instead of re-reading a stale total.
	err := app.RecordQuery("documents").
		Select(
			"COUNT(*)",
			"CAST(COALESCE(SUM(page_count), 0) AS INTEGER)",
			"CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER)",
		).
		Row(&documents, &pages, &bytes)
	if err != nil {
		return Usage{}, fmt.Errorf("measure document usage: %w", err)
	}

	users, err := CountAdditionalUsers(app)
	if err != nil {
		return Usage{}, err
	}

	return Usage{
		Documents:       documents,
		DocumentPages:   pages,
		StorageBytes:    bytes,
		AdditionalUsers: users,
	}, nil
}

// CountAdditionalUsers exempts exactly one account, the admin, and not "every
// account carrying is_app_admin": a superuser can create a second superuser and
// UpsertPairedUser flags it too, so exempting the flag would let the very person
// the limit constrains mint accounts without bound.
//
// Counting from the total also sidesteps three-valued logic: a record that never
// had is_app_admin written holds NULL, which `is_app_admin != true` never
// matches in SQLite.
// NULL, which `is_app_admin != true` does not match in SQLite.
func CountAdditionalUsers(app core.App) (int64, error) {
	total, err := app.CountRecords("users")
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return additionalOf(total), nil
}

func additionalOf(total int64) int64 {
	if total < 1 {
		return 0
	}
	return total - 1
}
