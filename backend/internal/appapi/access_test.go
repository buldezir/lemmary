package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// A shared document carries its owner's tags, so a filter naming one has to
// resolve against that owner too -- and against nobody else.
func TestNameScopeReachesOwnersWhoShareWithTheCaller(t *testing.T) {
	app := bootQueueApp(t)
	reader := makeQueueUser(t, app, "reader@example.com")
	sharer := makeQueueUser(t, app, "sharer@example.com")
	stranger := makeQueueUser(t, app, "stranger@example.com")
	shared := makeQueueDocument(t, app, sharer, "completed", "text")
	makeQueueDocument(t, app, stranger, "completed", "text")
	sharerTag := makeQueueTag(t, app, sharer, "Invoice")
	makeQueueTag(t, app, stranger, "Invoice")

	shares, err := app.FindCollectionByNameOrId(CollectionShares)
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(shares)
	grant.Set("document", shared.Id)
	grant.Set("user", reader)
	if err := app.Save(grant); err != nil {
		t.Fatal(err)
	}

	r := &agentRetriever{app: app, userID: reader}
	if got := r.sharedIDs(); len(got) != 1 || got[0] != shared.Id {
		t.Fatalf("shared documents = %v, want [%s]", got, shared.Id)
	}
	ids, err := findTagIDsByNames(app, []string{"Invoice"}, r.nameScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != sharerTag.Id {
		t.Fatalf("tag ids = %v, want only the sharer's %s", ids, sharerTag.Id)
	}
}

func TestLibraryGrantReachesCustomAPIsAndRetrieval(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "library-owner@example.com")
	reader := makeQueueUser(t, app, "library-reader@example.com")
	stranger := makeQueueUser(t, app, "library-stranger@example.com")
	doc := makeQueueDocument(t, app, owner, "completed", "text")
	coll, err := app.FindCollectionByNameOrId("library_shares")
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(coll)
	grant.Set("owner", owner)
	grant.Set("recipient", reader)
	if err := app.Save(grant); err != nil {
		t.Fatal(err)
	}
	if !CanReadDocument(app, doc, reader) || CanReadDocument(app, doc, stranger) {
		t.Fatal("incorrect library access")
	}
	ids, owners, err := SharedWith(app, reader)
	if err != nil || len(ids) != 1 || ids[0] != doc.Id || len(owners) != 1 || owners[0] != owner {
		t.Fatalf("retrieval scope: %v %v %v", ids, owners, err)
	}
	if err := app.Delete(grant); err != nil {
		t.Fatal(err)
	}
	if CanReadDocument(app, doc, reader) {
		t.Fatal("revoked library readable")
	}
}
