package aiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// ContextWindow is the model's context length in tokens, when the provider
	// reports one. OpenAI's /v1/models does not, so zero means "unknown" and the
	// configured default applies.
	ContextWindow int `json:"context_window,omitempty"`

	// caps is set when the provider returned a capabilities object (Mistral).
	caps *modelCapabilities
	// outputModalities is OpenRouter's architecture.output_modalities, the one
	// place a provider says outright that a model returns embeddings.
	outputModalities []string
}

type modelCapabilities struct {
	completionChat bool
	ocr            bool
}

const modelsListTimeout = 20 * time.Second

// AnthropicVersion dates the Messages API for the hand-rolled requests in this
// package. anthropic-sdk-go sends its own on every call it makes, so this is
// only for the ones it does not make.
const AnthropicVersion = "2023-06-01"

// ModelPurpose is the task a model is being picked for. It decides both which
// endpoint filter is asked for and which models are kept from the answer.
type ModelPurpose string

const (
	PurposeLLM       ModelPurpose = "llm"
	PurposeOCR       ModelPurpose = "ocr"
	PurposeEmbedding ModelPurpose = "embedding"
)

// ParseModelPurpose reads the `for=` query parameter. Anything unrecognised is
// the language-model list, which is the safe default: it is the longest list
// and the one an admin can always type past.
func ParseModelPurpose(raw string) ModelPurpose {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PurposeOCR):
		return PurposeOCR
	case string(PurposeEmbedding):
		return PurposeEmbedding
	default:
		return PurposeLLM
	}
}

// InfoURL is text-embeddings-inference's /info, which sits beside the /v1
// prefix rather than under it. It is the local catalogue: TEI serves exactly
// one model, and /info is where it names it.
func InfoURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/v1") + "/info"
}

func ModelsURL(p Provider, purpose ModelPurpose) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return ""
	}
	// A local endpoint has no /v1/models; /info is in TEI's OpenAPI spec and
	// names the one model it is serving.
	if p.SDK == SDKLocalEmbeddings {
		return InfoURL(base)
	}
	endpoint := base + "/models"
	// OpenRouter is the only provider that filters server-side, and its
	// embedding models are left out of the plain catalogue altogether, so
	// without the parameter the embedding picker is empty. The response still
	// goes through filterModels; the parameter is what makes the models appear.
	if p.SDK == SDKOpenRouter {
		var key, value string
		switch purpose {
		case PurposeOCR:
			key, value = "input_modalities", "file"
		case PurposeEmbedding:
			key, value = "output_modalities", "embeddings"
		default:
			return endpoint
		}
		u, err := url.Parse(endpoint)
		if err != nil {
			return endpoint + "?" + key + "=" + value
		}
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return endpoint
}

// ChatGPTModels is the catalogue for the chatgpt SDK, written out because the
// Codex backend publishes none. Names, not capabilities: which of these an
// account may use depends on its plan, and a model refused there surfaces as a
// provider error on the first request. Codex's own descriptions, because the
// picker renders `id (name)` and which of six near-identically-named models to
// bind is a real question. In Codex's order, most capable first.
func ChatGPTModels() []Model {
	return []Model{
		{ID: "gpt-6-astra", Name: "Our most capable model for complex, demanding work"},
		{ID: "gpt-5.6-sol", Name: "Reliable agentic workhorse for everyday tasks"},
		{ID: "gpt-5.6-terra", Name: "Balanced agentic coding model for everyday work"},
		{ID: DefaultExtractModel, Name: "Fast and affordable agentic coding model"},
		{ID: "gpt-5.5", Name: "Proven previous-generation model for coding and general work"},
		{ID: "gpt-5.4-mini", Name: "Small, fast, and cost-efficient model for simpler coding tasks"},
	}
}

func ListModels(ctx context.Context, p Provider, purpose ModelPurpose, client *http.Client, logger *slog.Logger) ([]Model, error) {
	// The one SDK whose catalogue is local. Answered before the checks below,
	// which would otherwise fail it for having a token rather than a key.
	if p.SDK == SDKChatGPT {
		if purpose == PurposeEmbedding {
			// CanEmbed already refuses that binding; returning nothing keeps
			// the picker honest if it is ever asked for anyway.
			return nil, nil
		}
		// The same list for OCR as for chat: the Codex catalogue says nothing
		// about which models take a file.
		return ChatGPTModels(), nil
	}

	// An SDK that neither chats nor embeds has no catalogue to list, and
	// returning nothing here keeps the checks below from turning a healthy
	// keyless sidecar into a 502. Not plain !IsLLM: the local embeddings
	// sidecar is keyless too, but it does name its one model at /info.
	if !IsLLM(p.SDK) && !CanEmbed(p.SDK) {
		return nil, nil
	}
	endpoint := ModelsURL(p, purpose)
	if endpoint == "" {
		return nil, fmt.Errorf("provider has no base URL")
	}
	if p.APIKey == "" && RequiresAPIKey(p.SDK) {
		return nil, fmt.Errorf("provider API key is not set")
	}

	if client == nil {
		client = &http.Client{Timeout: modelsListTimeout}
	}

	LogRequest(logger, p.SDK, http.MethodGet, endpoint, "", "for", string(purpose))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	// No blank Bearer for a keyless provider: an endpoint that does check the
	// header would rather see none than see an empty one.
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	// Past the SDKChatGPT early return above, so this never reaches Codex.
	req.Header.Set("User-Agent", UserAgent)
	// Hand-rolled request, so the SDK middleware that stamps this everywhere
	// else does not see it.
	if p.SDK == SDKOpenCode {
		req.Header.Set(SessionHeader, SessionFor("models"))
	}
	// Anthropic authenticates with x-api-key and dates its API in a header. The
	// bearer above is not merely redundant there: a request carrying both is
	// read as an OAuth one and refused.
	if p.SDK == SDKAnthropic {
		req.Header.Del("Authorization")
		req.Header.Set("x-api-key", p.APIKey)
		req.Header.Set("anthropic-version", AnthropicVersion)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("list models: HTTP %d: %s", resp.StatusCode, msg)
	}

	if p.SDK == SDKLocalEmbeddings {
		models, err := parseInfoResponse(body)
		if err != nil {
			return nil, err
		}
		return filterModels(models, p.SDK, purpose), nil
	}

	models, err := parseModelsResponse(body)
	if err != nil {
		return nil, err
	}
	return filterModels(models, p.SDK, purpose), nil
}

// parseInfoResponse reads text-embeddings-inference's /info into the one model
// it is serving. model_type is the field that matters: TEI serves rerankers and
// classifiers from the same endpoint shape, and either bound as an embedding
// model would fail on every document. An unrecognised type yields no models.
func parseInfoResponse(body []byte) ([]Model, error) {
	var info struct {
		ModelID         string `json:"model_id"`
		ServedModelName string `json:"served_model_name"`
		ModelType       any    `json:"model_type"`
		MaxInputLength  int    `json:"max_input_length"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode info response: %w", err)
	}
	if !infoIsEmbedding(info.ModelType) {
		return nil, nil
	}
	id := strings.TrimSpace(info.ServedModelName)
	if id == "" {
		id = strings.TrimSpace(info.ModelID)
	}
	if id == "" {
		return nil, fmt.Errorf("info response names no model")
	}
	name := strings.TrimSpace(info.ModelID)
	if name == "" {
		name = id
	}
	// max_input_length is in tokens, the same unit ContextWindow is in
	// everywhere else.
	return []Model{{ID: id, Name: name, ContextWindow: info.MaxInputLength}}, nil
}

// infoIsEmbedding reads TEI's model_type, which is a tagged union: a bare
// "embedding" string in some versions, an object keyed by the variant name
// ({"embedding":{"pooling":"cls"}}) in others. Both spellings have shipped and
// both mean the same thing, so both are accepted.
func infoIsEmbedding(modelType any) bool {
	switch v := modelType.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "embedding")
	case map[string]any:
		for key := range v {
			if strings.EqualFold(strings.TrimSpace(key), "embedding") {
				return true
			}
		}
	}
	return false
}

// filterModels keeps only the models that can serve purpose. Providers describe
// their catalogue very differently, so the rules are per-SDK with a name
// heuristic underneath. The heuristic is one-sided on purpose: an embedding
// model must never appear in the LLM or OCR lists, while a model missing from a
// list costs an admin one line in the Custom model id field.
func filterModels(models []Model, sdk string, purpose ModelPurpose) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if includeModel(m, sdk, purpose) {
			out = append(out, m)
		}
	}
	return out
}

func includeModel(m Model, sdk string, purpose ModelPurpose) bool {
	// A local endpoint serves embeddings and nothing else, and its model name
	// ("BAAI/bge-m3") carries no "embed" for the heuristic to find.
	if sdk == SDKLocalEmbeddings {
		return purpose == PurposeEmbedding
	}
	if purpose == PurposeEmbedding {
		return isEmbeddingModel(m, sdk)
	}
	if isEmbeddingModel(m, sdk) {
		return false
	}
	if sdk != SDKMistral {
		return true
	}
	if m.caps != nil {
		if purpose == PurposeOCR {
			return m.caps.ocr
		}
		return m.caps.completionChat
	}
	if purpose == PurposeOCR {
		return modelContains(m, "ocr")
	}
	return !modelContains(m, "ocr")
}

func isEmbeddingModel(m Model, sdk string) bool {
	switch sdk {
	case SDKMistral:
		// Mistral's capabilities object names chat and OCR but has no flag for
		// embeddings, so an embedding model is the one that admits to neither.
		if m.caps != nil {
			return !m.caps.completionChat && !m.caps.ocr && modelContains(m, "embed")
		}
	case SDKOpenRouter:
		// OpenRouter is explicit, and its "embeddings" output modality is the
		// only authoritative answer any provider gives us.
		if len(m.outputModalities) > 0 {
			return slices.Contains(m.outputModalities, "embeddings")
		}
	}
	return modelContains(m, "embed")
}

func modelContains(m Model, needle string) bool {
	return strings.Contains(strings.ToLower(m.ID), needle) || strings.Contains(strings.ToLower(m.Name), needle)
}

// smallestContextWindow returns the smallest positive value, or 0. Unlike
// pickContextWindow, which chooses between alternative spellings of one number,
// this reconciles two numbers that can genuinely differ.
func smallestContextWindow(values ...int) int {
	best := 0
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if best == 0 || v < best {
			best = v
		}
	}
	return best
}

// pickContextWindow takes the first positive value: providers report the window
// under different keys and only ever populate one of them.
func pickContextWindow(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func parseModelsResponse(body []byte) ([]Model, error) {
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		var arr []json.RawMessage
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return nil, fmt.Errorf("decode models response: %w", err)
		}
		return modelsFromRaw(arr), nil
	}

	raw := payload.Data
	if len(raw) == 0 {
		raw = payload.Models
	}
	return modelsFromRaw(raw), nil
}

func modelsFromRaw(raw []json.RawMessage) []Model {
	out := make([]Model, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		var row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			// display_name is Anthropic's, the only readable name it gives.
			DisplayName  string `json:"display_name"`
			Model        string `json:"model"`
			Capabilities *struct {
				CompletionChat bool `json:"completion_chat"`
				OCR            bool `json:"ocr"`
			} `json:"capabilities"`
			Architecture *struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			// context_length is OpenRouter's; max_context_length is Mistral's;
			// max_input_tokens is Anthropic's.
			ContextLength    int `json:"context_length"`
			MaxContextLength int `json:"max_context_length"`
			MaxInputTokens   int `json:"max_input_tokens"`
			TopProvider      *struct {
				ContextLength int `json:"context_length"`
			} `json:"top_provider"`
		}
		if json.Unmarshal(item, &row) != nil {
			continue
		}
		id := strings.TrimSpace(row.ID)
		if id == "" {
			id = strings.TrimSpace(row.Model)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = strings.TrimSpace(row.DisplayName)
		}
		if name == "" {
			name = id
		}
		// context_length and max_context_length are the same number under two
		// spellings (OpenRouter, Mistral), so the first positive one wins.
		contextWindow := pickContextWindow(row.ContextLength, row.MaxContextLength, row.MaxInputTokens)
		if row.TopProvider != nil {
			// top_provider.context_length is the window of the provider a request
			// is actually routed to, which can be smaller than the model's
			// advertised maximum. Overshooting it fails the completion mid-run.
			contextWindow = smallestContextWindow(contextWindow, row.TopProvider.ContextLength)
		}

		m := Model{ID: id, Name: name, ContextWindow: contextWindow}
		if row.Capabilities != nil {
			m.caps = &modelCapabilities{
				completionChat: row.Capabilities.CompletionChat,
				ocr:            row.Capabilities.OCR,
			}
		}
		if row.Architecture != nil {
			m.outputModalities = row.Architecture.OutputModalities
		}
		out = append(out, m)
	}
	return out
}
