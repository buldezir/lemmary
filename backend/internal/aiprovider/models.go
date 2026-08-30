package aiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
}

type modelCapabilities struct {
	completionChat bool
	ocr            bool
}

const modelsListTimeout = 20 * time.Second

func ModelsURL(p Provider, forOCR bool) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return ""
	}
	endpoint := base + "/models"
	if forOCR && p.SDK == SDKOpenRouter {
		u, err := url.Parse(endpoint)
		if err != nil {
			return endpoint + "?input_modalities=file"
		}
		q := u.Query()
		q.Set("input_modalities", "file")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return endpoint
}

func ListModels(ctx context.Context, p Provider, forOCR bool, client *http.Client, logger *slog.Logger) ([]Model, error) {
	if p.SDK == SDKGoogleVision {
		return nil, nil
	}
	endpoint := ModelsURL(p, forOCR)
	if endpoint == "" {
		return nil, fmt.Errorf("provider has no base URL")
	}
	if p.APIKey == "" {
		return nil, fmt.Errorf("provider API key is not set")
	}

	if client == nil {
		client = &http.Client{Timeout: modelsListTimeout}
	}

	LogRequest(logger, p.SDK, http.MethodGet, endpoint, "", "for_ocr", forOCR)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Accept", "application/json")

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

	models, err := parseModelsResponse(body)
	if err != nil {
		return nil, err
	}
	if p.SDK == SDKMistral {
		models = filterMistralModels(models, forOCR)
	}
	return models, nil
}

func filterMistralModels(models []Model, forOCR bool) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if includeMistralModel(m, forOCR) {
			out = append(out, m)
		}
	}
	return out
}

func includeMistralModel(m Model, forOCR bool) bool {
	if m.caps != nil {
		if forOCR {
			return m.caps.ocr
		}
		return m.caps.completionChat
	}
	if forOCR {
		return modelContains(m, "ocr")
	}
	return !modelContains(m, "ocr")
}

func modelContains(m Model, needle string) bool {
	return strings.Contains(strings.ToLower(m.ID), needle) || strings.Contains(strings.ToLower(m.Name), needle)
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
			ID           string `json:"id"`
			Name         string `json:"name"`
			Model        string `json:"model"`
			Capabilities *struct {
				CompletionChat bool `json:"completion_chat"`
				OCR            bool `json:"ocr"`
			} `json:"capabilities"`
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
		contextWindow := pickContextWindow(row.ContextLength, row.MaxContextLength)
		if row.TopProvider != nil {
			contextWindow = pickContextWindow(contextWindow, row.TopProvider.ContextLength)
		}

		m := Model{ID: id, Name: name, ContextWindow: contextWindow}
		if row.Capabilities != nil {
			m.caps = &modelCapabilities{
				completionChat: row.Capabilities.CompletionChat,
				ocr:            row.Capabilities.OCR,
			}
		}
		out = append(out, m)
	}
	return out
}
