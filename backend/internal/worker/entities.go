package worker

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/list"
	"golang.org/x/text/unicode/norm"

	"lemmary/backend/internal/ai"
)

const namedEntityListPageSize = 500

var ensureNamedEntityMu sync.Mutex

// Lookup prefers exact name_original, then exact name, then a
// punctuation/accent-insensitive match. Existing names are left unchanged.
func EnsureNamedEntity(app core.App, collection, userID, displayName, originalName string) (id string, created bool, err error) {
	// ponytail: one process-wide lock over lookup-then-insert. Only (user, name)
	// is unique in the schema, so the name_original and normalized tiers below
	// are unbacked: two pipelines extracting "Müller GmbH" and "Muller G.m.b.H."
	// both miss, both insert, and no conflict fires for
	// reuseNamedEntityAfterConflict to clean up. A partial unique index on the
	// normalized key would be the real fix; this is a handful of indexed
	// queries on a path that just spent a minute in OCR and extraction.
	ensureNamedEntityMu.Lock()
	defer ensureNamedEntityMu.Unlock()

	userID = strings.TrimSpace(userID)
	displayName = strings.TrimSpace(displayName)
	originalName = strings.TrimSpace(originalName)
	if displayName == "" {
		return "", false, nil
	}
	if userID == "" {
		return "", false, fmt.Errorf("user id is required")
	}
	if originalName == "" {
		originalName = displayName
	}

	if existingID, err := findNamedEntity(app, collection, userID, "name_original", originalName); err != nil {
		return "", false, err
	} else if existingID != "" {
		id, err := updateNamedEntity(app, collection, existingID, displayName, originalName)
		return id, false, err
	}

	if existingID, err := findNamedEntity(app, collection, userID, "name", displayName); err != nil {
		return "", false, err
	} else if existingID != "" {
		id, err := updateNamedEntity(app, collection, existingID, displayName, originalName)
		return id, false, err
	}

	if existingID, err := findNamedEntityNormalized(app, collection, userID, displayName, originalName); err != nil {
		return "", false, err
	} else if existingID != "" {
		id, err := updateNamedEntity(app, collection, existingID, displayName, originalName)
		return id, false, err
	}

	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return "", false, err
	}

	record := core.NewRecord(coll)
	record.Set("user", userID)
	record.Set("name", displayName)
	record.Set("name_original", originalName)
	if err := app.Save(record); err != nil {
		if id, reused, reuseErr := reuseNamedEntityAfterConflict(app, collection, userID, displayName, originalName); reused {
			return id, false, reuseErr
		}
		return "", false, err
	}
	return record.Id, true, nil
}

func reuseNamedEntityAfterConflict(app core.App, collection, userID, displayName, originalName string) (id string, reused bool, err error) {
	existingID, findErr := findNamedEntity(app, collection, userID, "name", displayName)
	if findErr != nil {
		return "", false, findErr
	}
	if existingID == "" {
		existingID, findErr = findNamedEntity(app, collection, userID, "name_original", originalName)
		if findErr != nil {
			return "", false, findErr
		}
	}
	if existingID == "" {
		existingID, findErr = findNamedEntityNormalized(app, collection, userID, displayName, originalName)
		if findErr != nil {
			return "", false, findErr
		}
	}
	if existingID == "" {
		return "", false, nil
	}
	id, err = updateNamedEntity(app, collection, existingID, displayName, originalName)
	return id, true, err
}

func ensureNamedEntity(app core.App, collection, userID, displayName, originalName string) (string, error) {
	id, _, err := EnsureNamedEntity(app, collection, userID, displayName, originalName)
	return id, err
}

func listCorrespondentNames(app core.App, userID string) ([]string, error) {
	return listNamedEntityNames(app, "correspondents", userID)
}

func listDocumentTypeNames(app core.App, userID string) ([]string, error) {
	return listNamedEntityNames(app, "document_types", userID)
}

func listTagNames(app core.App, userID string) ([]string, error) {
	return listNamedEntityNames(app, "tags", userID)
}

func listNamedEntityNames(app core.App, collection, userID string) ([]string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil
	}

	names := make([]string, 0)
	seen := map[string]struct{}{}
	offset := 0
	for {
		if len(names) >= ai.MaxExtractionCatalogNames {
			return names, nil
		}
		records, err := app.FindRecordsByFilter(
			collection,
			"user = {:userId}",
			"name,id",
			namedEntityListPageSize,
			offset,
			map[string]any{"userId": userID},
		)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", collection, err)
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

func namedEntityMatchKeys(values ...string) map[string]struct{} {
	keys := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := normalizeNamedEntityKey(value)
		if key == "" {
			continue
		}
		keys[key] = struct{}{}
	}
	return keys
}

func findNamedEntityNormalized(app core.App, collection, userID, displayName, originalName string) (string, error) {
	keys := namedEntityMatchKeys(displayName, originalName)
	if len(keys) == 0 {
		return "", nil
	}

	offset := 0
	for {
		records, err := app.FindRecordsByFilter(
			collection,
			"user = {:userId}",
			"name,id",
			namedEntityListPageSize,
			offset,
			map[string]any{"userId": userID},
		)
		if err != nil {
			return "", err
		}
		for _, rec := range records {
			for _, field := range []string{"name", "name_original"} {
				key := normalizeNamedEntityKey(rec.GetString(field))
				if key == "" {
					continue
				}
				if _, ok := keys[key]; ok {
					return rec.Id, nil
				}
			}
		}
		if len(records) < namedEntityListPageSize {
			return "", nil
		}
		offset += namedEntityListPageSize
	}
}

func findNamedEntity(app core.App, collection, userID, field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	existing, err := app.FindRecordsByFilter(
		collection,
		"user = {:userId} && "+field+" = {:name}",
		"",
		1,
		0,
		map[string]any{"userId": userID, "name": value},
	)
	if err != nil {
		return "", err
	}
	if len(existing) == 0 {
		return "", nil
	}
	return existing[0].Id, nil
}

func updateNamedEntity(app core.App, collection, id, displayName, originalName string) (string, error) {
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		return "", err
	}

	// Later extractions must not overwrite a user rename or swap
	// translated/source values.
	changed := false
	if strings.TrimSpace(record.GetString("name")) == "" && displayName != "" {
		record.Set("name", displayName)
		changed = true
	}
	if strings.TrimSpace(record.GetString("name_original")) == "" && originalName != "" {
		record.Set("name_original", originalName)
		changed = true
	}
	if changed {
		if err := app.Save(record); err != nil {
			// A unique (user, name) collision must not fail apply.
			return record.Id, nil
		}
	}
	return record.Id, nil
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

func validateDocumentNamedEntityOwnership(app core.App, record *core.Record) error {
	userID := strings.TrimSpace(record.GetString("user"))
	if err := requireOwnedRelation(app, "document_types", "document type", record.GetString("document_type"), userID); err != nil {
		return err
	}
	if err := requireOwnedRelation(app, "correspondents", "correspondent", record.GetString("correspondent"), userID); err != nil {
		return err
	}
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
