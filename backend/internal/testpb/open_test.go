package testpb_test

import (
	"testing"

	"lemmary/backend/internal/testpb"

	_ "lemmary/backend/migrations"
)

func TestOpenHasTheAppSchema(t *testing.T) {
	app := testpb.Open(t)
	if _, err := app.FindCollectionByNameOrId("users"); err != nil {
		t.Fatalf("users collection: %v", err)
	}
	if _, err := app.FindCollectionByNameOrId("documents"); err != nil {
		t.Fatalf("documents collection: %v", err)
	}
}
