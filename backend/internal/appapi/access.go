package appapi

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// CollectionShares is the collection holding read-only grants, one row per
// (document, recipient) pair.
const CollectionShares = "document_shares"

type shareLookup interface {
	FindFirstRecordByFilter(collectionModelOrIdentifier any, filter string, params ...dbx.Params) (*core.Record, error)
}

// CanReadDocument answers the same question as the documents ViewRule, for the
// endpoints that load a record by id and check ownership by hand. An empty
// userID is an unscoped caller (a superuser search), which reads everything.
func CanReadDocument(app shareLookup, record *core.Record, userID string) bool {
	if record == nil {
		return false
	}
	if userID == "" || record.GetString("user") == userID {
		return true
	}
	_, err := app.FindFirstRecordByFilter(
		CollectionShares,
		"document = {:document} && user = {:user}",
		dbx.Params{"document": record.Id, "user": userID},
	)
	return err == nil
}

// ReadableDocumentsSQL is the same predicate for the handlers that query the
// documents table rather than load records one by one. The table is addressed
// through alias, and userID is bound to the named parameter param.
func ReadableDocumentsSQL(alias, param string) string {
	ref := "{:" + param + "}"
	return "(" + alias + ".user = " + ref + " OR EXISTS (SELECT 1 FROM " + CollectionShares +
		" s WHERE s.document = " + alias + ".id AND s.user = " + ref + "))"
}
