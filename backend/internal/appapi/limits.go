package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/limits"
)

// Unlimited is explicit rather than a sentinel in Limit, so a client never has
// to know that some number means "no bound"; Limit is omitted when unlimited.
type limitStatus struct {
	Used      int64  `json:"used"`
	Limit     *int64 `json:"limit,omitempty"`
	Unlimited bool   `json:"unlimited"`
}

type limitsResponse struct {
	// Enforced is false when this install bounds nothing, which is the default.
	Enforced bool `json:"enforced"`

	// Misconfigured names the LIMIT_* variables this instance could not read.
	// Each fell back to unlimited, so the instance works and the plan is not
	// enforced, which is the one failure here nobody would otherwise notice.
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

// handleGetLimits is bindAuth rather than bindAdmin: the person about to be
// refused is the one who needs to see the room left, and the numbers are
// instance-wide totals that leak nothing about other users. The two per-file
// limits report a used of 0, since they bound one upload rather than accumulate.
func handleGetLimits(app core.App, lim limits.Limits, badKeys []string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		response := limitsResponse{Enforced: lim.Any()}
		if IsAppAdmin(e) {
			response.Misconfigured = badKeys
		}

		// Usage is only read when something is bounded, so an unlimited install
		// pays no COUNT here. The statuses are filled in either way: a body
		// saying enforced false while every limit reports unlimited false would
		// be read backwards by a consumer checking the individual limits.
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

// preflightImport is a better error, not a stronger guarantee: the create hook
// enforces every limit per document, and this only turns the common case into
// one message instead of several hundred. It is not a reservation -- nothing
// holds the room between the check and the run, so do not describe the bulk
// paths as all-or-nothing on the strength of it (docs/setup.md has the detail).
// pages is passed as 0 by callers that cannot know it without opening every PDF.
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
