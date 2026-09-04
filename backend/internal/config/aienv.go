package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"lemmary/backend/internal/aiprovider"
)

// AIEnv is the AI configuration read from the environment, once, before the app exists.
//
// Self-hosted (Managed false, the default): values seed the settings singleton
// on first boot and are inert afterwards; the Settings page is then the authority.
//
// Managed (Managed true): the operator owns the AI bill, so these are re-applied
// on every boot and the tenant cannot edit providers, model bindings, or duplicate
// detection. Timeouts, retries, and language settings stay tenant-owned.
type AIEnv struct {
	Managed   bool
	Providers aiprovider.Bootstrap

	// Operator-owned in managed mode.
	NearDuplicateEnabled   bool
	NearDuplicateThreshold float64

	// Seed-only in both modes; managed mode does not reset these on restart.
	OCRTimeout          time.Duration
	AITimeout           time.Duration
	WorkerTimeout       time.Duration
	WorkerMaxRetries    int
	DeepSearchLanguages string
	ExtractionPromptVer string
}

// Environment variable names, in one place so the error messages and the
// parsing cannot drift apart.
const (
	EnvManaged = "AI_MANAGED"

	EnvAISDK     = "AI_SDK"
	EnvAIAPIKey  = "AI_API_KEY"
	EnvAIBaseURL = "AI_BASE_URL"
	EnvAIModel   = "AI_MODEL"

	// EnvAIEmbeddingModel names the retrieval embedding model -- on the AI_SDK
	// provider by default, or on the AI_EMBEDDING_SDK one when that is set.
	EnvAIEmbeddingModel = "AI_EMBEDDING_MODEL"

	// The embedding provider block. An earlier release had none, on the
	// reasoning that a separate embedding endpoint was a rare Settings-only
	// choice and three more variables would be three more ways to
	// half-configure the feature. Running the embedding model yourself is the
	// case that reasoning did not anticipate: a sidecar on the compose network
	// is by definition a different endpoint from the language model, so without
	// these an operator could not bring an instance up on it from .env at all,
	// and a managed instance could not use one.
	//
	// Unset (the default) still means embeddings ride on the AI_SDK provider,
	// which is exactly what they did before.
	EnvAIEmbeddingSDK     = "AI_EMBEDDING_SDK"
	EnvAIEmbeddingAPIKey  = "AI_EMBEDDING_API_KEY"
	EnvAIEmbeddingBaseURL = "AI_EMBEDDING_BASE_URL"

	// EnvAISearchHelperModel names the model on the AI_SDK provider that Deep
	// Search hands bulk per-document work to. Same reasoning as the embedding
	// model: one provider, a second model name; a separate endpoint is a
	// Settings choice. Empty falls back to the search model.
	EnvAISearchHelperModel = "AI_SEARCH_HELPER_MODEL"

	EnvOCRSDK     = "OCR_SDK"
	EnvOCRAPIKey  = "OCR_API_KEY"
	EnvOCRBaseURL = "OCR_BASE_URL"
	EnvOCRModel   = "OCR_MODEL"
)

// AIEnvFromEnv parses the AI environment.
//
// Incomplete values error only in managed mode: the tenant cannot repair a
// missing key from Settings. Off it, absence means the setup wizard will ask.
func AIEnvFromEnv() (AIEnv, error) {
	managed, err := strictBool(EnvManaged)
	if err != nil {
		return AIEnv{}, err
	}

	env := AIEnv{
		Managed:                managed,
		NearDuplicateEnabled:   getEnvBool("NEAR_DUPLICATE_DETECTION_ENABLED", false),
		NearDuplicateThreshold: getEnvFloat("NEAR_DUPLICATE_THRESHOLD", DefaultNearDuplicateThreshold),
		OCRTimeout:             time.Duration(envIntDefault("OCR_TIMEOUT_SEC", 40, 1)) * time.Second,
		AITimeout:              time.Duration(envIntDefault("AI_TIMEOUT_SEC", 60, 1)) * time.Second,
		WorkerTimeout:          time.Duration(envIntDefault("WORKER_TIMEOUT_SEC", 300, 1)) * time.Second,
		WorkerMaxRetries:       envIntDefault("WORKER_MAX_RETRIES", 0, 0),
		DeepSearchLanguages:    NormalizeLanguageList(os.Getenv("DEEP_SEARCH_LANGUAGES")),
		ExtractionPromptVer:    getEnv("EXTRACTION_PROMPT_VERSION", "v1"),
	}

	llm, err := parseLLM()
	if err != nil {
		return AIEnv{}, err
	}
	ocr, err := parseOCR(llm)
	if err != nil {
		return AIEnv{}, err
	}
	embedding, err := parseEmbedding(llm)
	if err != nil {
		return AIEnv{}, err
	}
	env.Providers = aiprovider.Bootstrap{LLM: llm, OCR: ocr, Embedding: embedding}

	if env.Managed {
		if err := env.validateManaged(); err != nil {
			return AIEnv{}, err
		}
	}
	return env, nil
}

// strictBool refuses a value it cannot read, rather than falling back to off.
// AI_MANAGED is the billing lock: a typo read as "off" would leave Settings
// editable and the environment unapplied. Same spellings as the VAULT_* flags.
func strictBool(key string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf(
			"%s=%q is not a boolean; use 1/true/yes/on or 0/false/no/off, or leave it unset",
			key, os.Getenv(key))
	}
}

func parseLLM() (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(getEnv(EnvAISDK, aiprovider.SDKOpenAI))
	if !aiprovider.IsLLM(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q is not a language-model SDK (want one of %s, %s, %s)",
			EnvAISDK, sdk, aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter, aiprovider.SDKMistral)
	}

	spec := aiprovider.ProviderSpec{
		SDK:            sdk,
		APIKey:         strings.TrimSpace(os.Getenv(EnvAIAPIKey)),
		BaseURL:        aiprovider.NormalizeBaseURL(sdk, os.Getenv(EnvAIBaseURL)),
		Model:          strings.TrimSpace(getEnv(EnvAIModel, aiprovider.DefaultExtractModel)),
		EmbeddingModel: strings.TrimSpace(os.Getenv(EnvAIEmbeddingModel)),
		HelperModel:    strings.TrimSpace(os.Getenv(EnvAISearchHelperModel)),
	}
	return spec, nil
}

// parseOCR reads the optional second provider. Unset means OCR runs on the language model.
func parseOCR(llm aiprovider.ProviderSpec) (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(os.Getenv(EnvOCRSDK))
	key := strings.TrimSpace(os.Getenv(EnvOCRAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvOCRBaseURL))
	model := strings.TrimSpace(os.Getenv(EnvOCRModel))

	if sdk == "" {
		if key != "" || baseURL != "" || model != "" {
			// A key or model without an SDK is a half-written intention; folding
			// it into the LLM provider would point OCR somewhere not asked for.
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s, %s or %s is set without %s; name the OCR provider's SDK, or leave them all unset to run OCR on the %s provider",
				EnvOCRAPIKey, EnvOCRBaseURL, EnvOCRModel, EnvOCRSDK, EnvAISDK)
		}
		return aiprovider.ProviderSpec{}, nil
	}
	if !aiprovider.ValidSDK(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q is not a known SDK (want one of %s)",
			EnvOCRSDK, sdk, strings.Join(aiprovider.ValidSDKs, ", "))
	}
	if !aiprovider.CanOCR(sdk) {
		// Valid as an SDK, just not for this job. Caught here rather than on the
		// first uploaded document.
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q cannot read a document; it serves embeddings only",
			EnvOCRSDK, sdk)
	}

	// The same SDK is the same endpoint: reuse the language model's credential
	// and address rather than making an operator write them out twice.
	if sdk == llm.SDK {
		if key == "" {
			key = llm.APIKey
		}
		if baseURL == "" {
			baseURL = llm.BaseURL
		}
	} else if key == "" && aiprovider.RequiresAPIKey(sdk) {
		// Rejected in both modes here: off managed, the environment seeds once
		// and OCR would silently bind to the language model.
		//
		// A local sidecar is exempt because it has no key to give -- it is
		// reached by URL alone, and NormalizeBaseURL below supplies the compose
		// default when OCR_BASE_URL was left empty.
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q needs %s; it is a different endpoint from %s=%q and cannot borrow its key",
			EnvOCRSDK, sdk, EnvOCRAPIKey, EnvAISDK, llm.SDK)
	}
	if aiprovider.RequiresOCRModel(sdk) && model == "" {
		if sdk == llm.SDK {
			model = llm.Model
		} else {
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s=%q needs %s; only %s read a document without one",
				EnvOCRSDK, sdk, EnvOCRModel, strings.Join(aiprovider.ModellessOCRSDKs(), ", "))
		}
	}

	return aiprovider.ProviderSpec{
		SDK:     sdk,
		APIKey:  key,
		BaseURL: aiprovider.NormalizeBaseURL(sdk, baseURL),
		Model:   model,
	}, nil
}

// parseEmbedding reads the optional third provider. Unset means embeddings run
// on the language model, which is what they always did.
//
// It mirrors parseOCR, the existing precedent for "a second provider for one
// job", with one rule of its own: naming an SDK without a model would create a
// provider row with nothing bound to it, which reads as a configured feature
// that never embeds anything.
func parseEmbedding(llm aiprovider.ProviderSpec) (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(os.Getenv(EnvAIEmbeddingSDK))
	key := strings.TrimSpace(os.Getenv(EnvAIEmbeddingAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvAIEmbeddingBaseURL))
	model := strings.TrimSpace(os.Getenv(EnvAIEmbeddingModel))

	if sdk == "" {
		if key != "" || baseURL != "" {
			// Same half-written intention parseOCR refuses: folding these into
			// the language model would point embeddings somewhere not asked
			// for. AI_EMBEDDING_MODEL alone is not in this list -- on its own it
			// is the ordinary "embed on the AI_SDK provider" configuration.
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s or %s is set without %s; name the embedding provider's SDK, or leave them both unset to embed on the %s provider",
				EnvAIEmbeddingAPIKey, EnvAIEmbeddingBaseURL, EnvAIEmbeddingSDK, EnvAISDK)
		}
		return aiprovider.ProviderSpec{}, nil
	}
	if !aiprovider.CanEmbed(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q cannot serve embeddings (want one of %s)",
			EnvAIEmbeddingSDK, sdk, strings.Join(aiprovider.EmbeddingSDKs(), ", "))
	}
	if model == "" {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q needs %s; without it the provider is created and nothing binds to it",
			EnvAIEmbeddingSDK, sdk, EnvAIEmbeddingModel)
	}

	// The same SDK is the same endpoint: reuse the language model's credential
	// and address rather than making an operator write them out twice.
	if sdk == llm.SDK {
		if key == "" {
			key = llm.APIKey
		}
		if baseURL == "" {
			baseURL = llm.BaseURL
		}
	} else if key == "" && aiprovider.RequiresAPIKey(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q needs %s; it is a different endpoint from %s=%q and cannot borrow its key",
			EnvAIEmbeddingSDK, sdk, EnvAIEmbeddingAPIKey, EnvAISDK, llm.SDK)
	}

	return aiprovider.ProviderSpec{
		SDK:     sdk,
		APIKey:  key,
		BaseURL: aiprovider.NormalizeBaseURL(sdk, baseURL),
		Model:   model,
	}, nil
}

// validateManaged refuses the configurations a managed instance cannot serve
// and cannot be repaired out of.
func (e AIEnv) validateManaged() error {
	if !e.Providers.LLM.Configured() {
		return fmt.Errorf("%s=1 requires %s; a managed instance has no setup wizard to supply one",
			EnvManaged, EnvAIAPIKey)
	}
	if e.Providers.LLM.Model == "" {
		return fmt.Errorf("%s=1 requires %s", EnvManaged, EnvAIModel)
	}
	// parseOCR already refuses a named OCR provider with no key. The keyless
	// SDKs it lets through instead need an address, which NormalizeBaseURL
	// always supplies from DefaultBaseURL -- so this can only fire if that
	// default is ever removed, and it fires here rather than at the first
	// upload because a managed instance has no Settings page to fix it in.
	ocr := e.Providers.OCR
	if ocr.Requested() && aiprovider.RequiresBaseURL(ocr.SDK) && strings.TrimSpace(ocr.BaseURL) == "" {
		return fmt.Errorf("%s=1 with %s=%q requires %s; a local OCR engine is reached by address alone",
			EnvManaged, EnvOCRSDK, ocr.SDK, EnvOCRBaseURL)
	}
	return nil
}

// Defaults is the Config an install starts from, and the fallback when the
// settings record cannot be read — so it must never return something unusable.
func (e AIEnv) Defaults() Config {
	return Config{
		OCRModel:                      e.Providers.OCRModel(),
		ExtractModel:                  e.Providers.LLM.Model,
		ChatModel:                     e.Providers.LLM.Model,
		SearchModel:                   e.Providers.LLM.Model,
		SearchHelperModel:             e.Providers.LLM.HelperModel,
		EmbeddingModel:                e.Providers.LLM.EmbeddingModel,
		OCRTimeout:                    e.OCRTimeout,
		DeepSearchLanguages:           e.DeepSearchLanguages,
		OpenAITimeout:                 e.AITimeout,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 e.WorkerTimeout,
		WorkerMaxRetries:              e.WorkerMaxRetries,
		ExtractionPromptVer:           e.ExtractionPromptVer,
		NearDuplicateDetectionEnabled: e.NearDuplicateEnabled,
		NearDuplicateThreshold:        e.NearDuplicateThreshold,
	}
}
