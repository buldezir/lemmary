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

	// EmbeddingModel is the retrieval embedding model on this same endpoint.
	// It rides on the provider rather than being a provider of its own because
	// there is no environment variable for a separate embedding endpoint: one
	// key, one address, a second model name. Pointing embeddings somewhere else
	// is a Settings-only choice. Empty means dense retrieval is off.
	EmbeddingModel string

	// HelperModel is the Deep Search helper on this same endpoint, the model
	// that distils and surveys documents in bulk. Empty means the search
	// model does that work itself.
	HelperModel string
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
		bindHelper(settings, llmID, b.LLM.HelperModel)
		bindEmbedding(settings, llmID, b.LLM.EmbeddingModel)
	}
	return nil
}

// bindHelper points the Deep Search helper binding at the language model's
// provider. An empty model clears the binding so the fallback to the search
// model takes over, the same way removing AI_EMBEDDING_MODEL turns dense
// retrieval off rather than leaving a stale binding standing.
func bindHelper(settings *core.Record, providerID, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		settings.Set("search_helper_provider_id", "")
		settings.Set("search_helper_model", "")
		return
	}
	settings.Set("search_helper_provider_id", providerID)
	settings.Set("search_helper_model", model)
}

// bindEmbedding points the retrieval embedding binding at the language model's
// provider.
//
// Two behaviours are deliberate. An empty model clears the binding rather than
// leaving the previous one standing, so removing AI_EMBEDDING_MODEL from a
// managed instance actually turns dense retrieval off. And embedding_dims is
// reset only when the binding actually changed: it is a fact learned from the
// provider's first response, and rewriting it on every boot would make the
// whole archive look stale once a minute.
func bindEmbedding(settings *core.Record, providerID, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		settings.Set("embedding_provider_id", "")
		settings.Set("embedding_model", "")
		settings.Set("embedding_dims", 0)
		return
	}
	changed := strings.TrimSpace(settings.GetString("embedding_provider_id")) != providerID ||
		strings.TrimSpace(settings.GetString("embedding_model")) != model
	settings.Set("embedding_provider_id", providerID)
	settings.Set("embedding_model", model)
	if changed {
		settings.Set("embedding_dims", 0)
	}
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
