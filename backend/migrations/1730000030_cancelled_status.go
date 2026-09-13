package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Cancelled distinguishes work deliberately stopped by a user from a pipeline
// failure. Both the job and its document carry it: Activity reads the former,
// while the document status is what makes the stopped import safe to discard.
func init() {
	m.Register(addCancelledStatuses, dropCancelledStatuses)
}

func addCancelledStatuses(app core.App) error {
	for _, target := range []struct {
		collection string
		field      string
	}{
		{"documents", "processing_status"},
		{"processing_jobs", "status"},
	} {
		collection, err := app.FindCollectionByNameOrId(target.collection)
		if err != nil {
			return err
		}
		field, ok := collection.Fields.GetByName(target.field).(*core.SelectField)
		if !ok {
			return fmt.Errorf("%s.%s is not a select field", target.collection, target.field)
		}
		if !slices.Contains(field.Values, "cancelled") {
			field.Values = append(field.Values, "cancelled")
			if err := app.Save(collection); err != nil {
				return err
			}
		}
	}
	return nil
}

func dropCancelledStatuses(app core.App) error {
	for _, target := range []struct {
		collection string
		field      string
		fallback   string
	}{
		{"documents", "processing_status", "failed"},
		{"processing_jobs", "status", "failed"},
	} {
		collection, err := app.FindCollectionByNameOrId(target.collection)
		if err != nil {
			continue
		}
		field, ok := collection.Fields.GetByName(target.field).(*core.SelectField)
		if !ok {
			continue
		}
		if _, err := app.DB().NewQuery(
			"UPDATE {{" + target.collection + "}} SET [[" + target.field + "]] = {:fallback} WHERE [[" + target.field + "]] = 'cancelled'",
		).Bind(map[string]any{"fallback": target.fallback}).Execute(); err != nil {
			return err
		}
		field.Values = slices.DeleteFunc(field.Values, func(value string) bool { return value == "cancelled" })
		if err := app.Save(collection); err != nil {
			return err
		}
	}
	return nil
}
