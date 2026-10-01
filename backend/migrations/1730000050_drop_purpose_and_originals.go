package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Drops purpose and the source-language copies kept beside translated
// metadata: with a result language set, extraction now writes the title,
// summary, type and correspondent in that language only. The correspondents'
// and document types' name_original goes with their collections in 1730000051.
// Down restores the columns, not what was in them.
var droppedMetadataFields = []struct {
	collection string
	field      string
	maxRunes   int
}{
	{"documents", "purpose", 1000},
	{"documents", "purpose_original", 1000},
	{"documents", "title_original", 500},
	{"documents", "summary_original", 5000},
}

func init() {
	m.Register(dropPurposeAndOriginals, restorePurposeAndOriginals)
}

func dropPurposeAndOriginals(app core.App) error {
	return editMetadataFields(app, func(coll *core.Collection, field string, _ int) bool {
		if coll.Fields.GetByName(field) == nil {
			return false
		}
		coll.Fields.RemoveByName(field)
		return true
	})
}

func restorePurposeAndOriginals(app core.App) error {
	return editMetadataFields(app, func(coll *core.Collection, field string, maxRunes int) bool {
		if coll.Fields.GetByName(field) != nil {
			return false
		}
		coll.Fields.Add(&core.TextField{Name: field, Max: maxRunes})
		return true
	})
}

func editMetadataFields(app core.App, edit func(coll *core.Collection, field string, maxRunes int) bool) error {
	changed := map[string]*core.Collection{}
	for _, f := range droppedMetadataFields {
		coll, ok := changed[f.collection]
		if !ok {
			var err error
			if coll, err = app.FindCollectionByNameOrId(f.collection); err != nil {
				return err
			}
		}
		if edit(coll, f.field, f.maxRunes) {
			changed[f.collection] = coll
		}
	}
	for _, coll := range changed {
		if err := app.Save(coll); err != nil {
			return err
		}
	}
	return nil
}
