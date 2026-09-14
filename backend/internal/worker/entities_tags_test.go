package worker

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// Bootstrap alone runs only PocketBase's system migrations, so tags and users
// do not exist until the app migrations run too.
func bootTagTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	if err := app.RunAppMigrations(); err != nil {
		t.Fatalf("run app migrations: %v", err)
	}
	return app
}

func createTagUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("email", email)
	record.SetPassword("test-password-123")
	if err := app.Save(record); err != nil {
		t.Fatalf("save user %s: %v", email, err)
	}
	return record.Id
}

func createTag(t *testing.T, app core.App, name, userID string) string {
	t.Helper()
	id, _, err := EnsureTag(app, userID, name)
	if err != nil {
		t.Fatalf("create tag %q: %v", name, err)
	}
	return id
}

func TestMatchTagsResolvesAndDrops(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	other := createTagUser(t, app, "other@example.com")

	invoices := createTag(t, app, "Invoices", owner)
	buro := createTag(t, app, "Büro", owner)
	theirs := createTag(t, app, "Secret", other)

	matched, dropped, err := matchTags(app, owner, []string{
		"Invoices", // exact
		"  büro ",  // normalized: case, accent and surrounding space
		"Rechnung", // invented by the model, no such tag
		"invoices", // same tag again, must not appear twice
		"Secret",   // belongs to another user
		"   ",      // blank, neither matched nor reported
	})
	if err != nil {
		t.Fatalf("matchTags: %v", err)
	}

	want := []string{invoices, buro}
	if len(matched) != len(want) {
		t.Fatalf("matched = %v, want %v", matched, want)
	}
	for i, id := range want {
		if matched[i] != id {
			t.Fatalf("matched[%d] = %q, want %q (order follows the model's answer)", i, matched[i], id)
		}
	}
	for _, id := range matched {
		if id == theirs {
			t.Fatal("matched another user's tag")
		}
	}

	wantDropped := []string{"Rechnung", "Secret"}
	if len(dropped) != len(wantDropped) {
		t.Fatalf("dropped = %v, want %v", dropped, wantDropped)
	}
	for i, name := range wantDropped {
		if dropped[i] != name {
			t.Fatalf("dropped[%d] = %q, want %q", i, dropped[i], name)
		}
	}
}

func TestMatchTagsCreatesNothing(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	createTag(t, app, "Invoices", owner)

	before, err := app.CountRecords("tags")
	if err != nil {
		t.Fatalf("count tags: %v", err)
	}

	if _, dropped, err := matchTags(app, owner, []string{"Rechnung", "Versicherung"}); err != nil {
		t.Fatalf("matchTags: %v", err)
	} else if len(dropped) != 2 {
		t.Fatalf("dropped = %v, want both names", dropped)
	}

	after, err := app.CountRecords("tags")
	if err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if after != before {
		t.Fatalf("tag count went %d -> %d; matchTags must never create", before, after)
	}
}

func TestMatchTagsNoNamesIsANoOp(t *testing.T) {
	// nil app on purpose: an empty answer may not reach the database.
	matched, dropped, err := matchTags(nil, "user1", nil)
	if err != nil {
		t.Fatalf("expected a no-op, got %v", err)
	}
	if len(matched) != 0 || len(dropped) != 0 {
		t.Fatalf("expected empty result, got %v / %v", matched, dropped)
	}
}

// Apply writes the result over the document's tags, so an unreadable owner has
// to fail the step rather than silently strip them.
func TestMatchTagsRequiresAUser(t *testing.T) {
	if _, _, err := matchTags(nil, "  ", []string{"Invoices"}); err == nil {
		t.Fatal("expected an error when the user id is empty")
	}
}

func TestListTagNamesEmptyUser(t *testing.T) {
	names, err := listTagNames(nil, "  ")
	if err != nil {
		t.Fatalf("empty user should be a no-op, got %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected no names, got %v", names)
	}
}
