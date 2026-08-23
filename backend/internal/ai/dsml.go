package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// DeepSeek V4 (and some proxies) emit tool calls as DSML markup in message
// content instead of OpenAI-native tool_calls. Tokens use U+FF5C (｜); some
// gateways decode them as ordinary "|".
var (
	dsmlSep      = `(?:｜|\|{1,3})`
	dsmlInvokeRe = regexp.MustCompile(
		`(?is)<\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*invoke\s+name="([^"]+)"\s*>(.*?)</\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*invoke\s*>`,
	)
	dsmlParamRe = regexp.MustCompile(
		`(?is)<\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*parameter\s+name="([^"]+)"(?:\s+string="([^"]*)")?\s*>(.*?)</\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*parameter\s*>`,
	)
	dsmlBlockRe = regexp.MustCompile(
		`(?is)<\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*(?:tool_calls|function_calls)\s*>.*?` +
			`</\s*` + dsmlSep + `\s*DSML\s*` + dsmlSep + `\s*(?:tool_calls|function_calls)\s*>`,
	)
	dsmlLooseMarkerRe = regexp.MustCompile(`(?i)<\s*(?:｜|\|{1,3})\s*DSML\s*(?:｜|\|{1,3})`)
)

type parsedToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON object
}

func contentHasDSMLToolCalls(content string) bool {
	return dsmlLooseMarkerRe.MatchString(content) && strings.Contains(strings.ToLower(content), "invoke")
}

func parseDSMLToolCalls(content string) []parsedToolCall {
	matches := dsmlInvokeRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	calls := make([]parsedToolCall, 0, len(matches))
	for i, match := range matches {
		name := strings.TrimSpace(match[1])
		body := match[2]
		if name == "" {
			continue
		}
		argsJSON := parseDSMLParameters(body)
		calls = append(calls, parsedToolCall{
			ID:        fmt.Sprintf("dsml_%d", i+1),
			Name:      name,
			Arguments: argsJSON,
		})
	}
	return calls
}

func parseDSMLParameters(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "{}"
	}

	// Direct JSON body inside invoke.
	if strings.HasPrefix(body, "{") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(body), &raw); err == nil {
			encoded, err := json.Marshal(raw)
			if err == nil {
				return string(encoded)
			}
		}
	}

	params := map[string]any{}
	for _, match := range dsmlParamRe.FindAllStringSubmatch(body, -1) {
		name := strings.TrimSpace(match[1])
		isString := strings.EqualFold(strings.TrimSpace(match[2]), "true")
		value := strings.TrimSpace(match[3])
		if name == "" {
			continue
		}
		if isString {
			params[name] = value
			continue
		}
		// No string= attribute, or string=false: try JSON first so numbers,
		// booleans, arrays, and objects all decode (an array value left as a
		// raw string would fail the tool-args unmarshal and drop the call).
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err == nil {
			params[name] = decoded
		} else {
			params[name] = value
		}
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func stripDSMLMarkup(content string) string {
	cleaned := dsmlBlockRe.ReplaceAllString(content, "")
	cleaned = dsmlInvokeRe.ReplaceAllString(cleaned, "")
	// Remove any leftover DSML open/close tags that didn't form complete blocks.
	cleaned = regexp.MustCompile(`(?is)</?\s*(?:｜|\|{1,3})\s*DSML\s*(?:｜|\|{1,3})\s*[^>]*>`).ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

func formatDSMLToolResults(results []toolExecResult) string {
	var b strings.Builder
	b.WriteString("Tool results are below. Now answer the user in natural language only. Do not call tools again. Do not output DSML.\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("\n<tool_result name=%q id=%q>\n%s\n</tool_result>\n", r.Name, r.ID, r.Content))
	}
	return b.String()
}

type toolExecResult struct {
	ID      string
	Name    string
	Content string
}
