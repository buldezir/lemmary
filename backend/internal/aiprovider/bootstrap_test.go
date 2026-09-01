package aiprovider

import "testing"

// The pure half of Apply's decision: which provider serves OCR.
//
// Worth locking down separately from the parser, because the two disagreed once
// and the disagreement was invisible. A ProviderSpec naming an SDK with no key
// is rejected by config.parseOCR before it can reach Apply, but nothing in this
// package enforces that — so the resolution here must be driven by whether an
// SDK was named, never by whether it happens to carry a credential.
func TestOCRResolutionFollowsTheSDK(t *testing.T) {
	llm := ProviderSpec{SDK: SDKOpenAI, APIKey: "sk", Model: "llm-model"}

	cases := []struct {
		name               string
		ocr                ProviderSpec
		wantShares         bool
		wantSDK, wantModel string
	}{
		{
			name:       "no OCR spec at all",
			ocr:        ProviderSpec{},
			wantShares: true, wantSDK: SDKOpenAI, wantModel: "llm-model",
		},
		{
			name:       "the same SDK, its own model",
			ocr:        ProviderSpec{SDK: SDKOpenAI, APIKey: "sk", Model: "ocr-model"},
			wantShares: true, wantSDK: SDKOpenAI, wantModel: "ocr-model",
		},
		{
			name:       "a different SDK with a key of its own",
			ocr:        ProviderSpec{SDK: SDKGoogleVision, APIKey: "vision"},
			wantShares: false, wantSDK: SDKGoogleVision, wantModel: "",
		},
		{
			// The regression. An SDK named without a key used to read as "no OCR
			// asked for", so OCR bound to the language model and the operator
			// paid the LLM to read every page.
			name:       "a different SDK whose key is missing is still not the language model",
			ocr:        ProviderSpec{SDK: SDKGoogleVision},
			wantShares: false, wantSDK: SDKGoogleVision, wantModel: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := Bootstrap{LLM: llm, OCR: tc.ocr}
			if got := b.SharesOneProvider(); got != tc.wantShares {
				t.Errorf("SharesOneProvider()=%v, want %v", got, tc.wantShares)
			}
			if got := b.OCRSDK(); got != tc.wantSDK {
				t.Errorf("OCRSDK()=%q, want %q", got, tc.wantSDK)
			}
			if got := b.OCRModel(); got != tc.wantModel {
				t.Errorf("OCRModel()=%q, want %q", got, tc.wantModel)
			}
		})
	}
}

// Apply must do nothing at all when the environment asked for nothing, so a
// self-hosted install lands on the setup wizard rather than on half a provider.
func TestNothingConfiguredIsNotAnInstruction(t *testing.T) {
	if (Bootstrap{}).Configured() {
		t.Fatal("an empty bootstrap should not count as configured")
	}
	if (Bootstrap{LLM: ProviderSpec{SDK: SDKOpenAI}}).Configured() {
		t.Fatal("an SDK with no key should not count as configured")
	}
}
