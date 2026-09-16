package config

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The environment can carry a key, not a sign-in: AI_SDK=chatgpt would pass
// the IsLLM check and seed a provider row no file can ever complete.
func TestChatGPTIsNotAnAISDKValue(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKChatGPT)
	t.Setenv(EnvAIAPIKey, "sk-test")

	_, err := AIEnvFromEnv()
	if err == nil {
		t.Fatal("AI_SDK=chatgpt was accepted")
	}
	if !strings.Contains(err.Error(), EnvAISDK) {
		t.Errorf("the error does not name the variable: %v", err)
	}
	// The message lists what is allowed and must not list the value it just
	// refused, which is the whole failure mode it exists to avoid.
	if strings.Contains(err.Error(), "want one of") && strings.Count(err.Error(), aiprovider.SDKChatGPT) > 1 {
		t.Errorf("the error offers chatgpt as an alternative to itself: %v", err)
	}
}

// OCR_SDK=chatgpt is refused for the credential, not the capability: a sign-in
// cannot be written into a file. The message has to say so rather than claim
// the SDK cannot read a document, which Settings would disprove.
func TestChatGPTIsNotAnOCRSDKValue(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvOCRSDK, aiprovider.SDKChatGPT)

	_, err := AIEnvFromEnv()
	if err == nil {
		t.Fatal("OCR_SDK=chatgpt was accepted")
	}
	if !strings.Contains(err.Error(), EnvOCRSDK) {
		t.Errorf("the error does not name the variable: %v", err)
	}
	if strings.Contains(err.Error(), "embeddings only") {
		t.Errorf("the error still describes the wrong SDK: %v", err)
	}
	if strings.Contains(err.Error(), "cannot read a document") {
		t.Errorf("the error blames the capability, which chatgpt now has: %v", err)
	}
	if !strings.Contains(err.Error(), "Settings") {
		t.Errorf("the error does not say where the provider can be configured: %v", err)
	}
	// Same trap as the AI_SDK message above: the alternatives are derived, so
	// chatgpt must not appear in its own "want one of" list.
	if strings.Contains(err.Error(), "want one of") && strings.Count(err.Error(), aiprovider.SDKChatGPT) > 1 {
		t.Errorf("the error offers chatgpt as an alternative to itself: %v", err)
	}
}

// providerRecord loads a row the way PocketBase does: PostScan is what fills
// Original(), which the rotation test below rests on.
func providerRecord(t *testing.T, oauth string) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection(aiprovider.CollectionName)
	collection.Fields.Add(
		&core.TextField{Name: "sdk"},
		&core.TextField{Name: "alias"},
		&core.TextField{Name: "base_url"},
		&core.TextField{Name: "api_key"},
		&core.TextField{Name: aiprovider.OAuthField},
	)
	record := core.NewRecord(collection)
	record.Id = "provider1"
	record.Set("sdk", aiprovider.SDKChatGPT)
	record.Set("alias", "ChatGPT")
	record.Set(aiprovider.OAuthField, oauth)
	if err := record.PostScan(); err != nil {
		t.Fatal(err)
	}
	return record
}

// Sign-in and sign-out also write the token and nothing else, so "oauth moved"
// cannot tell them from a refresh; only whether the row can still serve.
func TestOnlyATokenRotationSkipsTheReload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		before  string
		after   string
		skipped bool
	}{
		{"refresh rotates a live token", `{"access":"old"}`, `{"access":"new"}`, true},
		{"sign-in fills an empty column", "", `{"access":"new"}`, false},
		{"sign-out empties it", `{"access":"old"}`, "", false},
		{"a whitespace-only column is empty", "   ", `{"access":"new"}`, false},
		{"an unchanged token is not a rotation", `{"access":"same"}`, `{"access":"same"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := providerRecord(t, tc.before)
			record.Set(aiprovider.OAuthField, tc.after)
			if got := onlyTokenRotated(record); got != tc.skipped {
				t.Fatalf("onlyTokenRotated = %v, want %v", got, tc.skipped)
			}
		})
	}
}

func TestARotationAlongsideAnotherFieldStillReloads(t *testing.T) {
	for _, field := range []string{"sdk", "alias", "base_url", "api_key"} {
		t.Run(field, func(t *testing.T) {
			record := providerRecord(t, `{"access":"old"}`)
			record.Set(aiprovider.OAuthField, `{"access":"new"}`)
			record.Set(field, "moved")
			if onlyTokenRotated(record) {
				t.Fatalf("a write that also changed %s skipped the reload", field)
			}
		})
	}
}
