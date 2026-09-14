package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/websearch"
)

// Overrides names the bindings that replace the configured ones for the length
// of one request or one job. An empty field keeps the configured client.
//
// The json tags are load-bearing: this is also the stored shape of a processing
// job's `overrides` column, so a job carries the same document the API accepts.
// A job never sets Chat or Search; the fields are inert there rather than
// forbidden, which keeps one type.
type Overrides struct {
	Chat      aiprovider.Binding `json:"chat,omitzero"`
	Search    aiprovider.Binding `json:"search,omitzero"`
	Extract   aiprovider.Binding `json:"extract,omitzero"`
	OCR       aiprovider.Binding `json:"ocr,omitzero"`
	Embedding aiprovider.Binding `json:"embedding,omitzero"`
}

// Empty is compared against the zero value rather than asking each
// Binding.Empty(), which is keyed on the provider id: a binding carrying a model
// and no provider is empty to it, and since this short-circuits before Validate
// such a binding would be dropped and the request would quietly run the
// configured model. Anything set here has to reach Validate, where a half-filled
// binding is refused.
func (o Overrides) Empty() bool {
	return o == Overrides{}
}

// BoundOverride is one field of Overrides with the capability it has to serve,
// and the name it is called by in an error message.
type BoundOverride struct {
	Name    string
	Binding aiprovider.Binding
	Purpose aiprovider.ModelPurpose
}

// Purposes is the one list of the five fields, so a binding cannot be added to
// the struct and forgotten by the validator, which would accept an override and
// then silently run the configured model. Fixed order, so the same bad request
// always names the same field first.
func (o Overrides) Purposes() []BoundOverride {
	return []BoundOverride{
		{"ocr", o.OCR, aiprovider.PurposeOCR},
		{"extract", o.Extract, aiprovider.PurposeLLM},
		{"chat", o.Chat, aiprovider.PurposeLLM},
		{"search", o.Search, aiprovider.PurposeLLM},
		{"embedding", o.Embedding, aiprovider.PurposeEmbedding},
	}
}

// Validate resolves every non-empty binding without building anything, so a
// request or a record write can be refused before any work is queued.
func (o Overrides) Validate(app core.App, cfg Config) error {
	for _, item := range o.Purposes() {
		if _, _, err := aiprovider.Resolve(app, item.Binding, item.Purpose); err != nil {
			return fmt.Errorf("%s override: %w", item.Name, err)
		}
	}
	return o.validateEmbeddingModel(cfg)
}

// validateEmbeddingModel refuses an embedding override naming a model other than
// the one the retrieval index was built for. A chunk row records the model and
// dimensions it was produced with, and the index only reads rows matching the
// configured spec (embed.SpecFrom, matchesSpec in internal/embed/source.go), so
// embedding on another model spends the call, writes rows, reports success and
// drops the document out of dense retrieval with nothing to show for it. The
// configured model is allowed: re-running the embed step for missing or stale
// vectors is the case worth having.
func (o Overrides) validateEmbeddingModel(cfg Config) error {
	binding := o.Embedding.Normalized()
	if binding.Empty() {
		return nil
	}
	configured := strings.TrimSpace(cfg.EmbeddingModel)
	if binding.Model == configured {
		return nil
	}
	if configured == "" {
		return fmt.Errorf("embedding override: no embedding model is configured, so there is no index for these vectors to join; bind one in Settings first")
	}
	return fmt.Errorf("embedding override: the retrieval index holds %q vectors, so vectors from %q would be written and never read; re-embed on %q, or change the model in Settings and reindex",
		configured, binding.Model, configured)
}

// WithOverrides returns the published snapshot with the named clients rebuilt on
// the bindings the caller supplied. It goes through the same builders apply
// does, so overridden clients get the same token middleware, session header and
// timeouts, with no second construction path to drift.
//
// Rebuilding is cheap (no network call), so this runs per request on the chat
// surfaces. The exception is a google_vision OCR client, which opens a gRPC
// connection; that binding is reached only from the worker, once per job.
func (r *Runtime) WithOverrides(app core.App, o Overrides) (Snapshot, error) {
	snap := r.Snapshot()
	if o.Empty() {
		return snap, nil
	}
	if err := o.Validate(app, snap.Cfg); err != nil {
		return snap, err
	}

	logger := app.Logger()
	aiLogger := logger.With("component", "ai")

	// Resolved again rather than carried out of Validate, whose loop is a yes/no
	// over five bindings; threading five pairs out of it would shape that answer
	// around this caller.
	resolve := func(b aiprovider.Binding, purpose aiprovider.ModelPurpose) (*aiprovider.Provider, string) {
		p, model, _ := aiprovider.Resolve(app, b, purpose)
		return p, model
	}

	if !o.OCR.Empty() {
		p, model := resolve(o.OCR, aiprovider.PurposeOCR)
		built, err := buildOCR(app, snap.Cfg, p, model, logger.With("component", "ocr"))
		if err != nil {
			return snap, fmt.Errorf("ocr override: %w", err)
		}
		snap.OCR = built
	}
	if !o.Extract.Empty() {
		p, model := resolve(o.Extract, aiprovider.PurposeLLM)
		// The splitter moves with the extractor because it is defined as the extraction
		// provider's second client; leaving it behind would make Snapshot.Splitter's own
		// doc comment false.
		snap.AI, snap.Splitter = buildExtractPair(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Chat.Empty() {
		p, model := resolve(o.Chat, aiprovider.PurposeLLM)
		snap.Chatter = buildChatter(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Search.Empty() {
		p, model := resolve(o.Search, aiprovider.PurposeLLM)
		// SearchHelper stays put. It is a separate binding because it does many cheap
		// per-document calls where the research model does a few expensive ones, and
		// moving bulk work onto the conversation model is not what picking it asked for.
		snap.SearchAgent = buildSearchAgent(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Embedding.Empty() {
		p, model := resolve(o.Embedding, aiprovider.PurposeEmbedding)
		snap.Embedder = buildEmbedder(app, snap.Cfg, p, model, aiLogger)
	}
	return snap, nil
}

// buildOCR copies the row before replacing its key: the caller's Provider may be
// the one hanging off Config, and a chatgpt placeholder key written back there
// would be published in the next snapshot as though it were real.
func buildOCR(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) (ocr.Provider, error) {
	if p == nil {
		return nil, nil
	}
	row := *p
	key, opts := providerCredential(app, p, logger)
	row.APIKey = key
	return ocr.NewFromAIProvider(row, model, cfg.OCRTimeout, logger, opts...)
}

// buildExtractPair builds both from one credential: asking twice would put two
// middlewares over one ChatGPT token source, each refreshing against the other.
// The splitter is always the extraction provider's client.
func buildExtractPair(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) (ai.Extractor, ai.Splitter) {
	if !usableLLM(p) {
		return nil, nil
	}
	key, opts := providerCredential(app, p, logger)
	extractor := ai.NewExtractor(
		p.SDK,
		key,
		model,
		p.BaseURL,
		cfg.ExtractionPromptVer,
		cfg.ProcessingResultLanguage,
		cfg.ExtractionRules,
		cfg.OpenAITimeout,
		logger,
		opts...,
	)
	splitter := ai.NewSplitter(
		p.SDK,
		key,
		model,
		p.BaseURL,
		cfg.OpenAITimeout,
		logger,
		opts...,
	)
	return extractor, splitter
}

func buildChatter(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) ai.Chatter {
	if !usableLLM(p) {
		return nil
	}
	key, opts := providerCredential(app, p, logger)
	return ai.NewChatter(p.SDK, key, model, p.BaseURL, cfg.OpenAITimeout, logger, opts...)
}

func buildSearchAgent(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) ai.SearchAgent {
	if !usableLLM(p) {
		return nil
	}
	key, opts := providerCredential(app, p, logger)
	return ai.NewSearchAgent(
		p.SDK,
		key,
		model,
		p.BaseURL,
		cfg.OpenAITimeout,
		cfg.DeepSearchLanguages,
		cfg.ProcessingResultLanguage,
		logger,
		opts...,
	)
}

func buildHelper(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) ai.Helper {
	if !usableLLM(p) {
		return nil
	}
	key, opts := providerCredential(app, p, logger)
	return ai.NewHelper(p.SDK, key, model, p.BaseURL, cfg.OpenAITimeout, logger, opts...)
}

// buildWebSearch has no model term and no override: a web-search API takes no
// model, and the tools are turned on per turn rather than bound per request.
func buildWebSearch(app core.App, cfg Config, p *aiprovider.Provider, logger *slog.Logger) *websearch.Tavily {
	if p == nil || !p.Configured() || !aiprovider.CanWebSearch(p.SDK) {
		return nil
	}
	key, _ := providerCredential(app, p, logger)
	return websearch.NewTavily(key, p.BaseURL, cfg.OpenAITimeout, logger)
}

// buildEmbedder takes EmbeddingDims from the configuration rather than the
// binding: it is the width the chunk index was built for, and an embedder asked
// for a different one writes rows that index discards. An override here is only
// useful for re-embedding on the model already bound; see the guard in worker.
func buildEmbedder(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) ai.Embedder {
	if p == nil || !p.Configured() || !aiprovider.CanEmbed(p.SDK) || model == "" {
		return nil
	}
	key, _ := providerCredential(app, p, logger)
	return ai.NewEmbedder(p.SDK, key, model, p.BaseURL, cfg.EmbeddingDims, cfg.OpenAITimeout, logger)
}
