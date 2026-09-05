package ai

import (
	"errors"
	"strings"
	"sync"

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

func mentionsReasoningEffortToolConflict(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "reasoning_effort") && strings.Contains(msg, "function tools")
}

// Models that have already refused tools alongside their default
// reasoning_effort. Remembering them keeps the cost of the discovery at one
// rejected request per model per process, rather than one per agent round —
// the research loop is uncapped, so paying it every round adds up.
var noReasoningEffortModels sync.Map

func rememberNoReasoningEffort(model string) {
	if key := modelKey(model); key != "" {
		noReasoningEffortModels.Store(key, struct{}{})
	}
}

func needsNoReasoningEffort(model string) bool {
	key := modelKey(model)
	if key == "" {
		return false
	}
	_, ok := noReasoningEffortModels.Load(key)
	return ok
}

// resetNoReasoningEffort clears what the process has learned. Tests only.
func resetNoReasoningEffort() {
	noReasoningEffortModels.Range(func(k, _ any) bool {
		noReasoningEffortModels.Delete(k)
		return true
	})
}

// modelKey normalises a configured model string for the two per-model notes
// this package keeps (see also responses.go).
func modelKey(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
