package embedstore

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
)

// Listener lets a derived vector index follow chunk changes without this
// package importing it. The direction matters: the store is the durable copy
// and must stay usable with no index at all.
type Listener interface {
	ChunksReplaced(app core.App, documentID string)
	ChunksDeleted(documentID string)
}

var (
	listenerMu sync.RWMutex
	listener   Listener
)

// Called once from wiring; passing nil detaches.
func SetListener(l Listener) {
	listenerMu.Lock()
	listener = l
	listenerMu.Unlock()
}

// Called after the transaction commits, never inside it: a listener that reads
// the rows back must not see them before they are durable.
func NotifyReplaced(app core.App, documentID string) {
	listenerMu.RLock()
	l := listener
	listenerMu.RUnlock()
	if l != nil {
		l.ChunksReplaced(app, documentID)
	}
}

func NotifyDeleted(documentID string) {
	listenerMu.RLock()
	l := listener
	listenerMu.RUnlock()
	if l != nil {
		l.ChunksDeleted(documentID)
	}
}

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

// ocr_text is the whole list because it is the whole input: a rename or a
// retag leaves every stored vector as valid as it was. Without the check, every
// processing_status flip during a pipeline run would buy a full re-embed.
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
