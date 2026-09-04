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
	// reports one. OpenAI's /v1/models does not, so zero means "unknown" and
	// the configured default applies. Research mode reads documents until this
	// window is spent, which is why it is worth surfacing in Settings.
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

// ModelPurpose is the task a model is being picked for. It decides both which
// endpoint filter is asked for and which models are kept from the answer.
//
// It replaced a forOCR bool once embeddings arrived: the three lists are
// genuinely disjoint (an OCR model does not chat, an embedding model does
// neither), and a boolean could only ever express two of them.
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
	// A local endpoint has no /v1/models: text-embeddings-inference does not
	// document one, so a picker built on it would be empty or wrong depending
	// on the version. /info is in its OpenAPI spec and names the one model it
	// is serving.
	if p.SDK == SDKLocal {
		return InfoURL(base)
	}
	endpoint := base + "/models"
	// OpenRouter is the only provider that filters server-side. Two of its
	// filters matter here: input_modalities=file for OCR, and
	// output_modalities=embeddings for embedding models -- which the plain
	// catalogue leaves out altogether, so without the parameter the embedding
	// picker is empty for every OpenRouter user. The response is still run
	// through filterModels afterwards; the parameter is what makes the models
	// appear at all.
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

func ListModels(ctx context.Context, p Provider, purpose ModelPurpose, client *http.Client, logger *slog.Logger) ([]Model, error) {
	// An SDK that neither chats nor embeds has no catalogue to list: Google
	// Vision annotates without a model, and docling serves one pipeline.
	// Returning nothing here rather than falling through is what keeps the
	// checks below from turning a perfectly healthy keyless sidecar into a 502
	// and an error banner in Settings.
	//
	// It is not plain !IsLLM: the local embeddings sidecar is keyless too, but
	// it does name its one model, at TEI's /info rather than /v1/models.
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
	// Hand-rolled request, so the SDK middleware that stamps this everywhere
	// else does not see it.
	if SessionHost(req.URL.Host) {
		req.Header.Set(SessionHeader, SessionFor("models"))
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

	if p.SDK == SDKLocal {
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
// it is serving.
//
// model_type is the field that matters: TEI serves rerankers and classifiers
// from the same image and the same endpoint shape, and either one bound as an
// embedding model would fail on every document with nothing in the UI to
// explain why. An unrecognised type yields no models rather than a guess.
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

// filterModels keeps only the models that can serve purpose.
//
// Providers describe their catalogue very differently -- Mistral ships a
// capabilities object, OpenRouter ships modality lists, OpenAI ships nothing at
// all -- so the rules are per-SDK with a name heuristic underneath. The
// heuristic is deliberately one-sided: an embedding model must never appear in
// the LLM or OCR lists, because binding one there fails on every document,
// while a model missing from a list costs an admin one line of typing, which
// the Custom model id field exists for.
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
	// A local endpoint serves embeddings and nothing else, and the model it
	// serves is whatever the operator started it with -- "BAAI/bge-m3" carries
	// no "embed" for the name heuristic to find, so asking the heuristic here
	// would empty the one picker this SDK exists for.
	if sdk == SDKLocal {
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

// pickContextWindow takes the first positive value: providers report the window
// under different keys and only ever populate one of them.
// smallestContextWindow returns the smallest positive value, or 0 when none is
// positive. Unlike pickContextWindow, which chooses between alternative
// spellings of one number, this reconciles two numbers that can genuinely
// differ.
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
			ID           string `json:"id"`
			Name         string `json:"name"`
			Model        string `json:"model"`
			Capabilities *struct {
				CompletionChat bool `json:"completion_chat"`
				OCR            bool `json:"ocr"`
			} `json:"capabilities"`
			Architecture *struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			// context_length is OpenRouter's; max_context_length is Mistral's.
			ContextLength    int `json:"context_length"`
			MaxContextLength int `json:"max_context_length"`
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
			name = id
		}
		// context_length and max_context_length are the same number under two
		// spellings (OpenRouter, Mistral), so the first positive one wins.
		contextWindow := pickContextWindow(row.ContextLength, row.MaxContextLength)
		if row.TopProvider != nil {
			// top_provider.context_length is different in kind: the window of
			// the provider a request is actually routed to, which can be
			// smaller than the model's advertised maximum. Research spends this
			// number, so the smaller one is the only safe answer -- overshooting
			// it means the completion is rejected mid-run.
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
