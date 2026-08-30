package worker

import (
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func coreTestJobsCollection() *core.Collection {
	jobs := core.NewBaseCollection("processing_jobs")
	jobs.Fields.Add(&core.JSONField{Name: "metadata_json"})
	return jobs
}

func coreTestDocumentsCollection() *core.Collection {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "ocr_text", Max: models.MaxOCRTextRunes})
	return docs
}
