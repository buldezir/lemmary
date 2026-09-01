package embedstore

import (
	"log/slog"
	"strings"
	"sync"

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
// document has to re-embed it and changing its file size does not.
var staleFields = []string{
	"ocr_text", "title", "title_original", "purpose", "summary",
	"document_type", "correspondent", "people_or_organizations", "document_date",
}

// Register keeps the tables in step with the documents collection.
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
}

const collectionDocuments = "documents"

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
		if field == "people_or_organizations" {
			if !equalStrings(models.PeopleOrOrganizations(record), models.PeopleOrOrganizations(original)) {
				return true
			}
			continue
		}
		if strings.TrimSpace(record.GetString(field)) != strings.TrimSpace(original.GetString(field)) {
			return true
		}
	}
	// Tags are a relation, so they arrive as an id list rather than as text.
	return !equalStrings(record.GetStringSlice("tags"), original.GetStringSlice("tags"))
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
