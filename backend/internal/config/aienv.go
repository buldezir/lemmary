package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"lemmary/backend/internal/aiprovider"
)

// AIEnv is everything the environment says about AI, parsed once.
//
// Two modes share this struct, and the mode is the whole of the difference:
//
//   - Self-hosted (Managed false, the default). The values below seed the
//     settings singleton on the first boot and are inert afterwards. An
//     operator who wants a test instance up without walking the setup wizard
//     puts a key in .env; an operator who wants to change one later uses the
//     Settings page, which is the only authority once the singleton exists.
//
//   - Managed (Managed true). The operator owns the AI bill, so these are
//     re-applied on every boot and the tenant's Settings page does not offer
//     Providers, Models or duplicate detection at all. What the container's
//     environment says is what the instance runs.
//
// This replaced a digest-per-variable scheme (app_settings.env_applied) whose
// only job was to let one code path serve both modes: a changed variable was
// applied, an unchanged one left alone. Naming the modes makes the digests
// unnecessary, and "which of these two rules am I under" is a question an
// operator can answer, where "has this variable changed since the last boot
// that acted on it" was not.
type AIEnv struct {
	Managed   bool
	Providers aiprovider.Bootstrap

	// Re-applied on every boot in managed mode, because each is a cost the
	// operator carries rather than a preference the tenant holds: a research
	// budget, and whether every upload pays for duplicate detection.
	SearchContextTokens    int
	NearDuplicateEnabled   bool
	NearDuplicateThreshold float64

	// Seed-only in both modes. Tuning an admin does, with no operator decision
	// behind it, so managed mode leaves these editable rather than resetting
	// somebody's timeout on every restart.
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

	EnvOCRSDK     = "OCR_SDK"
	EnvOCRAPIKey  = "OCR_API_KEY"
	EnvOCRBaseURL = "OCR_BASE_URL"
	EnvOCRModel   = "OCR_MODEL"
)

// AIEnvFromEnv parses the AI environment.
//
// It returns an error only in managed mode. Off it, an incomplete environment
// is not a mistake — it is an install that will be configured from the setup
// wizard, which is the ordinary self-hosted path. On it, an incomplete
// environment is unrecoverable from inside the instance: the Settings page is
// gone and nobody there can supply the missing key, so the honest answer is to
// refuse to start and say which variable is wrong. That is the same judgement
// vault.OptionsFromEnv makes about an unparseable boolean, and it lands where
// it can be acted on — the panel's create ladder ends in a health check, so a
// bad seed fails the provision instead of handing over a broken workspace.
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
//
// The same judgement vault.envBool makes, for a stronger reason. Everywhere else
// in this file an unreadable value falls back, because a typo in a timeout must
// not become a zero-second HTTP deadline. This flag is the billing lock: read as
// "off", AI_MANAGED=yes leaves the Settings page editable and the environment
// unapplied, and the operator finds out from an invoice. It also accepts the
// same spellings .env.example documents for the VAULT_* family, so that
// 1/true/yes/on mean here what they mean four blocks further down the file.
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
		SDK:     sdk,
		APIKey:  strings.TrimSpace(os.Getenv(EnvAIAPIKey)),
		BaseURL: aiprovider.NormalizeBaseURL(sdk, os.Getenv(EnvAIBaseURL)),
		Model:   strings.TrimSpace(getEnv(EnvAIModel, aiprovider.DefaultExtractModel)),
	}
	return spec, nil
}

// parseOCR reads the optional second provider. Unset means OCR runs on the
// language model, which is what makes one API key a complete configuration.
func parseOCR(llm aiprovider.ProviderSpec) (aiprovider.ProviderSpec, error) {
	sdk := strings.TrimSpace(os.Getenv(EnvOCRSDK))
	key := strings.TrimSpace(os.Getenv(EnvOCRAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvOCRBaseURL))
	model := strings.TrimSpace(os.Getenv(EnvOCRModel))

	if sdk == "" {
		if key != "" || baseURL != "" || model != "" {
			// Naming a key or a model without an SDK is a half-written
			// intention, and silently folding it into the LLM provider would
			// point OCR somewhere the operator did not ask for.
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
		// A second endpoint with nothing to authenticate to it. Rejected in
		// both modes, and rejected here rather than in validateManaged, because
		// the damage is worse off managed mode than on it: the environment
		// seeds once and is then inert, so OCR would silently bind to the
		// language model, bill it for every page, and never prompt the wizard
		// to ask about the provider that was actually asked for.
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
	// Nothing here about OCR. parseOCR already refuses a named OCR provider with
	// no key, in both modes, so by this point OCR either has its own credential
	// or shares the language model's -- and the language model has one, checked
	// above. A clause here would be unreachable, and an unreachable check reads
	// as protection that is not there.
	return nil
}

// Defaults is the Config an install starts from: the environment's values over
// the code defaults, with everything the environment no longer speaks to left
// at its zero value for the Settings page to fill in.
//
// Also the fallback when the settings record cannot be read, which is why it
// must never return something unusable.
func (e AIEnv) Defaults() Config {
	return Config{
		OCRModel:                      e.Providers.OCRModel(),
		ExtractModel:                  e.Providers.LLM.Model,
		ChatModel:                     e.Providers.LLM.Model,
		SearchModel:                   e.Providers.LLM.Model,
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
