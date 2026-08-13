package aiprovider

import "testing"

func TestLogRequestNilLogger(t *testing.T) {
	t.Parallel()
	LogRequest(nil, SDKOpenAI, "POST", "https://example.test/v1/chat/completions", "m", "purpose", "chat")
}
