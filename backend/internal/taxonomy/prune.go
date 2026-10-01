// Package taxonomy maintains the per-user tag / correspondent / document type
// taxonomy that documents point at.
package taxonomy

import (
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// PruneResult counts the records a prune removed, per collection. Tags is
// always 0 and stays in the shape because the API response and the Maintenance
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
// carries. Finding the orphans and deleting them share one transaction, so a
// value saved concurrently cannot end up pointing at an option this prune just
// removed.
func PruneOrphans(app core.App) (PruneResult, error) {
	var result PruneResult

	err := app.RunInTransaction(func(txApp core.App) error {
		for _, target := range []struct {
			fieldID string
			removed *int
		}{
			{models.CorrespondentFieldID, &result.Correspondents},
			{models.DocumentTypeFieldID, &result.DocumentTypes},
		} {
			n, err := deleteOrphanOptions(txApp, target.fieldID)
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

// deleteOrphanOptions lists before the first delete, and deletes through the
// app so the search index hears about each one.
func deleteOrphanOptions(app core.App, fieldID string) (int, error) {
	var ids []string
	err := app.DB().NewQuery(`SELECT o.id FROM ` + models.CustomFieldOptionsCollection + ` o
		WHERE o.field = {:field} AND NOT EXISTS (
			SELECT 1 FROM ` + models.CustomFieldValuesCollection + ` v WHERE v.option = o.id)`).
		Bind(dbx.Params{"field": fieldID}).Column(&ids)
	if err != nil {
		return 0, fmt.Errorf("list orphan %s options: %w", fieldID, err)
	}
	for _, id := range ids {
		record, err := app.FindRecordById(models.CustomFieldOptionsCollection, id)
		if err != nil {
			return 0, err
		}
		if err := app.Delete(record); err != nil {
			return 0, fmt.Errorf("delete %s option %s: %w", fieldID, id, err)
		}
	}
	return len(ids), nil
}
