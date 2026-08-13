package ai

import "strings"

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
