package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const documentSharesCollection = "document_shares"

// A share row is the grant: there is no permission column because there is only
// one permission. Read access reads "mine, or shared with me" everywhere, through
// a back relation PocketBase resolves to a single join, so both conditions bind
// to the same share row.
//
// The signed-in guard is load-bearing, not decoration. A back relation is a LEFT
// JOIN, so on a document with no shares the joined column is NULL, and PocketBase
// matches NULL against the empty string an anonymous @request.auth.id resolves
// to. Without the guard the rule hands every document to callers with no session
// at all -- including the file itself, whose token is checked against this rule.
const documentReadRule = "user = @request.auth.id || (@request.auth.id != '' && " +
	documentSharesCollection + "_via_document.user ?= @request.auth.id)"

// Named entities are readable when a document carrying them is, so the owner's
// chips render on a shared document. List rules stay owner-only: a filter
// dropdown must never fill up with somebody else's taxonomy.
var namedEntityReadRules = map[string]string{
	"tags":           namedEntityReadRule("tags"),
	"document_types": namedEntityReadRule("document_type"),
	"correspondents": namedEntityReadRule("correspondent"),
}

func namedEntityReadRule(documentField string) string {
	return "user = @request.auth.id || (@request.auth.id != '' && documents_via_" +
		documentField + "." + documentSharesCollection +
		"_via_document.user ?= @request.auth.id)"
}

func init() {
	m.Register(func(app core.App) error {
		if err := createDocumentShares(app); err != nil {
			return err
		}
		return setSharedReadRules(app, true)
	}, func(app core.App) error {
		if err := setSharedReadRules(app, false); err != nil {
			return err
		}
		coll, err := app.FindCollectionByNameOrId(documentSharesCollection)
		if err != nil {
			return nil
		}
		return app.Delete(coll)
	})
}

func createDocumentShares(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}

	shares, err := app.FindCollectionByNameOrId(documentSharesCollection)
	if err != nil {
		shares = core.NewBaseCollection(documentSharesCollection)
	}

	ownerRule := "document.user = @request.auth.id"
	shares.ListRule = types.Pointer(ownerRule + " || user = @request.auth.id")
	shares.ViewRule = types.Pointer(ownerRule + " || user = @request.auth.id")
	shares.CreateRule = types.Pointer(ownerRule)
	// Nothing on a share is mutable; regranting is a delete and a create.
	shares.UpdateRule = nil
	shares.DeleteRule = types.Pointer(ownerRule)

	if shares.Fields.GetByName("document") == nil {
		shares.Fields.Add(&core.RelationField{
			Name:          "document",
			Required:      true,
			CollectionId:  documents.Id,
			MaxSelect:     1,
			CascadeDelete: true,
		})
	}
	if shares.Fields.GetByName("user") == nil {
		shares.Fields.Add(&core.RelationField{
			Name:          "user",
			Required:      true,
			CollectionId:  "_pb_users_auth_",
			MaxSelect:     1,
			CascadeDelete: true,
		})
	}
	if shares.Fields.GetByName("created") == nil {
		shares.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	}

	shares.AddIndex("idx_document_shares_pair", true, "document, `user`", "")
	shares.AddIndex("idx_document_shares_user", false, "`user`", "")

	return app.Save(shares)
}

func setSharedReadRules(app core.App, shared bool) error {
	ownerRule := "user = @request.auth.id"

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	rule := ownerRule
	if shared {
		rule = documentReadRule
	}
	documents.ListRule = types.Pointer(rule)
	documents.ViewRule = types.Pointer(rule)
	if err := app.Save(documents); err != nil {
		return err
	}

	for name, sharedRule := range namedEntityReadRules {
		coll, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		if shared {
			coll.ViewRule = types.Pointer(sharedRule)
		} else {
			coll.ViewRule = types.Pointer(ownerRule)
		}
		if err := app.Save(coll); err != nil {
			return err
		}
	}
	return nil
}
