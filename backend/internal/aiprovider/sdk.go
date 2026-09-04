package aiprovider

import "strings"

const (
	SDKOpenAI       = "openai"
	SDKOpenRouter   = "openrouter"
	SDKGoogleVision = "google_vision"
	SDKMistral      = "mistral"

	// SDKDocling is an OCR engine the operator runs themselves, as a sidecar
	// container beside the app. It is the only SDK reached without a
	// credential: see RequiresAPIKey.
	//
	// One local SDK rather than several, on purpose. Docling's default engine
	// is RapidOCR, which is PaddleOCR's own PP-OCR models exported to ONNX, so
	// a separate PaddleOCR SDK would have been a second multi-gigabyte
	// container to run the recognition this one already does.
	SDKDocling = "docling"

	CollectionName = "ai_providers"
)

var ValidSDKs = []string{SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKDocling}

func ValidSDK(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKDocling:
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

// RequiresAPIKey reports whether an SDK is reached with a credential.
//
// Every hosted SDK is. The local sidecar is addressed by URL alone: it sits on
// the compose network with no port published, and inventing a key for it would
// be a field an admin has to fill in with something arbitrary before OCR would
// run.
//
// This exists because "has an API key" was the codebase's synonym for "is
// configured" -- in ProviderSpec.Configured, config.HasOCR, the provider create
// handler and the OCR test listing. Every one of those now asks this first.
func RequiresAPIKey(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKDocling:
		return false
	default:
		return true
	}
}

// IsLocalOCR reports whether the SDK is an engine on the operator's own
// hardware. Named separately from !RequiresAPIKey because the call sites mean
// different things: one is about authentication, the other about where the
// document goes and how long it takes to read.
func IsLocalOCR(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKDocling:
		return true
	default:
		return false
	}
}

// RequiresBaseURL reports whether an SDK is useless without an address.
//
// The hosted SDKs all have a documented endpoint that DefaultBaseURL supplies,
// so an empty base_url means "use the default" rather than "unconfigured".
// google_vision takes no base URL at all: it speaks gRPC through the official
// client, which owns its own address. The local sidecar is the opposite -- its
// address is the only thing distinguishing one install's from another's, and
// DefaultBaseURL can do no better than guess at the compose service name.
func RequiresBaseURL(sdk string) bool {
	return IsLocalOCR(sdk)
}

func RequiresOCRModel(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKGoogleVision, SDKDocling:
		return false
	default:
		return true
	}
}

// ModellessOCRSDKs names the SDKs that read a document without a model, for the
// error messages that have to list them. Derived rather than written out, so it
// cannot drift from RequiresOCRModel the way the old message naming only
// google_vision did.
func ModellessOCRSDKs() []string {
	out := make([]string, 0, len(ValidSDKs))
	for _, sdk := range ValidSDKs {
		if !RequiresOCRModel(sdk) {
			out = append(out, sdk)
		}
	}
	return out
}

func DefaultBaseURL(sdk string) string {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI:
		return "https://api.openai.com/v1"
	case SDKOpenRouter:
		return "https://openrouter.ai/api/v1"
	case SDKMistral:
		return "https://api.mistral.ai/v1"
	// The sidecar service name and port from docker-compose.local-ocr.yml, so
	// that OCR_SDK=docling alone is a complete configuration for anyone running
	// the overlay unedited. Anyone who moved it sets OCR_BASE_URL.
	case SDKDocling:
		return "http://docling:5001"
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
	case SDKDocling:
		return "Docling"
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
