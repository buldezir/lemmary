package ai

import (
	"errors"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// reasoningEffortNone is the only value some gpt-5-family models accept for
// reasoning_effort once function tools are on the request. It is not one of the
// SDK's low/medium/high constants, but shared.ReasoningEffort is a string type.
const reasoningEffortNone = "none"

// isReasoningEffortToolConflictError recognises a provider rejecting a
// non-"none" reasoning_effort alongside function tools on /v1/chat/completions.
// We never send the parameter ourselves; these models default it server-side
// and then refuse the request because tools are present.
func isReasoningEffortToolConflictError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// The param alone is enough; the wording of the message varies by
		// provider, and OpenAI-compatible proxies often leave it out entirely.
		if strings.EqualFold(strings.TrimSpace(apiErr.Param), "reasoning_effort") {
			return true
		}
		return mentionsReasoningEffortToolConflict(apiErr.Message)
	}
	return mentionsReasoningEffortToolConflict(err.Error())
}

// isEffortUnsupportedError recognises the Messages API refusing
// output_config.effort, which only Claude 4.5 and later accept. The SDK gives
// no parsed message field, so the raw error body is what there is to read; the
// parameter name is distinctive enough on its own.
func isEffortUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(apiErr.RawJSON())
	return strings.Contains(body, "output_config") || strings.Contains(body, "effort")
}

func mentionsReasoningEffortToolConflict(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "reasoning_effort") && strings.Contains(msg, "function tools")
}
