package aiprovider

import "strings"

const (
	SDKOpenAI       = "openai"
	SDKOpenRouter   = "openrouter"
	SDKGoogleVision = "google_vision"
	SDKMistral      = "mistral"

	// SDKOpenCode is OpenCode Go (Zen), a gateway in front of a catalogue of
	// third-party models. Its own SDK rather than an `openai` row with a base URL,
	// because it differs in two ways no base URL can express: it requires the
	// x-opencode-session header, and it serves each model on one of
	// /chat/completions, /responses or /messages, the last of which speaks
	// Anthropic's Messages API. Which one is a property of the model and /v1/models
	// does not say, so internal/opencode carries the table.
	//
	// It chats and reads documents; it does not embed, so CanEmbed refuses it.
	SDKOpenCode = "opencode"

	// SDKChatGPT reaches OpenAI's Codex backend with a ChatGPT subscription
	// instead of a metered API key. It is the only SDK whose credential is not a
	// string an admin can paste: the device-code flow in internal/chatgpt mints an
	// OAuth token pair, which lives in the row's `oauth` column, hence RequiresOAuth
	// beside RequiresAPIKey.
	//
	// It chats and reads documents; the Codex backend serves no /embeddings, so
	// CanEmbed refuses it. The endpoints behind it are OpenAI's own first-party
	// ones, undocumented and reserved for OpenAI's clients. See
	// docs/chatgpt_login.md.
	SDKChatGPT = "chatgpt"

	// SDKLocalEmbeddings is an OpenAI-compatible embeddings endpoint the operator
	// runs themselves. It embeds and nothing else, and like SDKDocling needs no
	// credential: a service on the compose network has nobody to authenticate to.
	SDKLocalEmbeddings = "local"

	// SDKDocling is an OCR engine the operator runs themselves, reached without a
	// credential. One local OCR SDK rather than several: Docling's default engine is
	// RapidOCR, PaddleOCR's own models exported to ONNX, so a separate PaddleOCR SDK
	// would be a second multi-gigabyte container doing the same recognition.
	SDKDocling = "docling"

	// SDKTavily is a web-search API, the only SDK here that serves neither a
	// model nor a document: it answers a query with ranked results and extracts
	// a page's text. It backs the web_search and web_fetch tools, so CanOCR has
	// to refuse it explicitly -- that predicate defaults to true, and a Tavily
	// row bound to OCR would only fail on the first uploaded document.
	SDKTavily = "tavily"

	CollectionName = "ai_providers"
)

var ValidSDKs = []string{SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKOpenCode, SDKChatGPT, SDKLocalEmbeddings, SDKDocling, SDKTavily}

func ValidSDK(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKGoogleVision, SDKMistral, SDKOpenCode, SDKChatGPT, SDKLocalEmbeddings, SDKDocling, SDKTavily:
		return true
	default:
		return false
	}
}

func IsLLM(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral, SDKOpenCode, SDKChatGPT:
		return true
	default:
		return false
	}
}

// CanEmbed reports whether an SDK can serve the retrieval embedding binding.
// Deliberately not IsLLM: SDKLocalEmbeddings embeds without chatting, and
// SDKOpenCode and SDKChatGPT chat without embedding. Deep Search's vectors want
// a second provider on those, and keyword retrieval alone is a working state
// until there is one.
func CanEmbed(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI, SDKOpenRouter, SDKMistral, SDKLocalEmbeddings:
		return true
	default:
		return false
	}
}

// CanOCR reports whether an SDK can read a document: the OCR engines, and every
// LLM SDK, which sends the file to a model. A local embeddings endpoint cannot.
// Without this, ValidSDK would let OCR_SDK=local through and the failure would
// only appear on the first document uploaded.
func CanOCR(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKLocalEmbeddings, SDKTavily:
		return false
	default:
		return true
	}
}

// CanWebSearch reports whether an SDK can serve the web-search binding, which
// backs the web_search and web_fetch tools. An allow-list, like CanEmbed: an
// SDK that has not been taught to search the web cannot do it by accident.
func CanWebSearch(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKTavily:
		return true
	default:
		return false
	}
}

// RequiresOAuth reports whether an SDK is reached with a token this app minted
// rather than one an admin typed. Asked separately from RequiresAPIKey: the
// create handler must not demand an api_key, Settings must show a sign-in
// button, and Configured has to look at a different column.
func RequiresOAuth(sdk string) bool {
	return strings.TrimSpace(sdk) == SDKChatGPT
}

// RequiresAPIKey reports whether an SDK is reached with a credential. Every
// hosted SDK is; the two sidecar SDKs are addressed by URL alone. It defaults
// to the strict answer: an unknown or empty SDK still demands a key.
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
// document goes and how long it takes to read.
func IsLocalOCR(sdk string) bool {
	switch strings.TrimSpace(sdk) {
	case SDKDocling:
		return true
	default:
		return false
	}
}

// RequiresBaseURL reports whether an SDK is useless without an address. The
// hosted SDKs have a documented endpoint DefaultBaseURL supplies, and
// google_vision takes none at all: it speaks gRPC through the official client.
// A sidecar's address is the only thing distinguishing one install from
// another's.
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
// Every "want one of ..." message is built through this rather than written out,
// so it cannot drift from the predicates.
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
func WebSearchSDKs() []string { return sdksWhere(CanWebSearch) }

// EnvLLMSDKs and EnvOCRSDKs name the SDKs AI_SDK and OCR_SDK accept, which is
// LLMSDKs and OCRSDKs minus the ones whose credential cannot be written down.
// The environment can carry a key but not a sign-in, so naming a chatgpt
// provider there would seed a row nobody can complete.
func EnvLLMSDKs() []string {
	return sdksWhere(func(sdk string) bool { return IsLLM(sdk) && !RequiresOAuth(sdk) })
}

func EnvOCRSDKs() []string {
	return sdksWhere(func(sdk string) bool { return CanOCR(sdk) && !RequiresOAuth(sdk) })
}

// ModellessOCRSDKs names the SDKs that read a document without a model, for the
// error messages that list them. Derived so it cannot drift from
// RequiresOCRModel.
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
	case SDKOpenCode:
		return "https://opencode.ai/zen/go/v1"
	// Not a /v1 root: the Codex backend serves one endpoint, /responses, which
	// the chatgpt middleware rewrites the SDK's /chat/completions into.
	case SDKChatGPT:
		return "https://chatgpt.com/backend-api/codex"
	case SDKLocalEmbeddings:
		// The service name in docker-compose.embeddings.yml, so the default is
		// right for the overlay and inert for anyone not running it.
		return "http://embeddings:80/v1"
	// The sidecar service name and port from docker-compose.local-ocr.yml, so
	// OCR_SDK=docling alone is a complete configuration for that overlay.
	case SDKDocling:
		return "http://docling:5001"
	case SDKTavily:
		return "https://api.tavily.com"
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
	case SDKOpenCode:
		return "Opencode Go"
	case SDKChatGPT:
		return "ChatGPT subscription"
	case SDKLocalEmbeddings:
		return "Local embeddings"
	case SDKDocling:
		return "Docling"
	case SDKTavily:
		return "Tavily"
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
