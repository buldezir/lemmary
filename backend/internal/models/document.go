package models

import (
	"encoding/json"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// PeopleOrOrganizations reads the documents.people_or_organizations JSON field.
//
// PocketBase hands JSON fields back as []string, []any, or a raw string
// depending on whether the record came from a save or a fresh DB read, so all
// three shapes are normalized here. Blank entries are dropped and the result is
// never nil, so callers can marshal it as an empty JSON array.
func PeopleOrOrganizations(record *core.Record) []string {
	if record == nil {
		return []string{}
	}

	switch v := record.Get("people_or_organizations").(type) {
	case []string:
		return trimNonEmpty(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		return unmarshalStringSlice([]byte(v))
	case nil:
		return []string{}
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return []string{}
		}
		return unmarshalStringSlice(encoded)
	}
}

func unmarshalStringSlice(raw []byte) []string {
	if strings.TrimSpace(string(raw)) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return trimNonEmpty(out)
}

func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
