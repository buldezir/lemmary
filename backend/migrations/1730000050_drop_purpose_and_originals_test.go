package migrations

import "testing"

func TestPurposeAndOriginalsAreGoneAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	for _, f := range droppedMetadataFields {
		coll, err := app.FindCollectionByNameOrId(f.collection)
		if err != nil {
			t.Fatalf("%s collection: %v", f.collection, err)
		}
		if coll.Fields.GetByName(f.field) != nil {
			t.Fatalf("%s.%s survived the migration", f.collection, f.field)
		}
	}
}

func TestDropPurposeAndOriginalsIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := restorePurposeAndOriginals(app); err != nil {
			t.Fatalf("restorePurposeAndOriginals: %v", err)
		}
	}
	coll, err := app.FindCollectionByNameOrId("documents")
	if err != nil || coll.Fields.GetByName("title_original") == nil {
		t.Fatalf("down did not restore documents.title_original: %v", err)
	}
	for range 2 {
		if err := dropPurposeAndOriginals(app); err != nil {
			t.Fatalf("dropPurposeAndOriginals: %v", err)
		}
	}
}
