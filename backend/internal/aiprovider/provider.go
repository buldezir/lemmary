package aiprovider

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type Provider struct {
	ID      string
	SDK     string
	Alias   string
	BaseURL string
	APIKey  string
}

func FromRecord(record *core.Record) Provider {
	sdk := strings.TrimSpace(record.GetString("sdk"))
	return Provider{
		ID:      record.Id,
		SDK:     sdk,
		Alias:   strings.TrimSpace(record.GetString("alias")),
		BaseURL: NormalizeBaseURL(sdk, record.GetString("base_url")),
		APIKey:  strings.TrimSpace(record.GetString("api_key")),
	}
}

func FindByID(app core.App, id string) (*Provider, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	record, err := app.FindRecordById(CollectionName, id)
	if err != nil {
		return nil, err
	}
	p := FromRecord(record)
	return &p, nil
}

func List(app core.App) ([]Provider, error) {
	records, err := app.FindAllRecords(CollectionName)
	if err != nil {
		return nil, err
	}
	out := make([]Provider, 0, len(records))
	for _, record := range records {
		out = append(out, FromRecord(record))
	}
	return out, nil
}

func FindByAlias(app core.App, alias string) (*core.Record, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, nil
	}
	record, err := app.FindFirstRecordByFilter(CollectionName, "alias = {:alias}", dbx.Params{"alias": alias})
	if err != nil {
		return nil, err
	}
	return record, nil
}

func EnsureUniqueAlias(app core.App, alias, excludeID string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("alias is required")
	}
	record, err := FindByAlias(app, alias)
	if err != nil {
		// FindFirstRecordByFilter returns an error when no record matches.
		return nil
	}
	if record != nil && record.Id != excludeID {
		return fmt.Errorf("alias %q is already in use", alias)
	}
	return nil
}

func ReferencedBySettings(settings *core.Record, providerID string) bool {
	if settings == nil || providerID == "" {
		return false
	}
	for _, field := range []string{"ocr_provider_id", "extract_provider_id", "chat_provider_id", "search_provider_id"} {
		if strings.TrimSpace(settings.GetString(field)) == providerID {
			return true
		}
	}
	return false
}
