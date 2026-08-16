package aiprovider

import "testing"

func TestAllowsCustomTemperature(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-5.6-luna", false},
		{"gpt-4.1", true},
		{"openai/gpt-5.6-luna", false},
		{"mistral-small-latest", true},
		{"gpt-5", false},
		{"gpt-5-mini", false},
		{"gpt-5-nano", false},
		{"GPT-5-mini", false},
		{"openai/gpt-5-mini", false},
		{"gpt-5.2-chat-latest", false},
		{"ft:gpt-5-mini:org:custom:abc", false},
		{"o1", false},
		{"o1-mini", false},
		{"o3-mini", false},
		{"openai/o4-mini", false},
		{"o10-experimental", true},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			if got := AllowsCustomTemperature(tc.model); got != tc.want {
				t.Fatalf("AllowsCustomTemperature(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}
