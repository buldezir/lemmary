package appwire

import (
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/metrics"
)

// registerUsageMetrics reports how full the instance is, and against what.
//
// The reading is limits.Measure -- the same one the usage endpoint serves the
// UI -- so a dashboard and the Settings page can never disagree about how many
// documents there are. It is taken on scrape rather than tracked, for the
// reason Measure exists at all: there is no delete hook on documents, so a
// counter would drift on exactly the path nothing watches.
//
// It lives here rather than in the limits package to keep that package a leaf,
// the same reason applyPerFileCaps is here.
func registerUsageMetrics(app core.App, lim limits.Limits) {
	// The allowances are read once at startup and never move, so they are
	// shaped once here rather than on every scrape.
	countLimits, byteLimits := instanceLimits(lim)

	reg := metrics.RegisterUsage(func() (metrics.Usage, error) {
		used, err := limits.Measure(app)
		if err != nil {
			return metrics.Usage{}, err
		}
		return metrics.Usage{
			Counts: map[string]int64{
				limits.NameDocuments:       used.Documents,
				limits.NameDocumentPages:   used.DocumentPages,
				limits.NameAdditionalUsers: used.AdditionalUsers,
			},
			CountLimits: countLimits,
			Bytes: map[string]int64{
				limits.NameStorageBytes: used.StorageBytes,
			},
			ByteLimits: byteLimits,
		}, nil
	})
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		_ = reg.Unregister()
		return e.Next()
	})
}

// instanceLimits is the allowance series for how full the instance is.
// Per-file ceilings (LIMIT_FILE_PAGES, LIMIT_FILE_BYTES, MaxOCRPages) stay
// off this instrument: they are not a stock the instance holds, and putting
// them on lemmary.limit next to documents would make lemmary_usage /
// lemmary_limit a lie for those labels.
func instanceLimits(lim limits.Limits) (counts, bytes map[string]int64) {
	counts = setLimits(map[string]limits.Limit{
		limits.NameDocuments:       lim.Documents,
		limits.NameDocumentPages:   lim.DocumentPages,
		limits.NameAdditionalUsers: lim.AdditionalUsers,
	})
	bytes = setLimits(map[string]limits.Limit{
		limits.NameStorageBytes: lim.StorageBytes,
	})
	return counts, bytes
}

// setLimits drops the unset allowances, because an absent series is how
// "unbounded" is said: 0 is a limit somebody sells, so it cannot double as the
// unlimited sentinel. See metrics.Usage.
func setLimits(in map[string]limits.Limit) map[string]int64 {
	out := make(map[string]int64, len(in))
	for name, limit := range in {
		if limit.IsUnlimited() {
			continue
		}
		out[name] = limit.Value()
	}
	return out
}
