package ngxapi

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ngxid"
)

// toNgxID derives a client-facing id from a PocketBase id.
//
// Only for the collections that carry no ngxid.Field column -- users and
// processing jobs -- whose ids a client reads and never sends back. Everything
// a client can address is read from its stored column: see ngxIDOf.
func toNgxID(pbID string) int {
	return ngxid.Hash(pbID)
}

// ngxIDOf reads the client-facing id a record was stamped with on create.
func ngxIDOf(record *core.Record) int {
	return record.GetInt(ngxid.Field)
}

func parseNgxID(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

// findRecordByNgxID resolves one client-facing id inside an owner's scope.
//
// One index seek, on the unique (user, ngx_id) index. It used to be a scan of
// the owner's whole table per call, because the id was an FNV hash of the
// PocketBase id and inverting it meant hashing every candidate -- and the
// caller that matters most is a thumbnail, which swift-paperless requests once
// per tile for a whole page of documents at a time.
func findRecordByNgxID(app core.App, collection string, ngxID int, ownerUserID string) (*core.Record, error) {
	// 0 is not an id this server ever issues, and it is what the column holds
	// for a row no create hook stamped. Matching it would hand a client a
	// record by asking for nothing.
	if ngxID <= 0 {
		return nil, errors.New("not found")
	}

	// The literal "ngx_id > 0" is not redundant with the guard above: the
	// unique index is partial on exactly that predicate, and SQLite will only
	// use a partial index when the query restates its WHERE clause. Without it
	// the planner falls back to whichever index covers user alone and scans
	// that owner's rows -- which is the cost this column exists to remove.
	filter := "ngx_id > 0 && ngx_id = {:ngxID}"
	params := dbx.Params{"ngxID": ngxID}
	if ownerUserID != "" {
		filter += " && user = {:userID}"
		params["userID"] = ownerUserID
	}
	return app.FindFirstRecordByFilter(collection, filter, params)
}

// ngxIDsByPBID reads the client-facing ids of a known set of records in one
// query.
//
// The reason it is batched: a document names its tags, type and correspondent
// by PocketBase id, and rendering those as client ids now means reading the
// related rows. Per field that is five hundred point lookups for a page of two
// hundred and fifty documents -- the same shape of traffic this whole change
// exists to remove.
func ngxIDsByPBID(app core.App, collection string, pbIDs []string) (map[string]int, error) {
	if len(pbIDs) == 0 {
		return map[string]int{}, nil
	}

	values := make([]any, 0, len(pbIDs))
	for _, id := range pbIDs {
		values = append(values, id)
	}

	type idRow struct {
		PBID  string `db:"id"`
		NgxID int    `db:"ngx_id"`
	}
	var rows []idRow
	err := app.RecordQuery(collection).
		Select("[["+collection+".id]]", "[["+collection+".ngx_id]]").
		AndWhere(dbx.In("[["+collection+".id]]", values...)).
		All(&rows)
	if err != nil {
		return nil, err
	}

	known := make(map[string]int, len(rows))
	for _, row := range rows {
		known[row.PBID] = row.NgxID
	}
	return known, nil
}

// pbIDsByNgxID is the reverse of ngxIDsByPBID: the PocketBase ids behind a set
// of client ids, scoped to one owner, in one query. Ids the owner does not have
// are simply absent, which is what tells a filter to match nothing.
func pbIDsByNgxID(app core.App, collection, ownerUserID string, ngxIDs []int) (map[int]string, error) {
	if len(ngxIDs) == 0 {
		return map[int]string{}, nil
	}

	values := make([]any, 0, len(ngxIDs))
	for _, id := range ngxIDs {
		if id > 0 {
			values = append(values, id)
		}
	}
	if len(values) == 0 {
		return map[int]string{}, nil
	}

	type idRow struct {
		PBID  string `db:"id"`
		NgxID int    `db:"ngx_id"`
	}
	q := app.RecordQuery(collection).
		Select("[["+collection+".id]]", "[["+collection+".ngx_id]]").
		// Restating the partial index's own predicate is what lets SQLite use
		// it -- see findRecordByNgxID.
		AndWhere(dbx.NewExp("[[" + collection + ".ngx_id]] > 0")).
		AndWhere(dbx.In("[["+collection+".ngx_id]]", values...))
	if ownerUserID != "" {
		q.AndWhere(dbx.HashExp{"user": ownerUserID})
	}

	var rows []idRow
	if err := q.All(&rows); err != nil {
		return nil, err
	}

	known := make(map[int]string, len(rows))
	for _, row := range rows {
		known[row.NgxID] = row.PBID
	}
	return known, nil
}

func findOwnedDocumentByNgxID(app core.App, authID string, ngxID int) (*core.Record, error) {
	return findRecordByNgxID(app, "documents", ngxID, authID)
}

// ownerScope returns a PocketBase filter and its matching params together so
// the placeholder and the bound user id cannot drift apart.
func ownerScope(authID string) (string, map[string]any) {
	if authID == "" {
		return "", nil
	}
	return "user = {:userId}", map[string]any{"userId": authID}
}

func findOwnedDocument(app core.App, authID, id string) (*core.Record, error) {
	ngxID, err := parseNgxID(id)
	if err != nil {
		return nil, err
	}
	return findOwnedDocumentByNgxID(app, authID, ngxID)
}

func resolvePBRelationID(app core.App, collection string, raw any, ownerUserID string) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case float64:
		record, err := findRecordByNgxID(app, collection, int(v), ownerUserID)
		if err != nil {
			return ""
		}
		return record.Id
	case int:
		record, err := findRecordByNgxID(app, collection, v, ownerUserID)
		if err != nil {
			return ""
		}
		return record.Id
	case string:
		if strings.TrimSpace(v) == "" {
			return ""
		}
		if ngxID, err := strconv.Atoi(v); err == nil {
			record, err := findRecordByNgxID(app, collection, ngxID, ownerUserID)
			if err != nil {
				return ""
			}
			return record.Id
		}
		record, err := app.FindRecordById(collection, v)
		if err != nil {
			return ""
		}
		if ownerUserID != "" && record.GetString("user") != ownerUserID {
			return ""
		}
		return record.Id
	default:
		return ""
	}
}

// resolveTagPBIDs maps paperless-ngx numeric tag ids (or raw PocketBase ids) to
// PocketBase ids owned by ownerUserID. An id that does not resolve is an error:
// clients PATCH the full tag list, so silently dropping one would permanently
// strip it from the document while the client sees a success.
func resolveTagPBIDs(app core.App, rawIDs []string, ownerUserID string) ([]string, error) {
	result := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ngxID, err := strconv.Atoi(raw)
		if err != nil {
			if tag, err := app.FindRecordById("tags", raw); err == nil && tag.GetString("user") == ownerUserID {
				result = append(result, raw)
				continue
			}
			return nil, fmt.Errorf("unknown tag id %q", raw)
		}
		tag, err := findRecordByNgxID(app, "tags", ngxID, ownerUserID)
		if err != nil {
			return nil, fmt.Errorf("unknown tag id %q", raw)
		}
		result = append(result, tag.Id)
	}
	return result, nil
}
