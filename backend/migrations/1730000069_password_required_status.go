package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const passwordRequiredStatus = "password_required"

// A PDF that needs its user password to open waits in this status until its
// owner unlocks it. Only the document carries it: the job that found it ends
// failed.
func init() {
	m.Register(addPasswordRequiredStatus, dropPasswordRequiredStatus)
}

func documentStatusField(app core.App) (*core.Collection, *core.SelectField, error) {
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil, nil, err
	}
	field, ok := collection.Fields.GetByName("processing_status").(*core.SelectField)
	if !ok {
		return nil, nil, fmt.Errorf("documents.processing_status is not a select field")
	}
	return collection, field, nil
}

func addPasswordRequiredStatus(app core.App) error {
	collection, field, err := documentStatusField(app)
	if err != nil {
		return err
	}
	if slices.Contains(field.Values, passwordRequiredStatus) {
		return nil
	}
	field.Values = append(field.Values, passwordRequiredStatus)
	return app.Save(collection)
}

func dropPasswordRequiredStatus(app core.App) error {
	collection, field, err := documentStatusField(app)
	if err != nil {
		return nil
	}
	if _, err := app.DB().NewQuery(
		"UPDATE {{documents}} SET [[processing_status]] = 'failed' WHERE [[processing_status]] = {:status}",
	).Bind(map[string]any{"status": passwordRequiredStatus}).Execute(); err != nil {
		return err
	}
	field.Values = slices.DeleteFunc(field.Values, func(value string) bool { return value == passwordRequiredStatus })
	return app.Save(collection)
}
