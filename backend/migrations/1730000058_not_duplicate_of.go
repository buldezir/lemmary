package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds not_duplicate_of, the documents the owner reviewed this one against and
// kept it apart from, so duplicate detection never pairs them again. Hidden:
// only marking a possible duplicate reviewed writes it.
//
// A duplicate reviewed before this kept duplicate_of, and with it skipped
// metadata, embedding and related links for good. Those go back to
// needs_review, where marking them reviewed now keeps them as documents of
// their own.
func init() {
	m.Register(addNotDuplicateOf, dropNotDuplicateOf)
}

func addNotDuplicateOf(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	if documents.Fields.GetByName("not_duplicate_of") == nil {
		documents.Fields.Add(&core.RelationField{
			Name:         "not_duplicate_of",
			CollectionId: documents.Id,
			MaxSelect:    100,
			Hidden:       true,
		})
		if err := app.Save(documents); err != nil {
			return err
		}
	}
	_, err = app.DB().NewQuery(
		"UPDATE documents SET processing_status = 'needs_review' WHERE duplicate_of != '' AND processing_status = 'completed'",
	).Execute()
	return err
}

func dropNotDuplicateOf(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil
	}
	documents.Fields.RemoveByName("not_duplicate_of")
	return app.Save(documents)
}
