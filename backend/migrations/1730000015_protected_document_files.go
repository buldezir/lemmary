package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Marks the documents file fields as protected.
//
// An unprotected FileField is served by PocketBase's /api/files route to any
// client that presents the URL — collection API rules do not apply there, so
// every uploaded document and preview was a bearer capability: unguessable,
// but valid for anyone holding the link, surviving logout and session
// revocation for as long as the record exists, and sitting in browser
// history and proxy logs the whole time.
//
// Protected files are checked against the collection's ViewRule with the
// short-lived file token (?token=, minted at POST /api/files/token by an
// authenticated user), so access becomes owner-or-superuser and expires with
// the token. The paperless-ngx compatibility layer is unaffected: its
// download and thumb handlers authenticate the request themselves and serve
// the bytes directly rather than redirecting through /api/files.
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
