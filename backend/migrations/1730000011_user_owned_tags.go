package migrations

import (
	"sort"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Tags were the last globally shared taxonomy: every user saw every tag, and two
// users could not both own a tag with the same name. This brings them in line
// with document_types/correspondents (see 1730000008) so the whole taxonomy is
// per-user.
//
// Existing tags are assigned to the user whose documents reference them. A tag
// referenced by several users is cloned per extra owner and those users'
// documents are repointed at their own copy, so no document loses a tag.
func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		if err := addOptionalUserField(app, "tags", users.Id); err != nil {
			return err
		}

		if err := assignTagOwners(app); err != nil {
			return err
		}

		if err := lockNamedEntityOwnership(app, "tags", "idx_tags_name", "idx_tags_user_name"); err != nil {
			return err
		}
		return setRelationCascadeDelete(app, "tags", "user", true)
	}, func(app core.App) error {
		return unlockNamedEntityOwnership(app, "tags", "idx_tags_name", "idx_tags_user_name")
	})
}

// assignTagOwners gives every tag a user, splitting tags shared by several users.
func assignTagOwners(app core.App) error {
	documents, err := app.FindAllRecords("documents")
	if err != nil {
		return err
	}

	// tag id -> owning user ids, in a deterministic order.
	owners := map[string][]string{}
	for _, doc := range documents {
		userID := doc.GetString("user")
		if userID == "" {
			continue
		}
		for _, tagID := range doc.GetStringSlice("tags") {
			if tagID == "" || containsString(owners[tagID], userID) {
				continue
			}
			owners[tagID] = append(owners[tagID], userID)
		}
	}
	for tagID := range owners {
		sort.Strings(owners[tagID])
	}

	fallbackOwner, err := ownerUserIDForNamedEntities(app)
	if err != nil {
		return err
	}

	tags, err := app.FindAllRecords("tags")
	if err != nil {
		return err
	}

	// original tag id -> (user id -> that user's tag id)
	clones := map[string]map[string]string{}
	for _, tag := range tags {
		tagOwners := owners[tag.Id]
		primary := fallbackOwner
		if len(tagOwners) > 0 {
			primary = tagOwners[0]
		}
		if primary == "" {
			// No users at all: nothing can own this tag, so drop it.
			if err := app.Delete(tag); err != nil {
				return err
			}
			continue
		}

		tag.Set("user", primary)
		if err := app.Save(tag); err != nil {
			return err
		}

		if len(tagOwners) < 2 {
			continue
		}
		perUser := map[string]string{primary: tag.Id}
		for _, userID := range tagOwners[1:] {
			clone := core.NewRecord(tag.Collection())
			clone.Set("user", userID)
			clone.Set("name", tag.GetString("name"))
			if err := app.Save(clone); err != nil {
				return err
			}
			perUser[userID] = clone.Id
		}
		clones[tag.Id] = perUser
	}

	if len(clones) == 0 {
		return nil
	}
	return repointDocumentTags(app, documents, clones)
}

// repointDocumentTags rewrites each document's tags to the copy owned by its user.
func repointDocumentTags(app core.App, documents []*core.Record, clones map[string]map[string]string) error {
	for _, doc := range documents {
		userID := doc.GetString("user")
		if userID == "" {
			continue
		}
		tagIDs := doc.GetStringSlice("tags")
		changed := false
		next := make([]string, 0, len(tagIDs))
		for _, tagID := range tagIDs {
			if perUser, ok := clones[tagID]; ok {
				if ownID, ok := perUser[userID]; ok && ownID != tagID {
					next = append(next, ownID)
					changed = true
					continue
				}
			}
			next = append(next, tagID)
		}
		if !changed {
			continue
		}
		doc.Set("tags", next)
		if err := app.Save(doc); err != nil {
			return err
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
