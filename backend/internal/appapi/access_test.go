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
