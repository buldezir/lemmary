package worker

import (
	"strings"
	"time"
)

const pbTimeLayout = "2006-01-02 15:04:05.000Z"

func nowTimestamp() string {
	return time.Now().UTC().Format(pbTimeLayout)
}

func truncateError(msg string, max int) string {
	if len(msg) <= max {
		return msg
	}
	return msg[:max]
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
