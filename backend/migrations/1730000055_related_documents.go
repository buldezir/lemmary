package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds related, the documents an owner or the pipeline linked to this one, and
// reference_numbers, the normalized identifiers the pipeline matches links on.
//
// A link is stored on one side only and read from both. related must never
// cascade: on a self-relation that would delete the linking document along
// with the linked one. reference_numbers is hidden: only the pipeline writes it.
func init() {
	m.Register(addRelatedDocuments, dropRelatedDocuments)
}

func addRelatedDocuments(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	if documents.Fields.GetByName("related") == nil {
		documents.Fields.Add(&core.RelationField{
			Name:         "related",
			CollectionId: documents.Id,
			MaxSelect:    50,
		})
	}
	if documents.Fields.GetByName("reference_numbers") == nil {
		documents.Fields.Add(&core.JSONField{Name: "reference_numbers", Hidden: true})
	}
	return app.Save(documents)
}

func dropRelatedDocuments(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil
	}
	documents.Fields.RemoveByName("related")
	documents.Fields.RemoveByName("reference_numbers")
	return app.Save(documents)
}
