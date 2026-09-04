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
	// it is refused as AI_SDK and OCR_SDK, and like SDKDocling it needs no
	// credential, because a service on the compose network has nobody to
	// authenticate to.
	SDKLocal = "local"

	// SDKDocling is an OCR engine the operator runs themselves, as a sidecar
	// container beside the app. Like SDKLocal it is reached without a
	// credential: see RequiresAPIKey.
	//
	// One local OCR SDK rather than several, on purpose. Docling's default
	// engine is RapidOCR, which is PaddleOCR's own PP-OCR models exported to
	// ONNX, so a separate PaddleOCR SDK would have been a second multi-gigabyte
	// container to run the recognition this one already does.
	SDKDocling = "docling"

	CollectionName = "ai_providers"
)

var ValidSDKs = []string{SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKLocal, SDKDocling}

func ValidSDK(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKLocal, SDKDocling:
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
// can: google_vision and docling are engines OCR exists for, and the three LLM
// SDKs send the file to a model. A local embeddings endpoint has no way to do
// it at all.
//
// Without this, ValidSDK would let OCR_SDK=local through -- it is a valid SDK,
// just not for this job -- and the failure would only appear on the first
// document uploaded.
func CanOCR(sdk string) bool {
	return strings.TrimSpace(sdk) != SDKLocal
}

// RequiresAPIKey reports whether an SDK is reached with a credential.
//
// Every hosted SDK is. The two sidecar SDKs are addressed by URL alone: they
// sit on the compose network with no port published, and inventing a key for
// them would be a field an admin has to fill in with something arbitrary before
// anything would run.
//
// This exists because "has an API key" was the codebase's synonym for "is
// configured" -- in ProviderSpec.Configured, Provider.Configured, config.HasOCR,
// config.HasEmbedding, aiprovider.ListModels, the provider create handler and
// the OCR test listing. Every one of those now asks this or Configured first.
// It defaults to the strict answer: an unknown or empty SDK still demands a
// key.
func RequiresAPIKey(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKLocal, SDKDocling:
		return false
	default:
		return true
	}
}

// IsLocalOCR reports whether the SDK is an OCR engine on the operator's own
// hardware. Named separately from !RequiresAPIKey because the call sites mean
// different things: one is about authentication, the other about where the
// document goes and how long it takes to read. SDKLocal is not one of these --
// it cannot read a document at all.
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
// client, which owns its own address. The sidecars are the opposite -- their
// address is the only thing distinguishing one install's from another's, and
// DefaultBaseURL can do no better than guess at the compose service name.
func RequiresBaseURL(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKLocal, SDKDocling:
		return true
	default:
		return false
	}
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
		if CanOCR(sdk) && !RequiresOCRModel(sdk) {
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
	case SDKLocal:
		// The service name in docker-compose.embeddings.yml, so the default is
		// already right for the overlay and inert for anyone not running it.
		return "http://embeddings:80/v1"
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
	case SDKLocal:
		return "Local embeddings"
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
