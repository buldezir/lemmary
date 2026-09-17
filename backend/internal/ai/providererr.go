package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"

	"lemmary/backend/internal/strutil"
)

// maxProviderErrorRunes bounds what a provider's own words may take up in a
// message meant for a person. Generous enough for a sentence about a context
// window or a quota, short of a provider that echoes the request back.
const maxProviderErrorRunes = 400

// Credentials and addresses providers echo back in error text. api_key is the
// operator's, not the reader's, and the base URL is of no use to someone who
// cannot change it; both would otherwise reach a user-visible message.
//
// The last three are for the errors that never reach an API type at all: a
// transport failure names the address it could not reach, so "dial tcp
// 10.0.0.5:11434: connect: connection refused" would otherwise tell every user
// where the operator's model runs. A host is only redacted when it carries a
// port, which is what a dial error has and what prose mentioning a vendor does
// not.
var providerSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+\S+`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`https?://\S+`),
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`),
	regexp.MustCompile(`\[[0-9a-fA-F:]+\](?::\d{1,5})?`),
	regexp.MustCompile(`(?i)\b(?:localhost|[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9-]+)+):\d{1,5}\b`),
}

// ProviderErrorMessage renders a failed completion as something a user can act
// on. The provider's own words are what distinguish a context window from a
// spent quota from a wrong key, and they used to be dropped in favour of one
// sentence that sent every reader to check the same healthy configuration.
func ProviderErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	// Asked before the OpenAI one because the Messages API is a different SDK
	// with a different error type, and without this an Anthropic failure loses
	// its status code and reads as a bare Go error.
	var messagesErr *anthropic.Error
	if errors.As(err, &messagesErr) {
		return providerErrorf(messagesErr.StatusCode, messagesErrorDetail(messagesErr))
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		detail := redactProviderSecrets(apiErr.Message)
		if detail == "" {
			detail = redactProviderSecrets(apiErr.Error())
		}
		return providerErrorf(apiErr.StatusCode, detail)
	}

	return "Provider error: " + redactProviderSecrets(err.Error())
}

func providerErrorf(status int, detail string) string {
	if status > 0 {
		return fmt.Sprintf("Provider error (%d): %s", status, detail)
	}
	return "Provider error: " + detail
}

// messagesErrorDetail digs the sentence out of an Anthropic failure. That SDK
// parses no message field, only the body it arrived in, which is
// {"type":"error","error":{"type":...,"message":...}}. The error type is the
// fallback because a body that did not parse is still better named than not
// named, and Error() itself is the last resort: it carries the request line.
func messagesErrorDetail(err *anthropic.Error) string {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(err.RawJSON()), &envelope) == nil {
		if detail := redactProviderSecrets(envelope.Error.Message); detail != "" {
			return detail
		}
		if detail := redactProviderSecrets(envelope.Error.Type); detail != "" {
			return detail
		}
	}
	return redactProviderSecrets(err.Error())
}

func redactProviderSecrets(text string) string {
	text = strings.TrimSpace(text)
	for _, pattern := range providerSecretPatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	text = strings.Join(strings.Fields(text), " ")
	return strutil.TruncateRunes(text, maxProviderErrorRunes)
}
