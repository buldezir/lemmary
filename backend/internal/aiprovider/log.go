package aiprovider

import (
	"log/slog"
	"strings"
)

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
