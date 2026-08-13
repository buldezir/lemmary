package aiprovider

import (
	"log/slog"
	"strings"
)

// LogRequest writes an INFO line for an outbound AI HTTP/SDK call.
func LogRequest(logger *slog.Logger, sdk, method, url, model string, extra ...any) {
	if logger == nil {
		return
	}
	attrs := []any{"sdk", sdk, "method", method, "url", url}
	if strings.TrimSpace(model) != "" {
		attrs = append(attrs, "model", model)
	}
	logger.Info("ai request", append(attrs, extra...)...)
}

func ChatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL(SDKOpenAI)
	}
	return base + "/chat/completions"
}
