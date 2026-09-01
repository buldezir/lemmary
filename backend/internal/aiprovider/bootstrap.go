package aiprovider

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// DefaultExtractModel is the model an install falls back to when nothing names
// one. It is the only copy: it used to be written out at three call sites, and
// the three had already begun to disagree about which of them was the default.
const DefaultExtractModel = "gpt-5.6-luna"

// ProviderSpec is one endpoint the environment asks for: which SDK speaks to
// it, the credential, where it lives, and the model to use on it.
//
// An empty APIKey means "the environment did not ask for this provider", which
// is not an error outside managed mode — it is an install that intends to be
// configured from the setup wizard.
type ProviderSpec struct {
	SDK     string
	APIKey  string
	BaseURL string
	Model   string
}

func (s ProviderSpec) Configured() bool { return strings.TrimSpace(s.APIKey) != "" }

// Bootstrap is the whole of what the environment can say about AI: one language
// model, and optionally a separate provider for OCR.
//
// Two providers rather than the previous three SDK-specific families, because
// three families forced every reader — including a second repository's console
// hint — to work out which of nine keys would win. Here the shape answers it:
// the LLM serves extraction, chat and Deep Search, and OCR runs on whichever
// provider OCR names, defaulting to the LLM's.
type Bootstrap struct {
	LLM ProviderSpec
	OCR ProviderSpec
}

// Configured reports whether the environment asked for anything at all.
func (b Bootstrap) Configured() bool { return b.LLM.Configured() || b.OCR.Configured() }

// SharesOneProvider is true when OCR runs on the same endpoint as the language
// model, which is both the default and the common case: an OpenAI-compatible
// key configures the entire install.
func (b Bootstrap) SharesOneProvider() bool {
	return !b.OCR.Configured() || b.OCR.SDK == b.LLM.SDK
}

// OCRModel is the model OCR actually runs with once the default has been
// resolved: the OCR provider's when one was named, and the language model's
// otherwise. Empty for Google Vision, which reads a document without one.
func (b Bootstrap) OCRModel() string {
	if b.OCR.Configured() {
		return b.OCR.Model
	}
	return b.LLM.Model
}

// OCRSDK is the SDK serving OCR once the default has been resolved.
func (b Bootstrap) OCRSDK() string {
	if b.OCR.Configured() {
		return b.OCR.SDK
	}
	return b.LLM.SDK
}

// Apply writes the bootstrap onto the provider collection and the settings
// record, and is safe to call repeatedly.
//
// Repeatable is the point. Managed mode calls this on every boot, so a provider
// is matched by its default alias and updated in place rather than added
// beside itself; a fleet restarted a hundred times must not accumulate a
// hundred "OpenAI 2" rows. The caller saves the settings record — this only
// stages the bindings on it, so that seeding a brand-new singleton stays one
// write.
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

	// The same endpoint under a second name would be a second row carrying the
	// same credential, and the Settings page would show the duplicate without
	// being able to explain it. So a separate row is created only for an OCR
	// provider that really is a different endpoint.
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

// upsertProvider creates the provider for spec.SDK, or updates the one already
// carrying that SDK's default alias.
//
// Matching by alias rather than by SDK is deliberate and matches what the
// retired env-apply path did: an admin who renamed a provider owns it, and a
// boot that silently reclaimed the rename would undo an edit made in the UI.
// A renamed provider therefore gets a fresh row rather than a hijacked one.
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
