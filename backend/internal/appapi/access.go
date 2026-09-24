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

// SharedWith lists the documents other accounts shared with userID, and those
// accounts. The chunk index stores only a document's owner, so retrieval widens
// its owner filter by these rather than reindexing chunks on every share change.
func SharedWith(app interface {
	FindRecordsByFilter(any, string, string, int, int, ...dbx.Params) ([]*core.Record, error)
}, userID string) (documentIDs, ownerIDs []string, err error) {
	if userID == "" {
		return nil, nil, nil
	}
	docs, err := app.FindRecordsByFilter("documents",
		"document_shares_via_document.user ?= {:user}", "", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		documentIDs = append(documentIDs, doc.Id)
		if owner := doc.GetString("user"); !seen[owner] {
			seen[owner] = true
			ownerIDs = append(ownerIDs, owner)
		}
	}
	return documentIDs, ownerIDs, nil
}
