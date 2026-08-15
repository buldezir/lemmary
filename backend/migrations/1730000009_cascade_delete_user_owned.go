package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		return setUserOwnedCascadeDelete(app, true)
	}, func(app core.App) error {
		return setUserOwnedCascadeDelete(app, false)
	})
}

// setUserOwnedCascadeDelete toggles CascadeDelete on relations that must go
// away with their owner: user-owned documents/types/correspondents, and jobs
// that would otherwise block document deletion (required document relation).
func setUserOwnedCascadeDelete(app core.App, cascade bool) error {
	targets := []struct{ collection, field string }{
		{"documents", "user"},
		{"document_types", "user"},
		{"correspondents", "user"},
		{"processing_jobs", "document"},
	}
	for _, target := range targets {
		if err := setRelationCascadeDelete(app, target.collection, target.field, cascade); err != nil {
			return err
		}
	}
	return nil
}

func setRelationCascadeDelete(app core.App, collectionName, fieldName string, cascade bool) error {
	coll, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		if !cascade {
			return nil
		}
		return err
	}
	field := coll.Fields.GetByName(fieldName)
	rel, ok := field.(*core.RelationField)
	if !ok || rel == nil {
		if !cascade {
			return nil
		}
		return fmt.Errorf("%s.%s is not a relation field", collectionName, fieldName)
	}
	rel.CascadeDelete = cascade
	return app.Save(coll)
}
