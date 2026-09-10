package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/ocr"
)

// Overrides names the bindings that replace the configured ones for the length
// of one request or one job. An empty field keeps the configured client.
//
// One struct rather than a method per binding: five near-identical
// "OverrideChatter"/"OverrideExtractor" entry points would each have to repeat
// the credential handling and the nil-provider fallback, and a caller wanting
// two of them at once (a reprocess job overriding OCR and extraction) would
// build two snapshots and pick fields out of both.
//
// The json tags are load-bearing: this is also the stored shape of a
// processing job's `overrides` column, so a job carries the same document the
// API accepts and there is no second struct to keep in step with this one. A
// job never sets Chat or Search; the fields are inert there rather than
// forbidden, which costs nothing and keeps one type.
type Overrides struct {
	Chat      aiprovider.Binding `json:"chat,omitzero"`
	Search    aiprovider.Binding `json:"search,omitzero"`
	Extract   aiprovider.Binding `json:"extract,omitzero"`
	OCR       aiprovider.Binding `json:"ocr,omitzero"`
	Embedding aiprovider.Binding `json:"embedding,omitzero"`
}

// Empty reports overrides that change nothing, so a caller can skip the rebuild
// entirely and hand on the published snapshot.
//
// Compared against the zero value rather than asking each Binding.Empty().
// That reads the same but is not: Binding.Empty is keyed on the provider id, so
// a binding carrying a model and no provider is "empty" to it -- and this is
// the short-circuit before Validate, so such a binding would be dropped and the
// request would quietly run the configured model instead of the one it named.
// Anything at all set here has to reach Validate, which is where a half-filled
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

// Purposes pairs each binding with the capability it has to serve.
//
// It is the one list of the five fields, so a binding cannot be added to the
// struct and forgotten by the validator -- which would accept an override and
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

// validateEmbeddingModel refuses an embedding override naming a model other
// than the one the retrieval index was built for.
//
// Not a stylistic restriction, and the reason it is checked here rather than
// left to the embedder: a chunk row records the model and dimension count it
// was produced with, and the index only reads rows matching the configured spec
// -- embed.SpecFrom, and matchesSpec in internal/embed/source.go. Embedding a
// document on a different model therefore spends the provider call, writes the
// rows, reports success, and drops that document out of dense retrieval,
// because nothing will ever read them. There is no way to make that outcome
// visible afterwards, so it is refused up front.
//
// The configured model is allowed, and is the case worth having: re-running the
// embed step for a document whose vectors are missing or stale.
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

// WithOverrides returns the published snapshot with the named clients rebuilt
// on the bindings the caller supplied.
//
// It goes through the same builders apply does, which is the point: the
// overridden clients get the same ChatGPT token middleware, the same OpenCode
// session header and the same timeouts as the configured ones, and there is no
// second construction path to drift from the first.
//
// Rebuilding is cheap -- the ai constructors only assemble an openai-go client,
// with no network call -- so this runs per request on the chat surfaces. The one
// exception is a google_vision OCR client, which opens a gRPC connection; that
// binding is reached only from the worker, once per job.
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

	// Resolved again rather than carried out of Validate: the loop there is a
	// yes/no over five bindings, and threading five (provider, model) pairs out
	// of it would make the shape of that answer depend on this caller.
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
		// The splitter moves with the extractor because it is defined as the
		// extraction provider's second client; leaving it on the configured
		// binding would make Snapshot.Splitter's own doc comment false.
		snap.AI, snap.Splitter = buildExtractPair(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Chat.Empty() {
		p, model := resolve(o.Chat, aiprovider.PurposeLLM)
		snap.Chatter = buildChatter(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Search.Empty() {
		p, model := resolve(o.Search, aiprovider.PurposeLLM)
		// SearchHelper deliberately stays put. It is a separate binding because
		// it does many cheap per-document calls where the research model does a
		// few expensive ones, and moving the bulk work onto whatever model was
		// picked for the conversation is not what picking it asked for.
		snap.SearchAgent = buildSearchAgent(app, snap.Cfg, p, model, aiLogger)
	}
	if !o.Embedding.Empty() {
		p, model := resolve(o.Embedding, aiprovider.PurposeEmbedding)
		snap.Embedder = buildEmbedder(app, snap.Cfg, p, model, aiLogger)
	}
	return snap, nil
}

// buildOCR builds the OCR client for a provider row, or nil when there is none.
//
// The row is copied before its key is replaced: the caller's Provider may be
// the one hanging off Config, and a chatgpt row's placeholder key written back
// there would be published in the next snapshot as though it were real.
func buildOCR(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) (ocr.Provider, error) {
	if p == nil {
		return nil, nil
	}
	row := *p
	key, opts := providerCredential(app, p, logger)
	row.APIKey = key
	return ocr.NewFromAIProvider(row, model, cfg.OCRTimeout, logger, opts...)
}

// buildExtractPair builds the extractor and the splitter together.
//
// One credential for both, which is why they are one function: asking twice
// would put two middlewares over one ChatGPT token source, each refreshing
// against the other. The splitter is always the extraction provider's client,
// so there is no caller that wants one without the other.
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

// buildEmbedder builds the embedding client, or nil when the provider cannot
// embed.
//
// EmbeddingDims comes from the configuration rather than the binding: it is the
// width the chunk index was built for, and an embedder asked for a different
// one writes rows that index discards. An override here is only ever useful for
// re-embedding on the model already bound -- see the guard in worker.
func buildEmbedder(app core.App, cfg Config, p *aiprovider.Provider, model string, logger *slog.Logger) ai.Embedder {
	if p == nil || !p.Configured() || !aiprovider.CanEmbed(p.SDK) || model == "" {
		return nil
	}
	key, _ := providerCredential(app, p, logger)
	return ai.NewEmbedder(p.SDK, key, model, p.BaseURL, cfg.EmbeddingDims, cfg.OpenAITimeout, logger)
}
