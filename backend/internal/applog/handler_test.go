package applog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type recordingHandler struct {
	level    slog.Level
	messages []string
}

func (h *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.messages = append(h.messages, r.Message)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func TestTeeHandlerFiltersConsoleAndInnerIndependently(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	inner := &recordingHandler{level: slog.LevelInfo}
	h := &teeHandler{
		console: slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		inner:   inner,
	}
	logger := slog.New(h)

	logger.Debug("debug-only")
	logger.Info("info-both")

	if len(inner.messages) != 1 || inner.messages[0] != "info-both" {
		t.Fatalf("inner messages = %v, want [info-both]", inner.messages)
	}

	out := stdout.String()
	if !strings.Contains(out, `"msg":"debug-only"`) {
		t.Fatalf("stdout missing debug-only: %s", out)
	}
	if !strings.Contains(out, `"msg":"info-both"`) {
		t.Fatalf("stdout missing info-both: %s", out)
	}

	var n int
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	for {
		var rec map[string]any
		err := dec.Decode(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode stdout json: %v", err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("stdout json records = %d, want 2", n)
	}
}
