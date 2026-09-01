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
	SearchContextTokens    int
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

	// EnvAIEmbeddingModel names the retrieval embedding model on the AI_SDK
	// provider. There is deliberately no AI_EMBEDDING_SDK / _API_KEY / _BASE_URL
	// trio: a separate embedding endpoint is a rare enough choice that it
	// belongs in Settings, and three more variables would mostly be three more
	// ways to half-configure the feature.
	EnvAIEmbeddingModel = "AI_EMBEDDING_MODEL"

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
		SearchContextTokens:    envIntDefault("SEARCH_CONTEXT_TOKENS", DefaultSearchContextTokens, 1),
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
	env.Providers = aiprovider.Bootstrap{LLM: llm, OCR: ocr}

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

	// The same SDK is the same endpoint: reuse the language model's credential
	// and address rather than making an operator write them out twice.
	if sdk == llm.SDK {
		if key == "" {
			key = llm.APIKey
		}
		if baseURL == "" {
			baseURL = llm.BaseURL
		}
	} else if key == "" {
		// Rejected in both modes here: off managed, the environment seeds once
		// and OCR would silently bind to the language model.
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q needs %s; it is a different endpoint from %s=%q and cannot borrow its key",
			EnvOCRSDK, sdk, EnvOCRAPIKey, EnvAISDK, llm.SDK)
	}
	if aiprovider.RequiresOCRModel(sdk) && model == "" {
		if sdk == llm.SDK {
			model = llm.Model
		} else {
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s=%q needs %s; only %s reads a document without one",
				EnvOCRSDK, sdk, EnvOCRModel, aiprovider.SDKGoogleVision)
		}
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
	// parseOCR already refuses a named OCR provider with no key.
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
		EmbeddingModel:                e.Providers.LLM.EmbeddingModel,
		OCRTimeout:                    e.OCRTimeout,
		DeepSearchLanguages:           e.DeepSearchLanguages,
		SearchContextTokens:           e.SearchContextTokens,
		OpenAITimeout:                 e.AITimeout,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 e.WorkerTimeout,
		WorkerMaxRetries:              e.WorkerMaxRetries,
		ExtractionPromptVer:           e.ExtractionPromptVer,
		NearDuplicateDetectionEnabled: e.NearDuplicateEnabled,
		NearDuplicateThreshold:        e.NearDuplicateThreshold,
	}
}
