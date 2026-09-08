package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the always_require_review setting and an index for the review Inbox.
//
// Two changes, one migration, because the setting is what fills the Inbox and
// the index is what makes reading it cheap. Both halves are split into named
// functions so the test can run each twice: a managed instance re-runs every
// migration on every boot, so anything here has to be idempotent.
func init() {
	m.Register(func(app core.App) error {
		if err := addAlwaysRequireReviewField(app); err != nil {
			return err
		}
		return addDocumentStatusIndex(app)
	}, func(app core.App) error {
		if err := dropDocumentStatusIndex(app); err != nil {
			return err
		}
		return dropAlwaysRequireReviewField(app)
	})
}

func addAlwaysRequireReviewField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if settings.Fields.GetByName("always_require_review") != nil {
		return nil
	}
	settings.Fields.Add(&core.BoolField{Name: "always_require_review"})
	return app.Save(settings)
}

func dropAlwaysRequireReviewField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	if settings.Fields.GetByName("always_require_review") == nil {
		return nil
	}
	settings.Fields.RemoveByName("always_require_review")
	return app.Save(settings)
}

// Covering index for the counts and lists keyed on processing_status.
//
// Same reasoning as idx_documents_user_document_date: the documents table
// stores ocr_text inline, up to models.MaxOCRTextRunes a row, so a query that
// has to read row bodies to test a status pays for text it never looks at.
// Until now (user, processing_status) had no index, and three things ask for it
// on nearly every page load: the header's Inbox count, Management's failed
// count, and the ?status= filter on the documents list. Equality on user
// first, then the filtered column, so the count reads the index alone.
func addDocumentStatusIndex(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	documents.AddIndex("idx_documents_user_processing_status", false, "user, processing_status", "")
	return app.Save(documents)
}

func dropDocumentStatusIndex(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil
	}
	documents.RemoveIndex("idx_documents_user_processing_status")
	return app.Save(documents)
}
