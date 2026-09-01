package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/limits"
)

// limitStatus is one allowance and what is used against it.
//
// Unlimited is explicit rather than encoded as a sentinel in Limit, so a client
// never has to know that some number means "no bound". Limit is omitted when
// unlimited for the same reason.
type limitStatus struct {
	Used      int64  `json:"used"`
	Limit     *int64 `json:"limit,omitempty"`
	Unlimited bool   `json:"unlimited"`
}

type limitsResponse struct {
	// Enforced is false when this install bounds nothing, which is the default.
	// The UI reads it to decide whether to render any of this at all.
	Enforced bool `json:"enforced"`

	// Misconfigured names the LIMIT_* variables this instance could not read.
	// Each one fell back to unlimited, so the instance works and the plan is not
	// being enforced -- the one failure here nobody would otherwise notice.
	// Admin-only: it is operator detail, and a regular user can do nothing with
	// it.
	Misconfigured []string `json:"misconfigured,omitempty"`

	Documents       limitStatus `json:"documents"`
	DocumentPages   limitStatus `json:"document_pages"`
	StorageBytes    limitStatus `json:"storage_bytes"`
	FileBytes       limitStatus `json:"file_bytes"`
	FilePages       limitStatus `json:"file_pages"`
	AdditionalUsers limitStatus `json:"additional_users"`
}

func status(limit limits.Limit, used int64) limitStatus {
	if limit.IsUnlimited() {
		return limitStatus{Used: used, Unlimited: true}
	}
	value := limit.Value()
	return limitStatus{Used: used, Limit: &value}
}

// handleGetLimits reports this instance's allowances and what is used against
// them.
//
// bindAuth rather than bindAdmin: the upload page shows how much room is left,
// and that is not an admin-only fact -- the person about to be refused is the
// one who needs to see it. The numbers are instance-wide totals with no
// per-account detail in them, so they leak nothing about other users.
//
// The two per-file limits report a used of 0: they bound one upload rather than
// accumulating, so there is nothing to have used up.
func handleGetLimits(app core.App, lim limits.Limits, badKeys []string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		response := limitsResponse{Enforced: lim.Any()}
		if IsAppAdmin(e) {
			response.Misconfigured = badKeys
		}

		// Usage is only read when something is actually bounded, so an unlimited
		// install pays no COUNT and no SUMs here. The statuses are still filled
		// in either way: a body saying `enforced: false` while every limit
		// reports `unlimited: false` contradicts itself, and a consumer that
		// checks the individual limits rather than the top-level flag would read
		// it backwards.
		var usage limits.Usage
		if response.Enforced {
			measured, err := limits.Measure(app)
			if err != nil {
				app.Logger().Error("reading instance usage failed", "component", "limits", "error", err)
				return writeError(e, http.StatusInternalServerError, "Failed to read instance usage.")
			}
			usage = measured
		}

		response.Documents = status(lim.Documents, usage.Documents)
		response.DocumentPages = status(lim.DocumentPages, usage.DocumentPages)
		response.StorageBytes = status(lim.StorageBytes, usage.StorageBytes)
		response.FileBytes = status(lim.FileBytes, 0)
		response.FilePages = status(lim.FilePages, 0)
		response.AdditionalUsers = status(lim.AdditionalUsers, usage.AdditionalUsers)

		return writeJSON(e, http.StatusOK, response)
	}
}

// preflightImport refuses a bulk ingest that plainly could not fit.
//
// A better error, not a stronger guarantee. The create hook is what actually
// enforces every limit, and it would refuse the over-limit documents one at a
// time -- leaving a partial library and several hundred identical errors. This
// turns the common case into one message while the user is still deciding whether
// to confirm.
//
// It is explicitly not a reservation. Nothing holds the room between this check
// and the run, so a batch confirmed minutes later, or racing another upload, can
// still stop partway; and each caller can only offer the dimensions it knows
// (see below). Do not describe the bulk paths as all-or-nothing on the strength
// of this function -- docs/setup.md lists what each one can and cannot promise.
//
// documents and bytes are the additions the batch would make. pages is passed as
// 0 by callers that cannot know it: an archive's real page counts are only
// discoverable by opening every PDF in it, which is not work to do inside an
// upload request. The pages limit is still enforced per document by the create
// hook, so a batch can be refused partway through on that one limit alone.
func preflightImport(app core.App, lim limits.Limits, documents, pages, bytes int64) *limits.ErrExceeded {
	if documents <= 0 {
		return nil
	}
	if lim.Documents.IsUnlimited() && lim.DocumentPages.IsUnlimited() && lim.StorageBytes.IsUnlimited() {
		return nil
	}
	usage, err := limits.Measure(app)
	if err != nil {
		// Not the caller's fault, and not a reason to refuse the upload: the
		// create hook still enforces every limit per document.
		app.Logger().Error("import preflight skipped: reading usage failed",
			"component", "limits", "error", err)
		return nil
	}
	return limits.AsExceeded(lim.CheckRoom(usage, documents, pages, bytes))
}
