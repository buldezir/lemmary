package aiprovider

import "strings"

const (
	SDKOpenAI       = "openai"
	SDKOpenRouter   = "openrouter"
	SDKGoogleVision = "google_vision"
	SDKMistral      = "mistral"

	// SDKChatGPT reaches OpenAI's Codex backend with a ChatGPT subscription
	// instead of a metered API key: the operator signs in once and the
	// conversation is billed against the seat they already pay for.
	//
	// It is the only SDK whose credential is not a string an admin can paste.
	// The device-code flow in internal/chatgpt mints an OAuth token pair, which
	// lives in the row's `oauth` column and is refreshed in place -- hence
	// RequiresOAuth beside RequiresAPIKey.
	//
	// It chats and reads documents; it does not embed. The Codex backend
	// serves no /embeddings, so CanEmbed refuses it and Deep Search's vectors
	// keep whatever provider they had. Its models do take file and image input,
	// so OCR runs on the seat like any other LLM OCR provider -- see
	// internal/ocr.NewLLMProvider and the input_file/input_image parts in
	// internal/chatgpt.messageContent.
	//
	// Off unless AI_CHATGPT_LOGIN=1: the endpoints behind it are OpenAI's own
	// first-party ones, undocumented and reserved for OpenAI's clients, so
	// whether to point an account at them is the operator's call to make
	// deliberately. See docs/chatgpt_login.md.
	SDKChatGPT = "chatgpt"

	// SDKLocalEmbeddings is an OpenAI-compatible embeddings endpoint the operator runs
	// themselves -- text-embeddings-inference in the compose overlay, though
	// anything that serves /v1/embeddings will do. It embeds and nothing else:
	// it is refused as AI_SDK and OCR_SDK, and like SDKDocling it needs no
	// credential, because a service on the compose network has nobody to
	// authenticate to.
	SDKLocalEmbeddings = "local"

	// SDKDocling is an OCR engine the operator runs themselves, as a sidecar
	// container beside the app. Like SDKLocalEmbeddings it is reached without
	// a credential: see RequiresAPIKey.
	//
	// One local OCR SDK rather than several, on purpose. Docling's default
	// engine is RapidOCR, which is PaddleOCR's own PP-OCR models exported to
	// ONNX, so a separate PaddleOCR SDK would have been a second multi-gigabyte
	// container to run the recognition this one already does.
	SDKDocling = "docling"

	CollectionName = "ai_providers"
)

var ValidSDKs = []string{SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKChatGPT, SDKLocalEmbeddings, SDKDocling}

func ValidSDK(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKChatGPT, SDKLocalEmbeddings, SDKDocling:
		return true
	default:
		return false
	}
}

func IsLLM(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral, SDKChatGPT:
		return true
	default:
		return false
	}
}

// CanEmbed reports whether an SDK can serve the retrieval embedding binding.
//
// It is deliberately not IsLLM, which it used to be by coincidence: every SDK
// that chatted also embedded, so one predicate covered both.
// SDKLocalEmbeddings embeds without chatting, which is what forces them apart
// -- and asking the right question at each binding is what keeps a local
// provider out of the extraction picker and a Google Vision provider out of the
// embedding one.
func CanEmbed(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral, SDKLocalEmbeddings:
		return true
	default:
		return false
	}
}

// CanOCR reports whether an SDK can read a document. google_vision and docling
// are engines OCR exists for, and every LLM SDK sends the file to a model --
// SDKChatGPT included, since its models take the same file and image input the
// metered ones do. A local embeddings endpoint has no way to do it at all.
//
// Without this, ValidSDK would let OCR_SDK=local through -- it is a valid SDK,
// just not for this job -- and the failure would only appear on the first
// document uploaded.
func CanOCR(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKLocalEmbeddings:
		return false
	default:
		return true
	}
}

// RequiresOAuth reports whether an SDK is reached with a token this app minted
// rather than one an admin typed.
//
// Only SDKChatGPT is. It is asked separately from RequiresAPIKey because the
// two answers differ everywhere it matters: the create handler must not demand
// an api_key, the Settings form must show a sign-in button instead of a
// password field, and Configured has to look at a different column.
func RequiresOAuth(sdk string) bool {
	return strings.TrimSpace(sdk) == SDKChatGPT
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
	case SDKLocalEmbeddings, SDKDocling, SDKChatGPT:
		return false
	default:
		return true
	}
}

// IsLocalOCR reports whether the SDK is an OCR engine on the operator's own
// hardware. Named separately from !RequiresAPIKey because the call sites mean
// different things: one is about authentication, the other about where the
// document goes and how long it takes to read. SDKLocalEmbeddings is not one
// of these -- it cannot read a document at all.
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
	case SDKLocalEmbeddings, SDKDocling:
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

// sdksWhere names every valid SDK a predicate accepts, in ValidSDKs order.
//
// Every "want one of ..." message is built through this rather than written
// out. The four-name list in the OCR conflict message was already wrong when
// docling shipped -- it named openai, openrouter, mistral and google_vision,
// and an admin who believed it would not have tried the one SDK that reads a
// document on their own hardware.
func sdksWhere(pred func(string) bool) []string {
	out := make([]string, 0, len(ValidSDKs))
	for _, sdk := range ValidSDKs {
		if pred(sdk) {
			out = append(out, sdk)
		}
	}
	return out
}

// LLMSDKs, EmbeddingSDKs and OCRSDKs name the SDKs each binding accepts.
func LLMSDKs() []string       { return sdksWhere(IsLLM) }
func EmbeddingSDKs() []string { return sdksWhere(CanEmbed) }
func OCRSDKs() []string       { return sdksWhere(CanOCR) }

// EnvLLMSDKs and EnvOCRSDKs name the SDKs AI_SDK and OCR_SDK accept, which is
// LLMSDKs and OCRSDKs minus the ones whose credential cannot be written down.
//
// The environment can carry a key. It cannot carry a sign-in: a chatgpt
// provider is created and signed in to from Settings, so naming it in either
// variable would seed a provider row nobody can complete from the file that
// named it. That is true of OCR_SDK as much as AI_SDK, even though the SDK can
// now do the job -- the obstacle is the credential, not the capability.
func EnvLLMSDKs() []string {
	return sdksWhere(func(sdk string) bool { return IsLLM(sdk) && !RequiresOAuth(sdk) })
}

func EnvOCRSDKs() []string {
	return sdksWhere(func(sdk string) bool { return CanOCR(sdk) && !RequiresOAuth(sdk) })
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
	// Not a /v1 root: the Codex backend serves one endpoint, /responses, which
	// the chatgpt middleware rewrites the SDK's /chat/completions into.
	case SDKChatGPT:
		return "https://chatgpt.com/backend-api/codex"
	case SDKLocalEmbeddings:
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
	case SDKChatGPT:
		return "ChatGPT subscription"
	case SDKLocalEmbeddings:
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

// ResponsesURL is the /responses endpoint for an OpenAI-compatible base URL.
// Some models are served only there; see internal/ai/responses.go.
func ResponsesURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL(SDKOpenAI)
	}
	return base + "/responses"
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
