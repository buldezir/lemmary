package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		if err := addOptionalUserField(app, "document_types", users.Id); err != nil {
			return err
		}
		if err := addOptionalUserField(app, "correspondents", users.Id); err != nil {
			return err
		}

		ownerID, err := ownerUserIDForNamedEntities(app)
		if err != nil {
			return err
		}
		if ownerID != "" {
			if err := assignNamedEntityOwner(app, "document_types", ownerID); err != nil {
				return err
			}
			if err := assignNamedEntityOwner(app, "correspondents", ownerID); err != nil {
				return err
			}
		}

		if err := lockNamedEntityOwnership(app, "document_types", "idx_document_types_name", "idx_document_types_user_name"); err != nil {
			return err
		}
		return lockNamedEntityOwnership(app, "correspondents", "idx_correspondents_name", "idx_correspondents_user_name")
	}, func(app core.App) error {
		if err := unlockNamedEntityOwnership(app, "document_types", "idx_document_types_name", "idx_document_types_user_name"); err != nil {
			return err
		}
		return unlockNamedEntityOwnership(app, "correspondents", "idx_correspondents_name", "idx_correspondents_user_name")
	})
}

func addOptionalUserField(app core.App, collectionName, usersCollectionID string) error {
	coll, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return err
	}
	if coll.Fields.GetByName("user") == nil {
		coll.Fields.Add(&core.RelationField{
			Name:         "user",
			CollectionId: usersCollectionID,
			MaxSelect:    1,
		})
	}
	return app.Save(coll)
}

func ownerUserIDForNamedEntities(app core.App) (string, error) {
	docs, err := app.FindAllRecords("documents")
	if err != nil {
		return "", err
	}

	counts := map[string]int{}
	for _, doc := range docs {
		uid := doc.GetString("user")
		if uid == "" {
			continue
		}
		counts[uid]++
	}

	bestID := ""
	bestCount := -1
	for uid, n := range counts {
		if n > bestCount || (n == bestCount && uid < bestID) {
			bestCount = n
			bestID = uid
		}
	}
	if bestID != "" {
		return bestID, nil
	}

	users, err := app.FindRecordsByFilter("users", "", "id", 1, 0)
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", nil
	}
	return users[0].Id, nil
}

func assignNamedEntityOwner(app core.App, collection, ownerID string) error {
	records, err := app.FindAllRecords(collection)
	if err != nil {
		return err
	}
	for _, record := range records {
		record.Set("user", ownerID)
		if err := app.Save(record); err != nil {
			return err
		}
	}
	return nil
}

func lockNamedEntityOwnership(app core.App, collectionName, oldIndex, newIndex string) error {
	coll, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return err
	}

	if f := coll.Fields.GetByName("user"); f != nil {
		if rel, ok := f.(*core.RelationField); ok {
			rel.Required = true
		}
	}

	ownerRule := "user = @request.auth.id"
	coll.ListRule = types.Pointer(ownerRule)
	coll.ViewRule = types.Pointer(ownerRule)
	coll.CreateRule = types.Pointer(ownerRule)
	coll.UpdateRule = types.Pointer(ownerRule)
	coll.DeleteRule = types.Pointer(ownerRule)

	coll.RemoveIndex(oldIndex)
	coll.AddIndex(newIndex, true, "user, name", "")
	return app.Save(coll)
}

func unlockNamedEntityOwnership(app core.App, collectionName, oldIndex, newIndex string) error {
	coll, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return nil
	}

	authRule := "@request.auth.id != ''"
	coll.ListRule = types.Pointer(authRule)
	coll.ViewRule = types.Pointer(authRule)
	coll.CreateRule = types.Pointer(authRule)
	coll.UpdateRule = types.Pointer(authRule)
	coll.DeleteRule = types.Pointer(authRule)

	if f := coll.Fields.GetByName("user"); f != nil {
		coll.Fields.RemoveById(f.GetId())
	}
	coll.RemoveIndex(newIndex)
	coll.AddIndex(oldIndex, true, "name", "")
	return app.Save(coll)
}
