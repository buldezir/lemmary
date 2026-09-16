package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Marks the documents file fields as protected.
//
// An unprotected FileField is served by PocketBase's /api/files route to any
// client that presents the URL: collection API rules do not apply there, so
// every uploaded document and preview was a bearer capability that survived
// logout and session revocation. Protected files are checked against the
// collection's ViewRule with a short-lived file token instead.
//
// The paperless-ngx compatibility layer is unaffected: its download and thumb
// handlers authenticate the request themselves and serve the bytes directly.
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		for _, name := range []string{"file", "preview"} {
			if f, ok := documents.Fields.GetByName(name).(*core.FileField); ok {
				f.Protected = true
			}
		}
		return app.Save(documents)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		for _, name := range []string{"file", "preview"} {
			if f, ok := documents.Fields.GetByName(name).(*core.FileField); ok {
				f.Protected = false
			}
		}
		return app.Save(documents)
	})
}
