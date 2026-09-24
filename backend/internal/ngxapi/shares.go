package ngxapi

import (
	"encoding/json"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/appapi"
)

// readableDocuments is the documents ViewRule as a query expression: rows the
// caller owns, plus rows shared with them read-only.
func readableDocuments(authID string) dbx.Expression {
	return dbx.NewExp(
		appapi.ReadableDocumentsSQL("documents", "ngxReader"),
		dbx.Params{"ngxReader": authID},
	)
}

func findReadableDocumentByNgxID(app core.App, authID string, ngxID int) (*core.Record, error) {
	record, err := findRecordByNgxID(app, "documents", ngxID, "")
	if err != nil {
		return nil, err
	}
	if !appapi.CanReadDocument(app, record, authID) {
		return nil, errNotFound
	}
	return record, nil
}

func findReadableDocument(app core.App, authID, id string) (*core.Record, error) {
	ngxID, err := parseNgxID(id)
	if err != nil {
		return nil, err
	}
	return findReadableDocumentByNgxID(app, authID, ngxID)
}

// sharedEntityIDs collects the named entities carried by the documents shared
// with authID. They belong to their own owners, so no rule reaches them; naming
// the handful of ids explicitly is what lets a client render a shared
// document's tags instead of blanks.
func sharedEntityIDs(app core.App, collection, authID string) ([]any, error) {
	if authID == "" {
		return nil, nil
	}

	type row struct {
		DocumentType  string `db:"document_type"`
		Correspondent string `db:"correspondent"`
		Tags          string `db:"tags"`
	}
	var rows []row
	err := app.DB().NewQuery(
		"SELECT d.[[document_type]], d.[[correspondent]], d.[[tags]] FROM {{documents}} d" +
			" JOIN {{" + appapi.CollectionShares + "}} s ON s.[[document]] = d.[[id]]" +
			" WHERE s.[[user]] = {:user}").
		Bind(dbx.Params{"user": authID}).All(&rows)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	ids := []any{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, r := range rows {
		switch collection {
		case "document_types":
			add(r.DocumentType)
		case "correspondents":
			add(r.Correspondent)
		case "tags":
			var tags []string
			// The column is a JSON array, and legacy rows hold an empty string.
			if json.Unmarshal([]byte(r.Tags), &tags) == nil {
				for _, tag := range tags {
					add(tag)
				}
			}
		}
	}
	return ids, nil
}

// readableEntities is sharedEntityIDs folded together with ownership, for the
// named-entity list and detail routes.
func readableEntities(app core.App, collection, authID string) (dbx.Expression, error) {
	owned := dbx.HashExp{"user": authID}
	shared, err := sharedEntityIDs(app, collection, authID)
	if err != nil {
		return nil, err
	}
	if len(shared) == 0 {
		return owned, nil
	}
	return dbx.Or(owned, dbx.In("id", shared...)), nil
}

func findReadableNamedRecord(app core.App, collection string, ngxID int, authID string) (*core.Record, error) {
	record, err := findRecordByNgxID(app, collection, ngxID, "")
	if err != nil {
		return nil, err
	}
	if record.GetString("user") == authID {
		return record, nil
	}
	shared, err := sharedEntityIDs(app, collection, authID)
	if err != nil {
		return nil, err
	}
	for _, id := range shared {
		if id == record.Id {
			return record, nil
		}
	}
	return nil, errNotFound
}
