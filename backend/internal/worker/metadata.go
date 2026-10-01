package worker

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// customFieldWrites fills only what is empty, so a value someone typed survives
// every reprocess, unless overwrite asks for every answer to replace what is
// there. A field extraction found nothing for keeps its value either way.
func customFieldWrites(app core.App, document *core.Record, answers map[string]any, overwrite bool) ([]models.FieldWrite, error) {
	if len(answers) == 0 {
		return nil, nil
	}
	fields, err := models.LoadCustomFields(app)
	if err != nil {
		return nil, err
	}
	values, err := models.LoadDocumentFieldValues(app, document.Id)
	if err != nil {
		return nil, err
	}
	var writes []models.FieldWrite
	for _, field := range fields {
		if _, filled := values[field.ID]; filled && !overwrite {
			continue
		}
		if value, ok := field.ValueIn(answers); ok {
			writes = append(writes, models.FieldWrite{Field: field, Value: value})
		}
	}
	return writes, nil
}

// correspondentName falls back to the first person or organization named, for
// a model that left correspondent empty.
func correspondentName(metadata *models.ExtractedMetadata) string {
	if name := strings.TrimSpace(metadata.Correspondent); name != "" {
		return name
	}
	for _, raw := range metadata.PeopleOrOrganizations {
		if name := strings.TrimSpace(raw); name != "" {
			return name
		}
	}
	return ""
}

// optionWrite points an option field at the owner's option for name, creating
// it when the owner has none. An empty name clears the field.
func optionWrite(app core.App, document *core.Record, field models.CustomField, name string) (models.FieldWrite, error) {
	if name == "" {
		return models.FieldWrite{Field: field}, nil
	}
	id, _, err := EnsureOption(app, field.ID, document.GetString("user"), name)
	if err != nil {
		return models.FieldWrite{}, err
	}
	return models.FieldWrite{Field: field, Value: id}, nil
}

func loadMetadataJSON(job *core.Record) (*models.ExtractedMetadata, error) {
	raw := job.Get("metadata_json")
	if raw == nil {
		return nil, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata_json: %w", err)
	}

	var metadata models.ExtractedMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal metadata_json: %w", err)
	}
	if !metadata.Populated() {
		return nil, nil
	}
	return &metadata, nil
}

func saveMetadataJSON(job *core.Record, metadata *models.ExtractedMetadata) {
	if metadata == nil {
		job.Set("metadata_json", nil)
		return
	}
	job.Set("metadata_json", metadata)
}
