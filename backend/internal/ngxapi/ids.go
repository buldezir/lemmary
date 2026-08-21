package ngxapi

import (
	"errors"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func toNgxID(pbID string) int {
	if pbID == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(pbID))
	id := int(h.Sum32() & 0x7fffffff)
	if id == 0 {
		return 1
	}
	return id
}

func parseNgxID(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func findRecordByNgxID(app core.App, collection string, ngxID int, ownerUserID string) (*core.Record, error) {
	filter, params := ownerScope(ownerUserID)
	var (
		records []*core.Record
		err     error
	)
	if params != nil {
		records, err = app.FindRecordsByFilter(collection, filter, "", 500, 0, params)
	} else {
		records, err = app.FindRecordsByFilter(collection, filter, "", 500, 0)
	}
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if toNgxID(record.Id) == ngxID {
			return record, nil
		}
	}
	return nil, errors.New("not found")
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

func ngxRelationID(record *core.Record, field string) any {
	id := record.GetString(field)
	if id == "" {
		return nil
	}
	return toNgxID(id)
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
// PocketBase ids, keeping only tags owned by ownerUserID.
func resolveTagPBIDs(app core.App, rawIDs []string, ownerUserID string) []string {
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
			}
			continue
		}
		tag, err := findRecordByNgxID(app, "tags", ngxID, ownerUserID)
		if err == nil {
			result = append(result, tag.Id)
		}
	}
	return result
}

func ngxTagIDs(app core.App, pbIDs []string) []int {
	result := make([]int, 0, len(pbIDs))
	for _, id := range pbIDs {
		if id != "" {
			result = append(result, toNgxID(id))
		}
	}
	return result
}
