package archiveimport

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
)

// Metadata sidecars come from another instance, so every field is treated as
// untrusted input: shape is checked before use and anything unusable is
// dropped rather than failing the document it belongs to.

func stringField(meta map[string]any, key string) string {
	if v, ok := meta[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func floatField(meta map[string]any, key string) (float64, bool) {
	v, ok := meta[key].(float64)
	return v, ok
}

func stringsField(meta map[string]any, key string) []string {
	raw, ok := meta[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// restorableStatuses are the processing_status values the collection accepts.
// "processing" is deliberately absent: nothing is mid-run in a fresh restore,
// and the pipeline sets the real status once its job runs.
var restorableStatuses = map[string]struct{}{
	models.DocStatusPending:     {},
	models.DocStatusCompleted:   {},
	models.DocStatusFailed:      {},
	models.DocStatusNeedsReview: {},
}

// applyMetadata restores the document fields carried by a metadata sidecar.
// Relations are resolved by name through resolver, creating the taxonomy record
// when this instance does not have it yet.
func applyMetadata(record *core.Record, meta map[string]any, resolver *taxonomyResolver) error {
	for _, field := range []string{
		"title", "title_original",
		"purpose", "purpose_original",
		"summary", "summary_original",
		"metadata_source", "text_fingerprint",
	} {
		if value := stringField(meta, field); value != "" {
			record.Set(field, value)
		}
	}

	if date, ok := models.NormalizeDocumentDate(stringField(meta, "document_date")); ok && date != "" {
		record.Set("document_date", date)
	}
	if status := stringField(meta, "processing_status"); status != "" {
		if _, ok := restorableStatuses[status]; ok {
			record.Set("processing_status", status)
		}
	}
	// The collection bounds confidence to 0..1; a value outside it would make
	// the whole save fail over one advisory number.
	if confidence, ok := floatField(meta, "confidence"); ok && confidence >= 0 && confidence <= 1 {
		record.Set("confidence", confidence)
	}
	if people := stringsField(meta, "people_or_organizations"); len(people) > 0 {
		record.Set("people_or_organizations", people)
	}

	tagIDs := make([]string, 0)
	for _, name := range stringsField(meta, "tags") {
		id, err := resolver.tag(name)
		if err != nil {
			return err
		}
		if id != "" {
			tagIDs = append(tagIDs, id)
		}
	}
	if len(tagIDs) > 0 {
		record.Set("tags", tagIDs)
	}

	if name := stringField(meta, "document_type"); name != "" {
		id, err := resolver.namedEntity("document_types", name, "")
		if err != nil {
			return err
		}
		if id != "" {
			record.Set("document_type", id)
		}
	}
	if name := stringField(meta, "correspondent"); name != "" {
		id, err := resolver.namedEntity("correspondents", name, "")
		if err != nil {
			return err
		}
		if id != "" {
			record.Set("correspondent", id)
		}
	}
	return nil
}

// parseTimestamp validates a timestamp from a sidecar before it is written back
// over an autodate column, so a malformed value cannot corrupt the row.
func parseTimestamp(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := types.ParseDateTime(raw)
	if err != nil || parsed.IsZero() {
		return "", false
	}
	return parsed.String(), true
}
