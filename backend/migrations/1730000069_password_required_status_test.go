package migrations

import (
	"slices"
	"testing"
)

func TestPasswordRequiredStatusExistsAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	// The initial migration carries the current values for fresh installs, so
	// take the value out again to prove the upgrade path adds it.
	collection, field, err := documentStatusField(app)
	if err != nil {
		t.Fatal(err)
	}
	field.Values = slices.DeleteFunc(field.Values, func(value string) bool { return value == passwordRequiredStatus })
	if err := app.Save(collection); err != nil {
		t.Fatalf("restore old schema: %v", err)
	}

	for range 2 {
		if err := addPasswordRequiredStatus(app); err != nil {
			t.Fatalf("addPasswordRequiredStatus: %v", err)
		}
	}
	_, field, err = documentStatusField(app)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Index(field.Values, passwordRequiredStatus); got < 0 || slices.Index(field.Values[got+1:], passwordRequiredStatus) >= 0 {
		t.Fatalf("processing_status values = %v; want password_required exactly once", field.Values)
	}
}
