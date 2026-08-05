package mail

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	ModeSimple = "simple"
	ModeDeep   = "deep"

	TriggerManual = "manual"
	TriggerCron   = "cron"

	ScanPending   = "pending"
	ScanRunning   = "running"
	ScanCompleted = "completed"
	ScanFailed    = "failed"

	gmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"

	defaultSimpleBatch = 20
	defaultDeepBatch   = 8
	defaultMaxMessages = 500
	defaultBodyChars   = 4000
)

// AttachmentMeta describes a Gmail attachment part.
type AttachmentMeta struct {
	ID       string
	Filename string
	MimeType string
	Size     int64
}

// Message is a normalized mail message for triage.
type Message struct {
	ID          string
	Subject     string
	From        string
	Date        string
	BodyText    string
	Attachments []AttachmentMeta
}

// Client abstracts Gmail API access (real or fake).
type Client interface {
	ListMessageIDs(ctx context.Context, query string, max int) ([]string, error)
	GetMessage(ctx context.Context, id string, mode string) (*Message, error)
	DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error)
}

// ClientFactory builds a Gmail client from a refresh token.
type ClientFactory func(app core.App, refreshToken string) (Client, error)

var defaultClientFactory ClientFactory = NewGmailClient

// SetClientFactory overrides the Gmail client factory (tests).
func SetClientFactory(f ClientFactory) {
	if f == nil {
		defaultClientFactory = NewGmailClient
		return
	}
	defaultClientFactory = f
}

// GoogleOAuthCredentials reads the Google provider client ID/secret from the users collection.
func GoogleOAuthCredentials(app core.App) (clientID, clientSecret string, err error) {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return "", "", fmt.Errorf("users collection: %w", err)
	}
	cfg, ok := users.OAuth2.GetProviderConfig("google")
	if !ok || strings.TrimSpace(cfg.ClientId) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return "", "", fmt.Errorf("Google OAuth2 is not configured on the users collection")
	}
	return strings.TrimSpace(cfg.ClientId), strings.TrimSpace(cfg.ClientSecret), nil
}

// GoogleOAuthConfigured reports whether Google OAuth is ready for Gmail linking.
func GoogleOAuthConfigured(app core.App) bool {
	_, _, err := GoogleOAuthCredentials(app)
	return err == nil
}

func oauthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmailReadonlyScope},
	}
}

// NewGmailClient creates a real Gmail API client from a stored refresh token.
func NewGmailClient(app core.App, refreshToken string) (Client, error) {
	clientID, clientSecret, err := GoogleOAuthCredentials(app)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("missing refresh token")
	}
	cfg := oauthConfig(clientID, clientSecret)
	token := &oauth2.Token{RefreshToken: refreshToken}
	httpClient := cfg.Client(context.Background(), token)
	svc, err := gmail.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gmail service: %w", err)
	}
	return &gmailClient{svc: svc}, nil
}

type gmailClient struct {
	svc *gmail.Service
}

func (c *gmailClient) ListMessageIDs(ctx context.Context, query string, max int) ([]string, error) {
	if max <= 0 {
		max = defaultMaxMessages
	}
	var ids []string
	pageToken := ""
	for len(ids) < max {
		call := c.svc.Users.Messages.List("me").Q(query).MaxResults(int64(min(100, max-len(ids))))
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		call = call.Context(ctx)
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("gmail list: %w", err)
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
			if len(ids) >= max {
				break
			}
		}
		if resp.NextPageToken == "" || len(resp.Messages) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return ids, nil
}

func (c *gmailClient) GetMessage(ctx context.Context, id string, mode string) (*Message, error) {
	// format=full is required so attachment IDs are present; body text is only kept in deep mode.
	raw, err := c.svc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail get %s: %w", id, err)
	}
	msg := &Message{ID: raw.Id}
	if raw.Payload != nil {
		for _, h := range raw.Payload.Headers {
			switch strings.ToLower(h.Name) {
			case "subject":
				msg.Subject = h.Value
			case "from":
				msg.From = h.Value
			case "date":
				msg.Date = h.Value
			}
		}
		msg.Attachments = collectAttachments(raw.Payload)
		if mode == ModeDeep {
			msg.BodyText = truncateRunes(extractBodyText(raw.Payload), defaultBodyChars)
		}
	}
	return msg, nil
}

func (c *gmailClient) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	resp, err := c.svc.Users.Messages.Attachments.Get("me", messageID, attachmentID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail attachment: %w", err)
	}
	data, err := decodeGmailBase64(resp.Data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// BuildDateQuery builds a Gmail search for messages with attachments in [from, to] (inclusive dates YYYY-MM-DD).
func BuildDateQuery(dateFrom, dateTo string) (string, error) {
	from, err := time.Parse("2006-01-02", dateFrom)
	if err != nil {
		return "", fmt.Errorf("invalid date_from: %w", err)
	}
	to, err := time.Parse("2006-01-02", dateTo)
	if err != nil {
		return "", fmt.Errorf("invalid date_to: %w", err)
	}
	if to.Before(from) {
		return "", fmt.Errorf("date_to must be on or after date_from")
	}
	// Gmail before: is exclusive; use day after date_to.
	before := to.AddDate(0, 0, 1)
	return fmt.Sprintf(
		"has:attachment after:%d/%d/%d before:%d/%d/%d",
		from.Year(), int(from.Month()), from.Day(),
		before.Year(), int(before.Month()), before.Day(),
	), nil
}

func collectAttachments(part *gmail.MessagePart) []AttachmentMeta {
	if part == nil {
		return nil
	}
	var out []AttachmentMeta
	var walk func(*gmail.MessagePart)
	walk = func(p *gmail.MessagePart) {
		if p == nil {
			return
		}
		filename := strings.TrimSpace(p.Filename)
		if filename != "" && p.Body != nil && p.Body.AttachmentId != "" {
			out = append(out, AttachmentMeta{
				ID:       p.Body.AttachmentId,
				Filename: filename,
				MimeType: p.MimeType,
				Size:     p.Body.Size,
			})
		}
		for _, child := range p.Parts {
			walk(child)
		}
	}
	walk(part)
	return out
}

func extractBodyText(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}
	var plain, html string
	var walk func(*gmail.MessagePart)
	walk = func(p *gmail.MessagePart) {
		if p == nil {
			return
		}
		mime := strings.ToLower(p.MimeType)
		if p.Body != nil && p.Body.Data != "" && p.Filename == "" {
			decoded, err := decodeGmailBase64(p.Body.Data)
			if err == nil {
				text := string(decoded)
				switch {
				case strings.HasPrefix(mime, "text/plain") && plain == "":
					plain = text
				case strings.HasPrefix(mime, "text/html") && html == "":
					html = text
				}
			}
		}
		for _, child := range p.Parts {
			walk(child)
		}
	}
	walk(part)
	if plain != "" {
		return plain
	}
	return stripHTML(html)
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func decodeGmailBase64(data string) ([]byte, error) {
	// Gmail uses URL-safe base64 without padding.
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(data)
	}
	if err != nil {
		return nil, fmt.Errorf("decode attachment: %w", err)
	}
	return decoded, nil
}
