package aiprovider

import "strings"

// AllowsCustomTemperature reports whether an OpenAI-compatible chat model
// accepts a non-default temperature. GPT-5 and o-series reasoning models
// only allow the API default (1); sending 0.1 causes a 400.
func AllowsCustomTemperature(model string) bool {
	name := canonicalChatModel(model)
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, "gpt-5") {
		return false
	}
	return !isOSeriesReasoningModel(name)
}

func canonicalChatModel(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if strings.HasPrefix(name, "ft:") {
		parts := strings.Split(name, ":")
		if len(parts) >= 2 {
			name = parts[1]
		}
	}
	return name
}

func isOSeriesReasoningModel(name string) bool {
	if len(name) < 2 || name[0] != 'o' || name[1] < '1' || name[1] > '9' {
		return false
	}
	return len(name) == 2 || name[2] == '-' || name[2] == '.'
}
