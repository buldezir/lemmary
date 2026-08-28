package limits

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// Usage is what the instance currently holds.
type Usage struct {
	Documents       int64
	DocumentPages   int64
	StorageBytes    int64
	AdditionalUsers int64
}

// Measure reads current usage.
//
// Everything is counted from live rows -- COUNT and SUM over documents rather
// than a stored counter -- so deleting a document frees its allowance with no
// bookkeeping at all. That matters here more than the query cost: there is no
// custom delete hook on documents, and PocketBase frees the blob in a
// fire-and-forget goroutine, so a counter would drift on exactly the path that
// is hardest to observe.
//
// Documents that predate the page_count / size_bytes migration contribute 0 to
// the two sums; see the migration for why they are not backfilled.
func Measure(app core.App) (Usage, error) {
	var documents, pages, bytes int64
	// One index-only scan of idx_documents_usage; see the migration for why that
	// index is not optional.
	//
	// COALESCE covers the empty library, where SUM over no rows is NULL. The
	// CAST covers the column type: a NumberField is NUMERIC and PocketBase
	// writes float64 into it, so SUM can come back REAL and must not be scanned
	// straight into an int64.
	//
	// RecordQuery rather than a bare DB().NewQuery so this inherits PocketBase's
	// lock-retry and query timeout, and so that inside a transaction it reads
	// that transaction's own uncommitted rows -- which is what makes a batched
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

// CountAdditionalUsers counts the accounts that consume the seat allowance.
//
// Exactly one account is free -- the instance's own admin, created by the setup
// wizard or a superuser CLI command rather than bought as a seat. Exactly one,
// not "every account carrying is_app_admin": a superuser can create a second
// superuser from the PocketBase dashboard, and UpsertPairedUser gives that one a
// flagged users record too, so exempting the flag would let the very person the
// limit constrains mint accounts without bound.
//
// Counting from the total rather than filtering on the flag also sidesteps
// three-valued logic: a record that has never had is_app_admin written holds SQL
// NULL, which `is_app_admin != true` does not match in SQLite.
func CountAdditionalUsers(app core.App) (int64, error) {
	total, err := app.CountRecords("users")
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return additionalOf(total), nil
}

// additionalOf is the seat count for a given number of accounts: all but one.
func additionalOf(total int64) int64 {
	if total < 1 {
		return 0
	}
	return total - 1
}
