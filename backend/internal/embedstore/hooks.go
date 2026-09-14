package embedstore

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
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

// touchesEmbeddedText reports whether this save changed the text the chunks were
// built from.
//
// ocr_text is the whole list, because ocr_text is the whole input: a chunk is a
// slice of that column and nothing else. Renaming a document, retagging it or
// rewriting its summary leaves every stored vector exactly as valid as it was,
// and re-embedding to confirm that would be an archive's worth of provider
// calls for no change in the result.
//
// Without the check at all, every processing_status flip during a pipeline run
// would mark the document stale and buy it another full re-embed.
func touchesEmbeddedText(record *core.Record) bool {
	if record == nil {
		return false
	}
	original := record.Original()
	if original == nil {
		return true
	}
	return strings.TrimSpace(record.GetString("ocr_text")) !=
		strings.TrimSpace(original.GetString("ocr_text"))
}
