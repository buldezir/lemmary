package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/strutil"
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

	// ChatGPTLogin opens the chatgpt SDK, which bills an operator's ChatGPT
	// subscription by talking to OpenAI's own Codex backend. Off unless asked for:
	// those endpoints are undocumented and reserved for OpenAI's clients, so
	// pointing an account at them is the operator's decision. Refused together
	// with Managed, whose tenant is not the party whose account is at risk.
	ChatGPTLogin bool

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

// Environment variable names in one place, so the error messages and the
// parsing cannot drift apart.
const (
	EnvManaged = "AI_MANAGED"

	EnvAISDK     = "AI_SDK"
	EnvAIAPIKey  = "AI_API_KEY"
	EnvAIBaseURL = "AI_BASE_URL"
	EnvAIModel   = "AI_MODEL"

	// EnvAIEmbeddingModel names the retrieval embedding model: on the AI_SDK
	// provider by default, or on the AI_EMBEDDING_SDK one when that is set.
	EnvAIEmbeddingModel = "AI_EMBEDDING_MODEL"

	// The embedding provider block exists for a self-hosted embedding sidecar,
	// which is by definition a different endpoint from the language model. Unset
	// still means embeddings ride on the AI_SDK provider.
	EnvAIEmbeddingSDK     = "AI_EMBEDDING_SDK"
	EnvAIEmbeddingAPIKey  = "AI_EMBEDDING_API_KEY"
	EnvAIEmbeddingBaseURL = "AI_EMBEDDING_BASE_URL"

	// EnvAISearchHelperModel names the model on the AI_SDK provider that Deep
	// Search hands bulk per-document work to. Empty falls back to the search model.
	EnvAISearchHelperModel = "AI_SEARCH_HELPER_MODEL"

	// EnvChatGPTLogin opens the chatgpt SDK. See AIEnv.ChatGPTLogin and
	// docs/chatgpt_login.md.
	EnvChatGPTLogin = "AI_CHATGPT_LOGIN"

	EnvOCRSDK     = "OCR_SDK"
	EnvOCRAPIKey  = "OCR_API_KEY"
	EnvOCRBaseURL = "OCR_BASE_URL"
	EnvOCRModel   = "OCR_MODEL"
)

// AIEnvFromEnv errors on incomplete values only in managed mode, where the
// tenant cannot repair a missing key from Settings. Off it, absence means the
// setup wizard will ask.
func AIEnvFromEnv() (AIEnv, error) {
	managed, err := strictBool(EnvManaged)
	if err != nil {
		return AIEnv{}, err
	}
	// Strict for the same reason AI_MANAGED is: a typo read as "off" leaves an
	// operator staring at a Settings page with no sign-in button.
	chatgptLogin, err := strictBool(EnvChatGPTLogin)
	if err != nil {
		return AIEnv{}, err
	}
	if managed && chatgptLogin {
		return AIEnv{}, fmt.Errorf("%s and %s cannot both be set: a managed instance bills the operator's own provider", EnvManaged, EnvChatGPTLogin)
	}

	env := AIEnv{
		Managed:                managed,
		ChatGPTLogin:           chatgptLogin,
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

// strictBool refuses a value it cannot read rather than falling back to off:
// AI_MANAGED is the billing lock, and a typo read as "off" would leave
// Settings editable and the environment unapplied.
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
	baseURL := aiprovider.NormalizeBaseURL(
		strings.TrimSpace(getEnv(EnvAISDK, aiprovider.SDKOpenAI)), os.Getenv(EnvAIBaseURL))
	// An OpenAI-compatible SDK aimed at OpenCode is the opencode SDK. Read before
	// the checks below, so they ask about the SDK that will actually serve.
	sdk := aiprovider.NormalizeOpenCodeSDK(
		strings.TrimSpace(getEnv(EnvAISDK, aiprovider.SDKOpenAI)), baseURL)
	// EnvLLMSDKs, not LLMSDKs: chatgpt chats, but its credential is minted by
	// signing in, so naming it here would seed a row this file cannot complete.
	if !aiprovider.IsLLM(sdk) || aiprovider.RequiresOAuth(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q is not a language-model SDK that can be configured from the environment (want one of %s)",
			EnvAISDK, sdk, strings.Join(aiprovider.EnvLLMSDKs(), ", "))
	}

	spec := aiprovider.ProviderSpec{
		SDK:            sdk,
		APIKey:         strings.TrimSpace(os.Getenv(EnvAIAPIKey)),
		BaseURL:        baseURL,
		Model:          strings.TrimSpace(getEnv(EnvAIModel, aiprovider.DefaultExtractModel)),
		EmbeddingModel: strings.TrimSpace(os.Getenv(EnvAIEmbeddingModel)),
		HelperModel:    strings.TrimSpace(os.Getenv(EnvAISearchHelperModel)),
	}
	return spec, nil
}

// parseOCR reads the optional second provider. Unset runs OCR on the LLM.
func parseOCR(llm aiprovider.ProviderSpec) (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(os.Getenv(EnvOCRSDK))
	key := strings.TrimSpace(os.Getenv(EnvOCRAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvOCRBaseURL))
	model := strings.TrimSpace(os.Getenv(EnvOCRModel))
	// Before the SDK is compared with the language model, or an OCR_SDK=openai on
	// the same OpenCode endpoint would look like a second provider with no key.
	sdk = aiprovider.NormalizeOpenCodeSDK(sdk, strutil.FirstNonEmpty(baseURL, llm.BaseURL))

	if sdk == "" {
		if key != "" || baseURL != "" || model != "" {
			// A key or model without an SDK is a half-written intention; folding it into
			// the LLM provider would point OCR somewhere not asked for.
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
		// Valid as an SDK, just not for this job. Caught here rather than on the first
		// uploaded document.
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q cannot read a document (want one of %s)",
			EnvOCRSDK, sdk, strings.Join(aiprovider.OCRSDKs(), ", "))
	}
	// EnvOCRSDKs, not OCRSDKs: chatgpt reads documents, but its credential is
	// minted by signing in, so naming it here would seed a row this file can never
	// complete. Bind OCR to it from Settings once it is signed in.
	if aiprovider.RequiresOAuth(sdk) {
		return aiprovider.ProviderSpec{}, fmt.Errorf(
			"%s=%q cannot be configured from the environment: it is signed in to from Settings, not given a key (want one of %s)",
			EnvOCRSDK, sdk, strings.Join(aiprovider.EnvOCRSDKs(), ", "))
	}

	// The same SDK is the same endpoint, so reuse the language model credential.
	if sdk == llm.SDK {
		if key == "" {
			key = llm.APIKey
		}
		if baseURL == "" {
			baseURL = llm.BaseURL
		}
	} else if key == "" && aiprovider.RequiresAPIKey(sdk) {
		// Rejected in both modes: off managed the environment seeds once, and OCR
		// would silently bind to the language model. A local sidecar is exempt because
		// it has no key to give, being reached by URL alone.
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
// on the language model. Naming an SDK without a model would create a provider
// row with nothing bound to it, which reads as a feature that never embeds.
func parseEmbedding(llm aiprovider.ProviderSpec) (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(os.Getenv(EnvAIEmbeddingSDK))
	key := strings.TrimSpace(os.Getenv(EnvAIEmbeddingAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvAIEmbeddingBaseURL))
	model := strings.TrimSpace(os.Getenv(EnvAIEmbeddingModel))
	// Same reading as parseLLM, so CanEmbed refuses an AI_EMBEDDING_SDK=openai
	// pointed at OpenCode: that endpoint has no /embeddings whatever it is called.
	sdk = aiprovider.NormalizeOpenCodeSDK(sdk, strutil.FirstNonEmpty(baseURL, llm.BaseURL))

	if sdk == "" {
		if key != "" || baseURL != "" {
			// Same half-written intention parseOCR refuses. AI_EMBEDDING_MODEL alone is
			// not in this list: on its own it means "embed on the AI_SDK provider".
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s or %s is set without %s; name the embedding provider's SDK, or leave them both unset to embed on the %s provider",
				EnvAIEmbeddingAPIKey, EnvAIEmbeddingBaseURL, EnvAIEmbeddingSDK, EnvAISDK)
		}
		// ...unless the language model's SDK cannot embed: opencode chats but serves
		// no /embeddings, so there is nothing to fall back to. Caught here rather than
		// at the first upload, with the binding still reading as configured.
		if model != "" && !aiprovider.CanEmbed(llm.SDK) {
			return aiprovider.ProviderSpec{}, fmt.Errorf(
				"%s is set but %s=%q cannot serve embeddings; name a %s (one of %s), or unset %s to search by keywords alone",
				EnvAIEmbeddingModel, EnvAISDK, llm.SDK, EnvAIEmbeddingSDK,
				strings.Join(aiprovider.EmbeddingSDKs(), ", "), EnvAIEmbeddingModel)
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

	// The same SDK is the same endpoint, so reuse the language model credential.
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

// validateManaged refuses what a managed instance cannot serve and cannot be
// repaired out of.
func (e AIEnv) validateManaged() error {
	if !e.Providers.LLM.Configured() {
		return fmt.Errorf("%s=1 requires %s; a managed instance has no setup wizard to supply one",
			EnvManaged, EnvAIAPIKey)
	}
	if e.Providers.LLM.Model == "" {
		return fmt.Errorf("%s=1 requires %s", EnvManaged, EnvAIModel)
	}
	// parseOCR already refuses a named OCR provider with no key; the keyless SDKs
	// need an address, which NormalizeBaseURL always supplies. So this only fires
	// if that default is removed, and it fires here because a managed instance has
	// no Settings page to fix it in.
	ocr := e.Providers.OCR
	if ocr.Requested() && aiprovider.RequiresBaseURL(ocr.SDK) && strings.TrimSpace(ocr.BaseURL) == "" {
		return fmt.Errorf("%s=1 with %s=%q requires %s; a local OCR engine is reached by address alone",
			EnvManaged, EnvOCRSDK, ocr.SDK, EnvOCRBaseURL)
	}
	return nil
}

// Defaults is the Config an install starts from, and the fallback when the
// settings record cannot be read, so it must never return something unusable.
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
