package worker

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"unicode"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/list"
	"golang.org/x/text/unicode/norm"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/models"
)

const namedEntityListPageSize = 500

var ensureNamedEntityMu sync.Mutex

// namedScope is where a name is looked up: one owner's tags, or one owner's
// options of one field.
type namedScope struct {
	collection string
	filter     string
	params     dbx.Params
}

func optionScope(fieldID, userID string) namedScope {
	return namedScope{
		collection: models.CustomFieldOptionsCollection,
		filter:     "field = {:field} && user = {:userId}",
		params:     dbx.Params{"field": fieldID, "userId": userID},
	}
}

func tagScope(userID string) namedScope {
	return namedScope{collection: "tags", filter: "user = {:userId}", params: dbx.Params{"userId": userID}}
}

// EnsureOption finds the owner's option of an option field by name, or creates
// it. Lookup prefers the exact name, then a punctuation/accent-insensitive
// match. An existing option is reused as it is, so a user rename survives.
func EnsureOption(app core.App, fieldID, userID, name string) (id string, created bool, err error) {
	// ponytail: one process-wide lock over lookup-then-insert. Only (field,
	// user, name) is unique in the schema, so the normalized tier below is
	// unbacked: two pipelines extracting "Müller GmbH" and "Muller G.m.b.H."
	// both miss, both insert, and no conflict fires for the lookup after a
	// failed save to clean up. A partial unique index on the normalized key
	// would be the real fix; this is a handful of indexed queries on a path
	// that just spent a minute in OCR and extraction.
	ensureNamedEntityMu.Lock()
	defer ensureNamedEntityMu.Unlock()

	userID = strings.TrimSpace(userID)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	if userID == "" {
		return "", false, fmt.Errorf("user id is required")
	}
	scope := optionScope(fieldID, userID)
	if existingID, err := findNamedEntityByName(app, scope, name); err != nil || existingID != "" {
		return existingID, false, err
	}

	coll, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection)
	if err != nil {
		return "", false, err
	}
	record := core.NewRecord(coll)
	record.Set("field", fieldID)
	record.Set("user", userID)
	record.Set("name", name)
	if err := app.Save(record); err != nil {
		if existingID, findErr := findNamedEntityByName(app, scope, name); findErr != nil || existingID != "" {
			return existingID, false, findErr
		}
		return "", false, err
	}
	return record.Id, true, nil
}

// findNamedEntityByName tries the exact name, then the normalized form.
func findNamedEntityByName(app core.App, scope namedScope, name string) (string, error) {
	if existingID, err := findNamedEntity(app, scope, name); err != nil || existingID != "" {
		return existingID, err
	}
	return findNamedEntityNormalized(app, scope, name)
}

func listOptionNames(app core.App, fieldID, userID string) ([]string, error) {
	return listNamedEntityNames(app, optionScope(fieldID, strings.TrimSpace(userID)))
}

func listTagNames(app core.App, userID string) ([]string, error) {
	return listNamedEntityNames(app, tagScope(strings.TrimSpace(userID)))
}

func listNamedEntityNames(app core.App, scope namedScope) ([]string, error) {
	if scope.params["userId"] == "" {
		return nil, nil
	}

	names := make([]string, 0)
	seen := map[string]struct{}{}
	offset := 0
	for {
		if len(names) >= ai.MaxExtractionCatalogNames {
			return names, nil
		}
		records, err := app.FindRecordsByFilter(scope.collection, scope.filter, "name,id",
			namedEntityListPageSize, offset, scope.params)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", scope.collection, err)
		}
		for _, rec := range records {
			names = addUniqueCatalogName(names, seen, rec.GetString("name"), ai.MaxExtractionCatalogNames)
			if len(names) >= ai.MaxExtractionCatalogNames {
				return names, nil
			}
		}
		if len(records) < namedEntityListPageSize {
			return names, nil
		}
		offset += namedEntityListPageSize
	}
}

func addUniqueCatalogName(names []string, seen map[string]struct{}, raw string, max int) []string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return names
	}
	key := strings.ToLower(name)
	if _, ok := seen[key]; ok {
		return names
	}
	if max > 0 && len(names) >= max {
		return names
	}
	seen[key] = struct{}{}
	return append(names, name)
}

func normalizeNamedEntityKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ToLower(norm.NFD.String(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func findNamedEntityNormalized(app core.App, scope namedScope, name string) (string, error) {
	key := normalizeNamedEntityKey(name)
	if key == "" {
		return "", nil
	}

	offset := 0
	for {
		records, err := app.FindRecordsByFilter(scope.collection, scope.filter, "name,id",
			namedEntityListPageSize, offset, scope.params)
		if err != nil {
			return "", err
		}
		for _, rec := range records {
			if normalizeNamedEntityKey(rec.GetString("name")) == key {
				return rec.Id, nil
			}
		}
		if len(records) < namedEntityListPageSize {
			return "", nil
		}
		offset += namedEntityListPageSize
	}
}

func findNamedEntity(app core.App, scope namedScope, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	params := dbx.Params{"name": value}
	maps.Copy(params, scope.params)
	existing, err := app.FindRecordsByFilter(scope.collection, scope.filter+" && name = {:name}", "", 1, 0, params)
	if err != nil {
		return "", err
	}
	if len(existing) == 0 {
		return "", nil
	}
	return existing[0].Id, nil
}

// Resolves extracted tag names against the user's tags, returning matched ids
// and the names it could not match.
//
// It never creates: tags are a vocabulary the user curates, so a name the
// archive does not have is one the model invented. Matching is
// punctuation/accent/case-insensitive, so "invoices" still finds "Invoices".
//
// A missing user id is an error rather than an empty answer: the caller writes
// the result over the document's tags, so "no tags" would clear them.
func matchTags(app core.App, userID string, names []string) (matched []string, dropped []string, err error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil, fmt.Errorf("user id is required")
	}
	if len(names) == 0 {
		return []string{}, nil, nil
	}

	index, err := tagIndexByKey(app, userID)
	if err != nil {
		return nil, nil, err
	}

	matched = make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		id, ok := index[normalizeNamedEntityKey(name)]
		if !ok {
			dropped = append(dropped, name)
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		matched = append(matched, id)
	}
	return matched, dropped, nil
}

// addMatchedTags adds the model's tags to the ones the document already has,
// which the uploader, the consume folder or the owner set.
func addMatchedTags(app core.App, document *core.Record, names []string) (matched, dropped []string, err error) {
	matched, dropped, err = matchTags(app, document.GetString("user"), names)
	if err != nil {
		return nil, nil, err
	}
	document.Set("tags", list.ToUniqueStringSlice(append(document.GetStringSlice("tags"), matched...)))
	return matched, dropped, nil
}

// First writer wins, so two tags that normalize alike resolve to the older one
// instead of flipping with page order.
const (
	maxTagSuggestions     = 3
	maxTagSuggestionRunes = 100 // tags.name max length
)

// pendingTagSuggestions turns the names matchTags could not resolve into the
// proposals kept for the reviewer: control characters gone, trimmed, deduped
// on the vocabulary's key, capped, and never longer than a tag name may be,
// since each one is handed to the tags collection unchanged on accept.
func pendingTagSuggestions(dropped []string) []string {
	kept := make([]string, 0, maxTagSuggestions)
	seen := map[string]struct{}{}
	for _, raw := range dropped {
		name := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, raw))
		key := normalizeNamedEntityKey(name)
		if key == "" || len([]rune(name)) > maxTagSuggestionRunes {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, name)
		if len(kept) == maxTagSuggestions {
			break
		}
	}
	return kept
}

func tagIndexByKey(app core.App, userID string) (map[string]string, error) {
	index := map[string]string{}
	offset := 0
	for {
		records, err := app.FindRecordsByFilter(
			"tags",
			"user = {:userId}",
			"name,id",
			namedEntityListPageSize,
			offset,
			map[string]any{"userId": userID},
		)
		if err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		for _, rec := range records {
			key := normalizeNamedEntityKey(rec.GetString("name"))
			if key == "" {
				continue
			}
			if _, ok := index[key]; !ok {
				index[key] = rec.Id
			}
		}
		if len(records) < namedEntityListPageSize {
			return index, nil
		}
		offset += namedEntityListPageSize
	}
}

func validateDocumentTagOwnership(app core.App, record *core.Record) error {
	userID := strings.TrimSpace(record.GetString("user"))
	for _, tagID := range record.GetStringSlice("tags") {
		if err := requireOwnedRelation(app, "tags", "tag", tagID, userID); err != nil {
			return err
		}
	}
	return nil
}

func requireOwnedRelation(app core.App, collection, label, id, userID string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	related, err := app.FindRecordById(collection, id)
	if err != nil {
		return fmt.Errorf("%s not found", label)
	}
	if related.GetString("user") != userID {
		return fmt.Errorf("%s does not belong to this user", label)
	}
	return nil
}

// Import paths only (archive restore, paperless-ngx import, the ngx REST API):
// those move data the user already owns, so creating a tag there is the user
// acting. The extraction pipeline deliberately uses matchTags instead.
func EnsureTag(app core.App, userID, name string) (id string, created bool, err error) {
	userID = strings.TrimSpace(userID)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	if userID == "" {
		return "", false, fmt.Errorf("user id is required")
	}

	if existingID, err := findTagByName(app, userID, name); err != nil {
		return "", false, err
	} else if existingID != "" {
		return existingID, false, nil
	}

	tagsCollection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		return "", false, err
	}
	tag := core.NewRecord(tagsCollection)
	tag.Set("user", userID)
	tag.Set("name", name)
	if err := app.Save(tag); err != nil {
		if existingID, findErr := findTagByName(app, userID, name); findErr == nil && existingID != "" {
			return existingID, false, nil
		}
		return "", false, err
	}
	return tag.Id, true, nil
}

func findTagByName(app core.App, userID, name string) (string, error) {
	existing, err := app.FindRecordsByFilter(
		"tags",
		"user = {:user} && name = {:name}",
		"",
		1,
		0,
		map[string]any{"user": userID, "name": name},
	)
	if err != nil {
		return "", err
	}
	if len(existing) == 0 {
		return "", nil
	}
	return existing[0].Id, nil
}

// NormalizeTagKey exposes the key apply_metadata matches tag names on, so a
// caller outside the pipeline resolves a model's answer exactly as apply does.
func NormalizeTagKey(s string) string { return normalizeNamedEntityKey(s) }
