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
	"sync"
	"time"
)

// DefaultCatalogURL is pi.dev's model catalogue, which answers
// /api/models/providers/{id} with a flat {modelID: {contextWindow, ...}} map.
// It is the only source here that knows a context window for a plain
// OpenAI-compatible endpoint: /v1/models does not report one.
const DefaultCatalogURL = "https://pi.dev/api/models/providers"

// CatalogProviders are pi.dev's provider ids, hardcoded rather than fetched
// from its index: they name the select field's options, and a list that has to
// be reachable before an admin can save a provider would make a third party's
// uptime part of ours.
var CatalogProviders = []string{
	"amazon-bedrock", "ant-ling", "anthropic", "azure-openai-responses", "baseten",
	"cerebras", "cloudflare-ai-gateway", "cloudflare-workers-ai", "deepseek",
	"fireworks", "github-copilot", "google", "google-vertex", "groq", "huggingface",
	"kimi-coding", "minimax", "minimax-cn", "mistral", "moonshotai", "moonshotai-cn",
	"nvidia", "openai", "openai-codex", "opencode", "opencode-go", "openrouter",
	"qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual", "together",
	"vercel-ai-gateway", "xai", "xiaomi", "xiaomi-token-plan-ams", "xiaomi-token-plan-cn",
	"xiaomi-token-plan-sgp", "zai", "zai-coding-cn",
}

func ValidCatalog(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	return slices.Contains(CatalogProviders, id)
}

// DefaultCatalog is the catalogue an SDK is most likely served by. Only a
// default: the openai SDK is any OpenAI-compatible endpoint, and a row whose
// base URL points at Groq or DeepSeek is a different catalogue entirely, which
// is why the field is editable.
func DefaultCatalog(sdk string) string {
	switch strings.TrimSpace(sdk) {
	case SDKOpenAI:
		return "openai"
	case SDKAnthropic:
		return "anthropic"
	case SDKOpenRouter:
		return "openrouter"
	case SDKMistral:
		return "mistral"
	case SDKOpenCode:
		return "opencode-go"
	case SDKChatGPT:
		return "openai-codex"
	default:
		return ""
	}
}

// ModelLimits is what the catalogue is consulted for.
type ModelLimits struct {
	ContextWindow int `json:"contextWindow"`
	MaxTokens     int `json:"maxTokens"`
}

const (
	catalogTTL     = 24 * time.Hour
	catalogRetryIn = 5 * time.Minute
	catalogTimeout = 10 * time.Second
)

type catalogEntry struct {
	models    map[string]ModelLimits
	expiresAt time.Time
}

// Catalog answers "how large is this model's context window", cached. Built
// once at startup rather than per settings snapshot, so a settings save does
// not throw the cache away.
type Catalog struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger

	mu      sync.Mutex
	entries map[string]catalogEntry
}

// NewCatalog builds the lookup. An empty baseURL turns it off: every window is
// then unknown, which costs the denominator on the usage line and nothing else.
func NewCatalog(baseURL string, logger *slog.Logger) *Catalog {
	if logger == nil {
		logger = slog.Default()
	}
	return &Catalog{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: catalogTimeout},
		logger:  logger,
		entries: map[string]catalogEntry{},
	}
}

// ContextWindow returns the model's context length in tokens, or 0 when it is
// not known: catalogue disabled, provider not tagged, host unreachable, or a
// model the catalogue does not list. Zero is never an error here; it means the
// usage line shows an absolute count without a denominator.
func (c *Catalog) ContextWindow(ctx context.Context, catalogID, model string) int {
	if c == nil {
		return 0
	}
	catalogID = strings.TrimSpace(catalogID)
	model = strings.TrimSpace(model)
	if c.baseURL == "" || catalogID == "" || model == "" {
		return 0
	}
	models := c.models(ctx, catalogID)
	if limits, ok := models[model]; ok {
		return limits.ContextWindow
	}
	return 0
}

func (c *Catalog) models(ctx context.Context, catalogID string) map[string]ModelLimits {
	c.mu.Lock()
	entry, ok := c.entries[catalogID]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.models
	}

	// Detached from the caller: this fills a process-wide cache, and a viewer
	// who hangs up mid-fetch would otherwise turn their own cancellation into a
	// failure cached for everyone. The client's own timeout still bounds it.
	models, err := c.fetch(context.WithoutCancel(ctx), catalogID)
	if err != nil {
		c.logger.Warn("model catalog fetch failed", "catalog", catalogID, slog.Any("error", err))
		return c.cacheFailure(catalogID)
	}

	c.mu.Lock()
	c.entries[catalogID] = catalogEntry{models: models, expiresAt: time.Now().Add(catalogTTL)}
	c.mu.Unlock()
	return models
}

// cacheFailure remembers that the catalogue is unreachable, so an unreachable
// host costs one request per few minutes rather than one per turn. It never
// replaces an answer that is still good: two lookups can miss at once, and the
// one that failed must not bury what the one that succeeded just filled in.
func (c *Catalog) cacheFailure(catalogID string) map[string]ModelLimits {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[catalogID]; ok && entry.models != nil && time.Now().Before(entry.expiresAt) {
		return entry.models
	}
	c.entries[catalogID] = catalogEntry{expiresAt: time.Now().Add(catalogRetryIn)}
	return nil
}

func (c *Catalog) fetch(ctx context.Context, catalogID string) (map[string]ModelLimits, error) {
	endpoint := c.baseURL + "/" + url.PathEscape(catalogID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("model catalog: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var models map[string]ModelLimits
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("model catalog: %w", err)
	}
	return models, nil
}
