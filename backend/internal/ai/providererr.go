package ai

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go"

	"lemmary/backend/internal/strutil"
)

// maxProviderErrorRunes bounds what a provider's own words may take up in a
// message meant for a person. Generous enough for a sentence about a context
// window or a quota, short of a provider that echoes the request back.
const maxProviderErrorRunes = 400

// Credentials and addresses providers echo back in error text. api_key is the
// operator's, not the reader's, and the base URL is of no use to someone who
// cannot change it; both would otherwise reach a user-visible message.
var providerSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+\S+`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`https?://\S+`),
}

// ProviderErrorMessage renders a failed completion as something a user can act
// on. The provider's own words are what distinguish a context window from a
// spent quota from a wrong key, and they used to be dropped in favour of one
// sentence that sent every reader to check the same healthy configuration.
func ProviderErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		detail := redactProviderSecrets(apiErr.Message)
		if detail == "" {
			detail = redactProviderSecrets(apiErr.Error())
		}
		if apiErr.StatusCode > 0 {
			return fmt.Sprintf("Provider error (%d): %s", apiErr.StatusCode, detail)
		}
		return "Provider error: " + detail
	}

	return "Provider error: " + redactProviderSecrets(err.Error())
}

func redactProviderSecrets(text string) string {
	text = strings.TrimSpace(text)
	for _, pattern := range providerSecretPatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	text = strings.Join(strings.Fields(text), " ")
	return strutil.TruncateRunes(text, maxProviderErrorRunes)
}
