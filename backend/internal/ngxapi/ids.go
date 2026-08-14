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

func findRecordByNgxID(
	app core.App,
	collection string,
	ngxID int,
	filter string,
	params map[string]any,
) (*core.Record, error) {
	records, err := app.FindRecordsByFilter(collection, filter, "", 500, 0, params)
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
	return findRecordByNgxID(app, "documents", ngxID, ownerFilter(authID), ownerParams(authID))
}

func ownerFilter(authID string) string {
	return "user = {:userId}"
}

func ownerParams(authID string) map[string]any {
	return map[string]any{"userId": authID}
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

func resolvePBRelationID(app core.App, collection string, raw any, filter string, params map[string]any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case float64:
		record, err := findRecordByNgxID(app, collection, int(v), filter, params)
		if err != nil {
			return ""
		}
		return record.Id
	case int:
		record, err := findRecordByNgxID(app, collection, v, filter, params)
		if err != nil {
			return ""
		}
		return record.Id
	case string:
		if strings.TrimSpace(v) == "" {
			return ""
		}
		if ngxID, err := strconv.Atoi(v); err == nil {
			record, err := findRecordByNgxID(app, collection, ngxID, filter, params)
			if err != nil {
				return ""
			}
			return record.Id
		}
		record, err := app.FindRecordById(collection, v)
		if err != nil {
			return ""
		}
		if uid, ok := params["userId"].(string); ok && uid != "" && record.GetString("user") != uid {
			return ""
		}
		return record.Id
	default:
		return ""
	}
}

func resolveTagPBIDs(app core.App, rawIDs []string) []string {
	result := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ngxID, err := strconv.Atoi(raw)
		if err != nil {
			if _, err := app.FindRecordById("tags", raw); err == nil {
				result = append(result, raw)
			}
			continue
		}
		tag, err := findRecordByNgxID(app, "tags", ngxID, "", nil)
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
