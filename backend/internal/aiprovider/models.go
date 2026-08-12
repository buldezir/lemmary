package aiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

func ListModels(ctx context.Context, p Provider, forOCR bool, client *http.Client) ([]Model, error) {
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
	return models, nil
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
			ID    string `json:"id"`
			Name  string `json:"name"`
			Model string `json:"model"`
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
		out = append(out, Model{ID: id, Name: name})
	}
	return out
}
