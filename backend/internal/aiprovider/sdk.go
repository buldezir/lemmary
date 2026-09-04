package aiprovider

import "strings"

const (
	SDKOpenAI       = "openai"
	SDKOpenRouter   = "openrouter"
	SDKGoogleVision = "google_vision"
	SDKMistral      = "mistral"

	// SDKLocal is an OpenAI-compatible embeddings endpoint the operator runs
	// themselves -- text-embeddings-inference in the compose overlay, though
	// anything that serves /v1/embeddings will do. It embeds and nothing else:
	// it is refused as AI_SDK and OCR_SDK, and it is the one SDK that needs no
	// credential, because a service on the compose network has nobody to
	// authenticate to.
	SDKLocal = "local"

	CollectionName = "ai_providers"
)

var ValidSDKs = []string{SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKLocal}

func ValidSDK(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKLocal:
		return true
	default:
		return false
	}
}

func IsLLM(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral:
		return true
	default:
		return false
	}
}

// CanEmbed reports whether an SDK can serve the retrieval embedding binding.
//
// It is deliberately not IsLLM, which it used to be by coincidence: every SDK
// that chatted also embedded, so one predicate covered both. SDKLocal embeds
// without chatting, which is what forces them apart -- and asking the right
// question at each binding is what keeps a local provider out of the extraction
// picker and a Google Vision provider out of the embedding one.
func CanEmbed(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral, SDKLocal:
		return true
	default:
		return false
	}
}

// CanOCR reports whether an SDK can read a document. Everything but SDKLocal
// can: google_vision is the SDK OCR exists for, and the three LLM SDKs send the
// file to a model. A local embeddings endpoint has no way to do it at all.
//
// Without this, ValidSDK would let OCR_SDK=local through -- it is a valid SDK,
// just not for this job -- and the failure would only appear on the first
// document uploaded.
func CanOCR(sdk string) bool {
	return strings.TrimSpace(sdk) != SDKLocal
}

// RequiresAPIKey is false only for SDKLocal. Everywhere else an empty key means
// a half-written configuration, and the app has always read it that way.
func RequiresAPIKey(sdk string) bool {
	return strings.TrimSpace(sdk) != SDKLocal
}

// HasCredential reports whether a provider carries what its SDK needs to be
// called. It replaces the bare `p.APIKey != ""` test that stood in for this
// question before a keyless SDK existed.
func HasCredential(p Provider) bool {
	return strings.TrimSpace(p.APIKey) != "" || !RequiresAPIKey(p.SDK)
}

func RequiresOCRModel(sdk string) bool {
	return strings.TrimSpace(sdk) != SDKGoogleVision
}

func DefaultBaseURL(sdk string) string {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI:
		return "https://api.openai.com/v1"
	case SDKOpenRouter:
		return "https://openrouter.ai/api/v1"
	case SDKMistral:
		return "https://api.mistral.ai/v1"
	case SDKLocal:
		// The service name in docker-compose.embeddings.yml, so the default is
		// already right for the overlay and inert for anyone not running it.
		return "http://embeddings:80/v1"
	default:
		return ""
	}
}

func DefaultAlias(sdk string) string {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI:
		return "OpenAI"
	case SDKOpenRouter:
		return "OpenRouter"
	case SDKGoogleVision:
		return "Google Cloud Vision"
	case SDKMistral:
		return "Mistral"
	case SDKLocal:
		return "Local embeddings"
	default:
		return sdk
	}
}

func NormalizeBaseURL(sdk, baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed != "" {
		return trimmed
	}
	return strings.TrimRight(DefaultBaseURL(sdk), "/")
}

func ChatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL(SDKOpenAI)
	}
	return base + "/chat/completions"
}

// EmbeddingsURL is the /embeddings endpoint for an OpenAI-compatible base URL.
// It exists for the outbound request log: the SDK builds the real URL itself,
// and a log line that guessed a different one would be worse than none.
func EmbeddingsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL(SDKOpenAI)
	}
	return base + "/embeddings"
}
