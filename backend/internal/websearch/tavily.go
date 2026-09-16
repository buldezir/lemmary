// Package websearch reaches the public web on the user's behalf, backing the
// web_search and web_fetch tools in Deep Research and Ask AI.
//
// Fetching is Tavily's job as well as searching: /extract runs on their network,
// so a URL the model invents -- or a user pastes -- is never dialled from this
// process. That is why there is no SSRF guard here and no HTML parser.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/strutil"
)

const (
	// maxResponseBytes bounds a response from an address an admin can change.
	// Generous: an /extract batch of long pages is legitimately megabytes.
	maxResponseBytes = 8 << 20

	// maxPageBytes is how much of one extracted page reaches the model. The
	// whole batch becomes a single tool message, so this is what keeps a
	// five-URL fetch from filling the context window on its own.
	maxPageBytes = 20000

	defaultTimeout = 30 * time.Second
)

// Result is one search hit. Snippet is Tavily's own extract of the passages
// that matched, not the page: reading the page is what Fetch is for.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Page is one extracted page. Error is set instead of Content for a URL Tavily
// could not read, so a partly-failed batch still reports per URL rather than
// failing whole.
type Page struct {
	URL     string
	Content string
	Error   string
}

type Tavily struct {
	apiKey  string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

func NewTavily(apiKey, baseURL string, timeout time.Duration, logger *slog.Logger) *Tavily {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Tavily{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

type searchRequest struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	SearchDepth string `json:"search_depth"`
	Topic       string `json:"topic"`
}

type searchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

type extractRequest struct {
	URLs         []string `json:"urls"`
	Format       string   `json:"format"`
	ExtractDepth string   `json:"extract_depth"`
}

type extractResponse struct {
	Results []struct {
		URL        string `json:"url"`
		RawContent string `json:"raw_content"`
	} `json:"results"`
	FailedResults []struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	} `json:"failed_results"`
}

// apiError is Tavily's error envelope. Their `detail` is an object on a
// validation error and a bare string on some others, so it is decoded loosely
// and the raw body is the fallback.
type apiError struct {
	Detail json.RawMessage `json:"detail"`
}

func (c *Tavily) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("a search query is required")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	var out searchResponse
	err := c.post(ctx, "/search", searchRequest{
		Query:       query,
		MaxResults:  maxResults,
		SearchDepth: "basic",
		Topic:       "general",
	}, &out, "query_chars", len(query), "max_results", maxResults)
	if err != nil {
		return nil, fmt.Errorf("tavily search: %w", err)
	}

	results := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		url := strings.TrimSpace(r.URL)
		if url == "" {
			continue
		}
		results = append(results, Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     url,
			Snippet: strings.TrimSpace(r.Content),
		})
	}
	return results, nil
}

func (c *Tavily) Fetch(ctx context.Context, urls []string) ([]Page, error) {
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			cleaned = append(cleaned, u)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("at least one url is required")
	}

	var out extractResponse
	err := c.post(ctx, "/extract", extractRequest{
		URLs:         cleaned,
		Format:       "markdown",
		ExtractDepth: "basic",
	}, &out, "urls", len(cleaned))
	if err != nil {
		return nil, fmt.Errorf("tavily extract: %w", err)
	}

	pages := make([]Page, 0, len(cleaned))
	for _, r := range out.Results {
		pages = append(pages, Page{
			URL:     strings.TrimSpace(r.URL),
			Content: strutil.Truncate(strings.TrimSpace(r.RawContent), maxPageBytes),
		})
	}
	for _, f := range out.FailedResults {
		message := strings.TrimSpace(f.Error)
		if message == "" {
			message = "could not be read"
		}
		pages = append(pages, Page{URL: strings.TrimSpace(f.URL), Error: message})
	}
	return pages, nil
}

func (c *Tavily) post(ctx context.Context, path string, body, out any, logExtra ...any) error {
	if c.apiKey == "" {
		return fmt.Errorf("API key is not configured")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.url(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", aiprovider.UserAgent)

	aiprovider.LogRequest(c.logger, aiprovider.SDKTavily, http.MethodPost, url, "", logExtra...)
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	c.logger.Info("web search response",
		"status", resp.StatusCode,
		"bytes", len(data),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errorMessage(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Tavily) url(path string) string {
	base := c.baseURL
	if base == "" {
		base = aiprovider.DefaultBaseURL(aiprovider.SDKTavily)
	}
	return base + path
}

func errorMessage(body []byte) string {
	var env apiError
	if json.Unmarshal(body, &env) == nil && len(env.Detail) > 0 {
		var text string
		if json.Unmarshal(env.Detail, &text) == nil && strings.TrimSpace(text) != "" {
			return text
		}
		var object struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(env.Detail, &object) == nil && strings.TrimSpace(object.Error) != "" {
			return object.Error
		}
	}
	return strutil.Truncate(strings.TrimSpace(string(body)), 500)
}
