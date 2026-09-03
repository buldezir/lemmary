package ngxapi

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"

	"github.com/pocketbase/dbx"
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

// ngxIDMap is the client-facing id to PocketBase id map for one collection and
// owner, built by hashing every candidate.
//
// It selects the id column alone. The obvious implementation pages whole
// records through FindRecordsByFilter, which hydrates every field to read one:
// on the documents collection that means pulling the OCR text of the entire
// archive -- megabytes -- to answer "which record is 944698583", once per
// request. A thumbnail grid asks that twenty-five times per screen.
//
// Ties are broken toward the lowest PocketBase id, so a hash collision always
// resolves to the same record as it did before this was a map.
//
// The result is cacheable whole: see ngxIDScopes.
func ngxIDMap(app core.App, collection, ownerUserID string) (map[int]string, error) {
	idColumn := "[[" + collection + ".id]]"
	q := app.RecordQuery(collection).Select(idColumn).OrderBy(idColumn + " ASC")
	if ownerUserID != "" {
		q.AndWhere(dbx.HashExp{"user": ownerUserID})
	}

	var ids []string
	if err := q.Column(&ids); err != nil {
		return nil, err
	}

	known := make(map[int]string, len(ids))
	for _, id := range ids {
		ngxID := toNgxID(id)
		if _, seen := known[ngxID]; !seen {
			known[ngxID] = id
		}
	}
	return known, nil
}

func findRecordByNgxID(app core.App, collection string, ngxID int, ownerUserID string) (*core.Record, error) {
	// A hit skips the scan entirely: the mapping is permanent, because neither
	// a PocketBase id nor its hash ever changes. Everything that could have
	// gone stale -- the record deleted, or a collision resolving to somebody
	// else's row -- is caught by the fetch and the ownership check below, which
	// fall through to a fresh scan rather than answering wrongly.
	scope := ngxIDScope{collection: collection, owner: ownerUserID}
	if pbID, ok := ngxIDScopes.get(scope, ngxID); ok {
		if record, err := app.FindRecordById(collection, pbID); err == nil {
			if ownerUserID == "" || record.GetString("user") == ownerUserID {
				return record, nil
			}
		}
	}

	known, err := ngxIDMap(app, collection, ownerUserID)
	if err != nil {
		return nil, err
	}
	ngxIDScopes.put(scope, known)

	pbID, ok := known[ngxID]
	if !ok {
		return nil, errors.New("not found")
	}
	return app.FindRecordById(collection, pbID)
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

func ngxTagIDs(app core.App, pbIDs []string) []int {
	result := make([]int, 0, len(pbIDs))
	for _, id := range pbIDs {
		if id != "" {
			result = append(result, toNgxID(id))
		}
	}
	return result
}

// ngxIDScopes caches the whole client-id-to-PocketBase-id map per collection
// and owner.
//
// Caching one resolution at a time was not enough, and the reason is what the
// traffic looks like: a thumbnail grid is twenty-five requests for twenty-five
// *different* ids, so every one of them missed and rescanned. Storing the map
// the scan already built means the first tile pays for the whole screen.
//
// Every entry is a hint, never an answer. A hit is still fetched by primary key
// and ownership-checked, so a deleted record or a hash collision landing on
// somebody else's row falls through to a fresh scan instead of answering
// wrongly. A miss always rescans, which is what keeps a document created since
// the map was built reachable.
var ngxIDScopes = &ngxIDCache{scopes: map[ngxIDScope]map[int]string{}}

// maxNgxIDScopes bounds the cache by owner-collection pairs rather than by
// entries, because a scope is only useful whole. Eviction drops everything: the
// cost is one scan per live scope, and a partial policy would be code to
// maintain for no gain.
const maxNgxIDScopes = 64

type ngxIDScope struct {
	collection string
	owner      string
}

type ngxIDCache struct {
	mu     sync.RWMutex
	scopes map[ngxIDScope]map[int]string
}

func (c *ngxIDCache) get(scope ngxIDScope, ngxID int) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pbID, ok := c.scopes[scope][ngxID]
	return pbID, ok
}

func (c *ngxIDCache) put(scope ngxIDScope, known map[int]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.scopes) >= maxNgxIDScopes {
		c.scopes = make(map[ngxIDScope]map[int]string, maxNgxIDScopes)
	}
	c.scopes[scope] = known
}
