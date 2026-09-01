package aiprovider

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// DefaultExtractModel is the fallback when nothing names a model. One copy so
// call sites cannot disagree.
const DefaultExtractModel = "gpt-5.6-luna"

// An empty APIKey means the environment did not ask for this provider, which
// is not an error outside managed mode — the setup wizard will ask.
type ProviderSpec struct {
	SDK     string
	APIKey  string
	BaseURL string
	Model   string
}

func (s ProviderSpec) Configured() bool { return strings.TrimSpace(s.APIKey) != "" }

// Requested is whether an SDK was named, distinct from Configured (has a key).
// Conflating them is how OCR silently ends up on the language model.
func (s ProviderSpec) Requested() bool { return strings.TrimSpace(s.SDK) != "" }

// Bootstrap is one language model and an optional separate OCR provider.
// The LLM serves extraction, chat and Deep Search; OCR defaults to the LLM.
type Bootstrap struct {
	LLM ProviderSpec
	OCR ProviderSpec
}

func (b Bootstrap) Configured() bool { return b.LLM.Configured() || b.OCR.Configured() }

// SharesOneProvider is true when OCR uses the LLM's endpoint (the default).
// Keys on whether an OCR SDK was named, never on whether it has a key.
func (b Bootstrap) SharesOneProvider() bool {
	return !b.OCR.Requested() || b.OCR.SDK == b.LLM.SDK
}

// OCRModel is the OCR provider's model, or the LLM's when OCR was not named.
// Empty for Google Vision, which reads a document without a model.
func (b Bootstrap) OCRModel() string {
	if b.OCR.Requested() {
		return b.OCR.Model
	}
	return b.LLM.Model
}

func (b Bootstrap) OCRSDK() string {
	if b.OCR.Requested() {
		return b.OCR.SDK
	}
	return b.LLM.SDK
}

// Apply writes the bootstrap onto providers and settings, and is safe to call
// repeatedly: managed mode does this every boot, matching by default alias so
// restarts do not accumulate duplicate rows. The caller saves settings; this
// only stages the bindings.
func Apply(app core.App, settings *core.Record, b Bootstrap) error {
	if settings == nil || !b.Configured() {
		return nil
	}

	llmID := ""
	if b.LLM.Configured() {
		id, err := upsertProvider(app, b.LLM)
		if err != nil {
			return err
		}
		llmID = id
	}

	// Separate row only when OCR is a different endpoint; otherwise Settings
	// would show a duplicate credential with no explanation.
	ocrID := llmID
	if b.OCR.Configured() && !b.SharesOneProvider() {
		id, err := upsertProvider(app, b.OCR)
		if err != nil {
			return err
		}
		ocrID = id
	}

	if ocrID != "" {
		settings.Set("ocr_provider_id", ocrID)
		// Google Vision takes no model, and storing one would fail the
		// Settings page's own validation the next time an admin saved it.
		if RequiresOCRModel(b.OCRSDK()) {
			settings.Set("ocr_model", b.OCRModel())
		} else {
			settings.Set("ocr_model", "")
		}
	}
	if llmID != "" {
		model := strings.TrimSpace(b.LLM.Model)
		if model == "" {
			model = DefaultExtractModel
		}
		bindLLM(settings, llmID, model, model, model)
	}
	return nil
}

// upsertProvider creates or updates the provider carrying spec.SDK's default alias.
// Matching by alias rather than SDK: an admin who renamed a provider owns it;
// a boot that reclaimed the rename would undo a UI edit.
func upsertProvider(app core.App, spec ProviderSpec) (string, error) {
	alias := DefaultAlias(spec.SDK)
	record, err := FindByAlias(app, alias)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("look up provider %s: %w", alias, err)
	}
	if record == nil {
		collection, err := EnsureCollection(app)
		if err != nil {
			return "", err
		}
		record = core.NewRecord(collection)
		record.Set("alias", uniqueAlias(app, alias))
	}

	record.Set("sdk", spec.SDK)
	record.Set("base_url", NormalizeBaseURL(spec.SDK, spec.BaseURL))
	record.Set("api_key", strings.TrimSpace(spec.APIKey))
	if err := app.Save(record); err != nil {
		return "", fmt.Errorf("save provider %s: %w", alias, err)
	}
	return record.Id, nil
}
