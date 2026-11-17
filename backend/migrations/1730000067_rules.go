package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

// Adds rules, each user's if-then automation over their own documents and the
// admin's global ones over everybody's, and
// processing_jobs.return_status, the status a job that runs only the rules
// leaves the document in.
//
// Named functions so the test can run them twice: a managed instance re-runs
// every migration on every boot.
func init() {
	m.Register(addRules, dropRules)
}

func addRules(app core.App) error {
	if err := createRulesCollection(app); err != nil {
		return err
	}
	return addJobReturnStatus(app)
}

func createRulesCollection(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(rules.Collection); err == nil {
		return nil
	}
	coll := core.NewBaseCollection(rules.Collection)
	// A rule without a user is global: it runs on everyone's documents, so
	// only an admin sees or keeps one.
	access := "(user = @request.auth.id || (user = '' && @request.auth." + pairedAdminField + " = true))"
	coll.ListRule = new(access)
	coll.ViewRule = new(access)
	coll.CreateRule = new("@request.auth.id != '' && " + access)
	coll.UpdateRule = new(access + " && @request.body.user:changed = false")
	coll.DeleteRule = new(access)
	coll.Fields.Add(
		&core.RelationField{Name: "user", CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "name", Required: true, Max: 100},
		&core.BoolField{Name: "enabled"},
		&core.NumberField{Name: "position", OnlyInt: true},
		&core.SelectField{
			Name:      "triggers",
			Required:  true,
			MaxSelect: 2,
			Values:    []string{rules.TriggerProcessed, rules.TriggerEdited},
		},
		&core.JSONField{Name: "conditions", MaxSize: 64 << 10},
		&core.JSONField{Name: "actions", MaxSize: 64 << 10},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	coll.AddIndex("idx_rules_user", false, "`user`, position", "")
	return app.Save(coll)
}

func addJobReturnStatus(app core.App) error {
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		return err
	}
	if jobs.Fields.GetByName(models.JobReturnStatus) != nil {
		return nil
	}
	jobs.Fields.Add(&core.SelectField{
		Name:      models.JobReturnStatus,
		MaxSelect: 1,
		Values:    []string{models.DocStatusCompleted, models.DocStatusNeedsReview},
	})
	return app.Save(jobs)
}

func dropRules(app core.App) error {
	if jobs, err := app.FindCollectionByNameOrId("processing_jobs"); err == nil {
		jobs.Fields.RemoveByName(models.JobReturnStatus)
		if err := app.Save(jobs); err != nil {
			return err
		}
	}
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		return nil
	}
	return app.Delete(coll)
}
