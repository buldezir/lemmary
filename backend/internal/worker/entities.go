package worker

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/text/unicode/norm"

	"paperless-go/backend/internal/ai"
)

const namedEntityListPageSize = 500

// EnsureNamedEntity finds or creates a named entity (correspondent / document type)
// owned by userID. Lookup prefers exact name_original, then exact name, then a
// punctuation/accent-insensitive match. created is true only when a new record
// is inserted. Existing name and name_original values are left unchanged.
func EnsureNamedEntity(app core.App, collection, userID, displayName, originalName string) (id string, created bool, err error) {
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

	// Keep an existing display name and original so later extractions cannot
	// overwrite a user rename or swap translated/source values.
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
			// Unique (user, name) collisions must not fail apply; the matched record is kept as-is.
			return record.Id, nil
		}
	}
	return record.Id, nil
}

func ensureTags(app core.App, userID string, names []string) ([]string, error) {
	tagIDs := make([]string, 0, len(names))
	for _, name := range names {
		id, _, err := EnsureTag(app, userID, name)
		if err != nil {
			return nil, err
		}
		if id != "" {
			tagIDs = append(tagIDs, id)
		}
	}
	return tagIDs, nil
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

// EnsureTag finds or creates a tag owned by userID, matched by exact name.
// created is true only when a new record is inserted.
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
		// Race: a concurrent create may have won the unique (user, name) index.
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
