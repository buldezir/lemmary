// Package taxonomy maintains the per-user tag / correspondent / document type
// taxonomy that documents point at.
package taxonomy

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

const (
	collectionDocuments      = "documents"
	collectionCorrespondents = "correspondents"
	collectionDocumentTypes  = "document_types"
)

const prunePageSize = 500

// PruneResult counts the records a prune removed, per collection. Tags is
// always 0 and stays in the shape because the API response and the Management
// page have read it since before tags became a hand-curated vocabulary.
type PruneResult struct {
	Tags           int `json:"tags"`
	Correspondents int `json:"correspondents"`
	DocumentTypes  int `json:"document_types"`
}

func (r PruneResult) Total() int {
	return r.Tags + r.Correspondents + r.DocumentTypes
}

// PruneOrphans deletes every correspondent and document type that no document
// references. Collecting the references and deleting share one transaction, so
// a document saved concurrently cannot end up pointing at an id this prune
// just removed.
func PruneOrphans(app core.App) (PruneResult, error) {
	var result PruneResult

	err := app.RunInTransaction(func(txApp core.App) error {
		refs, err := referencedIDs(txApp)
		if err != nil {
			return err
		}

		for _, target := range []struct {
			collection string
			removed    *int
		}{
			{collectionCorrespondents, &result.Correspondents},
			{collectionDocumentTypes, &result.DocumentTypes},
		} {
			n, err := deleteOrphans(txApp, target.collection, refs[target.collection])
			if err != nil {
				return err
			}
			*target.removed = n
		}
		return nil
	})
	if err != nil {
		return PruneResult{}, err
	}
	return result, nil
}

// documentRefs is the projection a prune reads from documents: the relation
// columns only, so OCR text is not loaded just to look at relations.
type documentRefs struct {
	Correspondent string `db:"correspondent"`
	DocumentType  string `db:"document_type"`
}

func referencedIDs(app core.App) (map[string]map[string]struct{}, error) {
	collection, err := app.FindCollectionByNameOrId(collectionDocuments)
	if err != nil {
		return nil, err
	}

	refs := map[string]map[string]struct{}{
		collectionCorrespondents: {},
		collectionDocumentTypes:  {},
	}

	offset := 0
	for {
		var page []documentRefs
		err := app.RecordQuery(collection).
			Select("correspondent", "document_type").
			OrderBy("id ASC").
			Limit(int64(prunePageSize)).
			Offset(int64(offset)).
			All(&page)
		if err != nil {
			return nil, fmt.Errorf("list document relations: %w", err)
		}

		for _, row := range page {
			addRef(refs[collectionCorrespondents], row.Correspondent)
			addRef(refs[collectionDocumentTypes], row.DocumentType)
		}

		if len(page) < prunePageSize {
			return refs, nil
		}
		offset += prunePageSize
	}
}

func addRef(ids map[string]struct{}, id string) {
	if id == "" {
		return
	}
	ids[id] = struct{}{}
}

func deleteOrphans(app core.App, collection string, referenced map[string]struct{}) (int, error) {
	orphans, err := orphanRecords(app, collection, referenced)
	if err != nil {
		return 0, err
	}
	for _, record := range orphans {
		if err := app.Delete(record); err != nil {
			return 0, fmt.Errorf("delete %s %s: %w", collection, record.Id, err)
		}
	}
	return len(orphans), nil
}

// orphanRecords pages through collection and keeps the unreferenced records. The
// listing finishes before the first delete: deleting while paging would shift
// the later offsets and skip records.
func orphanRecords(app core.App, collection string, referenced map[string]struct{}) ([]*core.Record, error) {
	var orphans []*core.Record
	offset := 0
	for {
		page, err := app.FindRecordsByFilter(collection, "id != ''", "id", prunePageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", collection, err)
		}
		for _, record := range page {
			if _, used := referenced[record.Id]; !used {
				orphans = append(orphans, record)
			}
		}
		if len(page) < prunePageSize {
			return orphans, nil
		}
		offset += prunePageSize
	}
}
