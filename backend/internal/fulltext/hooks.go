package fulltext

import (
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

const (
	collectionDocuments      = "documents"
	collectionTags           = "tags"
	collectionCorrespondents = "correspondents"
	collectionDocumentTypes  = "document_types"
)

// Register opens the index after app migrations (outer bootstrap) and keeps it
// in sync with document and named-entity changes.
func Register(app core.App, idx *Index) {
	if idx == nil {
		idx = New()
	}

	app.OnBootstrap().Bind(&hook.Handler[*core.BootstrapEvent]{
		Priority: -10,
		Func: func(e *core.BootstrapEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			if err := idx.Open(e.App.DataDir()); err != nil {
				e.App.Logger().Error("fulltext index open failed", slog.Any("error", err))
				return nil
			}
			registerRecordHooks(e.App, idx)
			if idx.NeedsRebuild() || idx.ShouldHeal(e.App) {
				n, err := idx.Rebuild(e.App)
				if err != nil {
					e.App.Logger().Error("fulltext rebuild failed", slog.Any("error", err))
					return nil
				}
				e.App.Logger().Info("fulltext index rebuilt", slog.Int("documents", n))
			}
			return nil
		},
	})

	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		if err := idx.Close(); err != nil {
			e.App.Logger().Warn("fulltext index close failed", slog.Any("error", err))
		}
		return e.Next()
	})
}

func registerRecordHooks(app core.App, idx *Index) {
	upsertDoc := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		idx.EnqueueUpsert(e.App, e.Record.Id)
		return nil
	}
	app.OnRecordAfterCreateSuccess(collectionDocuments).BindFunc(upsertDoc)
	app.OnRecordAfterUpdateSuccess(collectionDocuments).BindFunc(upsertDoc)
	app.OnRecordAfterDeleteSuccess(collectionDocuments).BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		idx.EnqueueDelete(e.Record.Id)
		return nil
	})

	reindexNamed := func(collection, field string) func(*core.RecordEvent) error {
		return func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			idx.EnqueueReindexEntity(e.App, collection, field, e.Record.Id)
			return nil
		}
	}

	app.OnRecordAfterUpdateSuccess(collectionTags).BindFunc(reindexNamed(collectionTags, FieldTags))
	app.OnRecordAfterDeleteSuccess(collectionTags).BindFunc(reindexNamed(collectionTags, FieldTags))
	app.OnRecordAfterUpdateSuccess(collectionCorrespondents).BindFunc(reindexNamed(collectionCorrespondents, FieldCorrespondent))
	app.OnRecordAfterDeleteSuccess(collectionCorrespondents).BindFunc(reindexNamed(collectionCorrespondents, FieldCorrespondent))
	app.OnRecordAfterUpdateSuccess(collectionDocumentTypes).BindFunc(reindexNamed(collectionDocumentTypes, FieldDocumentType))
	app.OnRecordAfterDeleteSuccess(collectionDocumentTypes).BindFunc(reindexNamed(collectionDocumentTypes, FieldDocumentType))
}

func reindexDocumentsForEntity(app core.App, idx *Index, collection, field, entityID string) {
	ids := map[string]struct{}{}

	filter := field + " = {:id}"
	if collection == collectionTags {
		filter = "tags.id ?= {:id}"
	}
	sqlIDs, err := documentIDsByFilter(app, filter, map[string]any{"id": entityID})
	if err != nil {
		app.Logger().Error("fulltext named-entity lookup failed", slog.String("collection", collection), slog.Any("error", err))
	} else {
		for _, id := range sqlIDs {
			ids[id] = struct{}{}
		}
	}

	bleveIDs, err := idx.IDsByKeyword(field, entityID)
	if err != nil {
		app.Logger().Error("fulltext named-entity index lookup failed", slog.String("field", field), slog.Any("error", err))
	} else {
		for _, id := range bleveIDs {
			ids[id] = struct{}{}
		}
	}

	names := newNameCache(app)
	for id := range ids {
		rec, err := app.FindRecordById(collectionDocuments, id)
		if err != nil {
			if delErr := idx.deleteUnlocked(id); delErr != nil {
				app.Logger().Error("fulltext delete after entity change failed", slog.String("id", id), slog.Any("error", delErr))
			}
			continue
		}
		if err := idx.putUnlocked(rec.Id, buildWith(names, rec)); err != nil {
			app.Logger().Error("fulltext reindex after entity change failed", slog.String("id", id), slog.Any("error", err))
		}
	}
}

func documentIDsByFilter(app core.App, filter string, params map[string]any) ([]string, error) {
	page := lookupPageSize
	if page <= 0 {
		page = defaultLookupPage
	}
	var ids []string
	offset := 0
	for {
		records, err := app.FindRecordsByFilter(collectionDocuments, filter, "", page, offset, params)
		if err != nil {
			return ids, err
		}
		for _, rec := range records {
			ids = append(ids, rec.Id)
		}
		if len(records) < page {
			return ids, nil
		}
		offset += page
	}
}
