package ngxapi

import (
	"encoding/json"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/appapi"
	"lemmary/backend/internal/models"
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
	if _, fieldID := storage(collection); fieldID != "" {
		var options []string
		err := app.DB().NewQuery(
			"SELECT DISTINCT v.[[option]] FROM {{" + models.CustomFieldValuesCollection + "}} v" +
				" JOIN {{" + appapi.CollectionShares + "}} s ON s.[[document]] = v.[[document]]" +
				" WHERE s.[[user]] = {:user} AND v.[[field]] = {:field} AND v.[[option]] != ''").
			Bind(dbx.Params{"user": authID, "field": fieldID}).Column(&options)
		if err != nil {
			return nil, err
		}
		ids := make([]any, len(options))
		for i, id := range options {
			ids[i] = id
		}
		return ids, nil
	}

	var rows []string
	err := app.DB().NewQuery(
		"SELECT d.[[tags]] FROM {{documents}} d" +
			" JOIN {{" + appapi.CollectionShares + "}} s ON s.[[document]] = d.[[id]]" +
			" WHERE s.[[user]] = {:user}").
		Bind(dbx.Params{"user": authID}).Column(&rows)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	ids := []any{}
	for _, raw := range rows {
		var tags []string
		// The column is a JSON array, and legacy rows hold an empty string.
		if json.Unmarshal([]byte(raw), &tags) != nil {
			continue
		}
		for _, tag := range tags {
			if tag != "" && !seen[tag] {
				seen[tag] = true
				ids = append(ids, tag)
			}
		}
	}
	return ids, nil
}

// readableEntities is sharedEntityIDs folded together with ownership, for the
// named-entity list and detail routes.
func readableEntities(app core.App, collection, authID string) (dbx.Expression, error) {
	var scope dbx.Expression = dbx.HashExp{"user": authID}
	shared, err := sharedEntityIDs(app, collection, authID)
	if err != nil {
		return nil, err
	}
	if len(shared) > 0 {
		scope = dbx.Or(scope, dbx.In("id", shared...))
	}
	if _, fieldID := storage(collection); fieldID != "" {
		scope = dbx.And(dbx.HashExp{"field": fieldID}, scope)
	}
	return scope, nil
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
