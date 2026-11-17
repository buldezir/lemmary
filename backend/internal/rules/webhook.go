package rules

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	WebhookEvent   = "rule.matched"
	WebhookTimeout = 10 * time.Second
	// SignatureHeader carries "sha256=" and the hex HMAC-SHA256 of the body
	// under the action's secret, when it has one.
	SignatureHeader = "X-Lemmary-Signature"
)

type WebhookPayload struct {
	Event    string          `json:"event"`
	Trigger  string          `json:"trigger"`
	Rule     WebhookRule     `json:"rule"`
	Document WebhookDocument `json:"document"`
}

type WebhookRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// WebhookDocument is the document as the rules left it, names rather than ids.
type WebhookDocument struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	Summary          string         `json:"summary"`
	DocumentDate     string         `json:"document_date"`
	DocumentType     string         `json:"document_type"`
	Correspondent    string         `json:"correspondent"`
	Tags             []string       `json:"tags"`
	Fields           map[string]any `json:"fields"`
	ProcessingStatus string         `json:"processing_status"`
}

// Deliver POSTs body to rawURL and wants a 2xx back. The error names the host
// only: a URL often carries a token in its path or query.
func Deliver(ctx context.Context, client *http.Client, rawURL, secret string, body []byte) error {
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Host
	}
	ctx, cancel := context.WithTimeout(ctx, WebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook to %s: %w", host, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Lemmary-Webhook")
	req.Header.Set("X-Lemmary-Event", WebhookEvent)
	if secret != "" {
		req.Header.Set(SignatureHeader, Sign(secret, body))
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error quotes the whole URL.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("webhook to %s: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook to %s answered %d", host, resp.StatusCode)
	}
	return nil
}

func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
