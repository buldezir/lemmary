package migrations

import "testing"

func TestChatSessionFiltersMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := addChatSessionFiltersField(app); err != nil {
			t.Fatalf("addChatSessionFiltersField on an already-migrated app: %v", err)
		}
	}
	collection, err := app.FindCollectionByNameOrId("chat_sessions")
	if err != nil {
		t.Fatalf("chat_sessions collection: %v", err)
	}
	if collection.Fields.GetByName("filters") == nil {
		t.Fatal("chat_sessions has no filters field")
	}
}
