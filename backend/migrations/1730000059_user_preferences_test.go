package migrations

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestUserPreferencesMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := addUserPreferencesField(app); err != nil {
			t.Fatalf("addUserPreferencesField on an already-migrated app: %v", err)
		}
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	if users.Fields.GetByName("preferences") == nil {
		t.Fatal("users has no preferences field")
	}
}

// The account writes this field itself, so its size is the one thing the
// database holds it to.
func TestUserPreferencesAreCappedInSize(t *testing.T) {
	app := bootMigratedApp(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}

	for name, c := range map[string]struct {
		value any
		valid bool
	}{
		"empty":     {nil, true},
		"choices":   {map[string]any{"card_layout": "preview", "language": "de"}, true},
		"oversized": {map[string]any{"note": strings.Repeat("x", 5000)}, false},
	} {
		record := core.NewRecord(users)
		record.Set("email", "preferences@example.test")
		record.SetPassword("preferences-password-1")
		record.Set("preferences", c.value)
		if err := app.Validate(record); (err == nil) != c.valid {
			t.Errorf("%s: valid=%v, got err %v", name, c.valid, err)
		}
	}
}
