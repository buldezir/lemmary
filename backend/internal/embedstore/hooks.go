package embedstore

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// Listener is notified when a document's chunks change, so a derived vector
// index can follow along without this package importing it.
//
// The direction matters: embedstore is the durable copy and the Bleve chunk
// index is derived data that can be rebuilt from it at any time. Making the
// store call up into the index (rather than the index reach into the store on
// every write) is what keeps the store usable with no index at all, which is
// exactly the state this feature ships in before the index lands.
type Listener interface {
	ChunksReplaced(app core.App, documentID string)
	ChunksDeleted(documentID string)
}

var (
	listenerMu sync.RWMutex
	listener   Listener
)

// SetListener installs the process-wide listener. Called once from wiring;
// passing nil detaches.
func SetListener(l Listener) {
	listenerMu.Lock()
	listener = l
	listenerMu.Unlock()
}

// NotifyReplaced tells the listener a document's chunks were rewritten. Called
// by the embedder after the transaction commits, never inside it: a listener
// that reads the rows back must not be able to see them before they are durable.
func NotifyReplaced(app core.App, documentID string) {
	listenerMu.RLock()
	l := listener
	listenerMu.RUnlock()
	if l != nil {
		l.ChunksReplaced(app, documentID)
	}
}

// NotifyDeleted tells the listener a document's chunks are gone.
func NotifyDeleted(documentID string) {
	listenerMu.RLock()
	l := listener
	listenerMu.RUnlock()
	if l != nil {
		l.ChunksDeleted(documentID)
	}
}

// staleFields are the document fields a chunk's text is built from. A change to
// any of them means the stored vectors describe text that no longer exists.
//
// ocr_text is the body; the rest are the header chunk, which is why renaming a
// document has to re-embed it and changing its file size does not. It has to
// list every field chunk.Header renders -- tags and the *_original pair
// included -- or an edit to one of them leaves a vector describing metadata
// nobody can see any more.
var staleFields = []string{
	"ocr_text", "title", "title_original", "purpose", "purpose_original",
	"summary", "summary_original", "document_type", "correspondent",
	"people_or_organizations", "tags", "document_date",
}

// listFields are the staleFields that arrive as a list rather than as text, so
// they are compared element by element instead of through GetString.
var listFields = map[string]func(*core.Record) []string{
	"people_or_organizations": models.PeopleOrOrganizations,
	"tags":                    func(r *core.Record) []string { return r.GetStringSlice("tags") },
}

// entityFilters find every document that references one named-entity record.
// The header passage embeds the resolved *names* of these relations -- "Invoice"
// rather than an id -- so renaming one of them dates every document pointing at
// it, exactly as editing the document's own title would.
var entityFilters = map[string]string{
	collectionTags:           "tags.id ?= {:id}",
	collectionCorrespondents: "correspondent = {:id}",
	collectionDocumentTypes:  "document_type = {:id}",
}

// entityNameFields are the fields whose value reaches the header. A tag saved
// with a new colour is not a reason to re-embed an archive.
var entityNameFields = []string{"name", "name_original"}

// entityFanoutPage bounds one page of the fan-out. A tag can be on every
// document in an archive, and the ids are collected before anything is written.
const entityFanoutPage = 500

// Register keeps the tables in step with the documents collection and with the
// named entities the header passage quotes.
func Register(app core.App) {
	app.OnRecordAfterDeleteSuccess(collectionDocuments).BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if err := Delete(e.App.DB(), e.Record.Id); err != nil {
			e.App.Logger().Warn("embedstore delete failed; the orphan sweep will retry",
				"document", e.Record.Id, slog.Any("error", err))
			return nil
		}
		NotifyDeleted(e.Record.Id)
		return nil
	})

	app.OnRecordAfterUpdateSuccess(collectionDocuments).BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if !touchesEmbeddedText(e.Record) {
			return nil
		}
		if err := MarkStale(e.App.DB(), e.Record.Id); err != nil {
			e.App.Logger().Warn("embedstore mark stale failed",
				"document", e.Record.Id, slog.Any("error", err))
		}
		return nil
	})

	for _, collection := range []string{collectionTags, collectionCorrespondents, collectionDocumentTypes} {
		app.OnRecordAfterUpdateSuccess(collection).BindFunc(func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			if !renamed(e.Record) {
				return nil
			}
			n, err := markStaleForEntity(e.App.DB(), e.App, collection, e.Record.Id)
			if err != nil {
				e.App.Logger().Warn("embedstore entity fan-out failed",
					"collection", collection, "entity", e.Record.Id, slog.Any("error", err))
				return nil
			}
			if n > 0 {
				e.App.Logger().Info("renamed entity dated the documents that quote it",
					"collection", collection, "entity", e.Record.Id, "documents", n)
			}
			return nil
		})
	}
}

const (
	collectionDocuments      = "documents"
	collectionTags           = "tags"
	collectionCorrespondents = "correspondents"
	collectionDocumentTypes  = "document_types"
)

// documentFinder is the slice of core.App the entity fan-out needs: documents
// by filter, nothing else. Narrow because it is what makes the fan-out testable
// without a whole PocketBase app behind it.
type documentFinder interface {
	FindRecordsByFilter(
		collectionModelOrIdentifier any,
		filter string,
		sort string,
		limit int,
		offset int,
		params ...dbx.Params,
	) ([]*core.Record, error)
}

// renamed reports whether this save changed a name the header passage quotes.
func renamed(record *core.Record) bool {
	if record == nil {
		return false
	}
	original := record.Original()
	if original == nil {
		return true
	}
	for _, field := range entityNameFields {
		if strings.TrimSpace(record.GetString(field)) != strings.TrimSpace(original.GetString(field)) {
			return true
		}
	}
	return false
}

// markStaleForEntity marks every document referencing entityID stale, and
// reports how many. Marked rather than re-embedded on the spot: the backfill
// owns the provider calls, and a rename of a tag on ten thousand documents must
// not happen inside the request that renamed it.
func markStaleForEntity(db dbx.Builder, finder documentFinder, collection, entityID string) (int, error) {
	filter, ok := entityFilters[collection]
	if !ok || finder == nil || strings.TrimSpace(entityID) == "" {
		return 0, nil
	}

	marked := 0
	for offset := 0; ; offset += entityFanoutPage {
		records, err := finder.FindRecordsByFilter(
			collectionDocuments, filter, "", entityFanoutPage, offset,
			dbx.Params{"id": entityID},
		)
		if err != nil {
			return marked, fmt.Errorf("embedstore: documents for %s %s: %w", collection, entityID, err)
		}
		for _, record := range records {
			if err := MarkStale(db, record.Id); err != nil {
				return marked, err
			}
			marked++
		}
		if len(records) < entityFanoutPage {
			return marked, nil
		}
	}
}

// touchesEmbeddedText reports whether this save changed anything the chunks were
// built from. Without the check every processing_status flip during a pipeline
// run would mark the document stale and buy it another full re-embed.
func touchesEmbeddedText(record *core.Record) bool {
	if record == nil {
		return false
	}
	original := record.Original()
	if original == nil {
		return true
	}
	for _, field := range staleFields {
		// Relations and JSON arrays arrive as lists rather than as text, so
		// they are compared as lists; GetString on one says nothing useful.
		if values, ok := listFields[field]; ok {
			if !equalStrings(values(record), values(original)) {
				return true
			}
			continue
		}
		if strings.TrimSpace(record.GetString(field)) != strings.TrimSpace(original.GetString(field)) {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
