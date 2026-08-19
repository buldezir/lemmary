package applog

import (
	"context"
	"log/slog"

	pblogger "github.com/pocketbase/pocketbase/tools/logger"
)

// teeHandler writes records to a console handler when they meet that
// handler's min level, and forwards to inner only when inner.Enabled.
type teeHandler struct {
	console slog.Handler
	inner   slog.Handler
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.console.Enabled(ctx, level) || h.inner.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var first error
	if h.console.Enabled(ctx, r.Level) {
		first = h.console.Handle(ctx, r)
	}
	if h.inner.Enabled(ctx, r.Level) {
		if err := h.inner.Handle(ctx, r); err != nil && first == nil {
			return err
		}
	}
	return first
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{
		console: h.console.WithAttrs(attrs),
		inner:   h.inner.WithAttrs(attrs),
	}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &teeHandler{
		console: h.console.WithGroup(name),
		inner:   h.inner.WithGroup(name),
	}
}

func innerBatchHandler(h slog.Handler) *pblogger.BatchHandler {
	for h != nil {
		switch t := h.(type) {
		case *teeHandler:
			h = t.inner
		case *pblogger.BatchHandler:
			return t
		default:
			return nil
		}
	}
	return nil
}
