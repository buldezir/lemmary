package ngximport

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const defaultPageSize = 100

// Client talks to a remote Paperless-ngx REST API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		baseURL:    normalized,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpClient,
	}, nil
}

// NormalizeBaseURL trims trailing slashes and an optional /api suffix.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("url must include a host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.Path = strings.TrimSuffix(u.Path, "/api")
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

type namedEntity struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ngxDocument struct {
	ID                 int    `json:"id"`
	Title              string `json:"title"`
	Content            string `json:"content"`
	Tags               []int  `json:"tags"`
	DocumentType       *int   `json:"document_type"`
	Correspondent      *int   `json:"correspondent"`
	Created            string `json:"created"`
	CreatedDate        string `json:"created_date"`
	OriginalFileName   string `json:"original_file_name"`
	ArchivedFileName   string `json:"archived_file_name"`
}

type page[T any] struct {
	Count    int     `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []T     `json:"results"`
}

func (c *Client) ListTags() ([]namedEntity, error) {
	return listAll[namedEntity](c, "/api/tags/")
}

func (c *Client) ListCorrespondents() ([]namedEntity, error) {
	return listAll[namedEntity](c, "/api/correspondents/")
}

func (c *Client) ListDocumentTypes() ([]namedEntity, error) {
	return listAll[namedEntity](c, "/api/document_types/")
}

func (c *Client) ListDocuments() ([]ngxDocument, error) {
	return listAll[ngxDocument](c, "/api/documents/")
}

type downloadedFile struct {
	Name string
	Data []byte
}

func (c *Client) DownloadDocument(id int) (downloadedFile, error) {
	rel := fmt.Sprintf("/api/documents/%d/download/?original=true", id)
	req, err := c.newRequest(http.MethodGet, rel, nil)
	if err != nil {
		return downloadedFile{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return downloadedFile{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return downloadedFile{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return downloadedFile{}, fmt.Errorf("download document %d: status %d: %s", id, resp.StatusCode, truncate(string(body), 200))
	}
	name := filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = fmt.Sprintf("document-%d.bin", id)
	}
	return downloadedFile{Name: name, Data: body}, nil
}

func listAll[T any](c *Client, apiPath string) ([]T, error) {
	var all []T
	next := apiPath
	if !strings.Contains(next, "?") {
		next += "?page=1&page_size=" + strconv.Itoa(defaultPageSize)
	}
	for next != "" {
		var p page[T]
		if err := c.getJSON(next, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Results...)
		if p.Next == nil || strings.TrimSpace(*p.Next) == "" {
			break
		}
		nextURL := strings.TrimSpace(*p.Next)
		if strings.HasPrefix(nextURL, "http://") || strings.HasPrefix(nextURL, "https://") {
			u, err := url.Parse(nextURL)
			if err != nil {
				return nil, fmt.Errorf("parse next url: %w", err)
			}
			next = u.RequestURI()
		} else {
			next = nextURL
		}
	}
	return all, nil
}

func (c *Client) getJSON(relOrPath string, dest any) error {
	req, err := c.newRequest(http.MethodGet, relOrPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: status %d: %s", relOrPath, resp.StatusCode, truncate(string(body), 200))
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode %s: %w", relOrPath, err)
	}
	return nil
}

func (c *Client) newRequest(method, relOrPath string, body io.Reader) (*http.Request, error) {
	full, err := joinURL(c.baseURL, relOrPath)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, full, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Accept", "application/json; version=9")
	return req, nil
}

func joinURL(base, relOrPath string) (string, error) {
	if strings.HasPrefix(relOrPath, "http://") || strings.HasPrefix(relOrPath, "https://") {
		return relOrPath, nil
	}
	if _, err := url.Parse(base); err != nil {
		return "", err
	}
	if _, err := url.Parse(relOrPath); err != nil {
		return "", err
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(relOrPath, "/") {
		relOrPath = "/" + relOrPath
	}
	return base + relOrPath, nil
}

func filenameFromDisposition(header string) string {
	if header == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(params["filename"]); name != "" {
		return path.Base(name)
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
