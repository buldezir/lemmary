package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// The colour is written straight from the browser through the collection rules,
// so the pattern on the column is the only thing standing between a tag and an
// arbitrary string rendered into a style attribute.
func TestTheTagColorColumnAcceptsAHexAndRefusesTheRest(t *testing.T) {
	app := bootMigratedApp(t)
	userID := makeUser(t, app, "tag-color@example.com")

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("find the tags collection: %v", err)
	}
	if _, ok := collection.Fields.GetByName("color").(*core.TextField); !ok {
		t.Fatal("color field is missing or not text")
	}

	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("name", "Invoices")
	record.Set("color", "#6e2620")
	if err := app.Save(record); err != nil {
		t.Fatalf("save a coloured tag: %v", err)
	}
	if got := record.GetString("color"); got != "#6e2620" {
		t.Fatalf("color read back as %q", got)
	}

	record.Set("color", "red")
	if err := app.Save(record); err == nil {
		t.Fatal("saved a tag with a non-hex colour")
	}

	// Empty is how a tag says it has no colour, so it has to stay legal.
	record.Set("color", "")
	if err := app.Save(record); err != nil {
		t.Fatalf("clear the colour: %v", err)
	}
}
