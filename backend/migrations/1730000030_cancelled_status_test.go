package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestCancelledStatusesExistAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	// Recreate the schema immediately before this migration. The initial
	// migration also carries the current values for fresh installs, so merely
	// booting the full chain would not prove the upgrade path widened anything.
	for _, target := range []struct {
		collection string
		field      string
	}{
		{"documents", "processing_status"},
		{"processing_jobs", "status"},
	} {
		collection, err := app.FindCollectionByNameOrId(target.collection)
		if err != nil {
			t.Fatalf("find %s: %v", target.collection, err)
		}
		field := collection.Fields.GetByName(target.field).(*core.SelectField)
		field.Values = slices.DeleteFunc(field.Values, func(value string) bool { return value == "cancelled" })
		if err := app.Save(collection); err != nil {
			t.Fatalf("restore old %s schema: %v", target.collection, err)
		}
	}
	if err := addCancelledStatuses(app); err != nil {
		t.Fatalf("addCancelledStatuses: %v", err)
	}

	for _, target := range []struct {
		collection string
		field      string
	}{
		{"documents", "processing_status"},
		{"processing_jobs", "status"},
	} {
		collection, err := app.FindCollectionByNameOrId(target.collection)
		if err != nil {
			t.Fatalf("find %s: %v", target.collection, err)
		}
		field, ok := collection.Fields.GetByName(target.field).(*core.SelectField)
		if !ok {
			t.Fatalf("%s.%s is not a select field", target.collection, target.field)
		}
		if !slices.Contains(field.Values, "cancelled") {
			t.Fatalf("%s.%s values = %v; cancelled is missing", target.collection, target.field, field.Values)
		}
	}
}

func TestCancelledStatusMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)
	if err := addCancelledStatuses(app); err != nil {
		t.Fatalf("addCancelledStatuses on migrated app: %v", err)
	}
	if err := addCancelledStatuses(app); err != nil {
		t.Fatalf("addCancelledStatuses a second time: %v", err)
	}
}
