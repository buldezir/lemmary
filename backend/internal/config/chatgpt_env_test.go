package config

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The environment can carry a key; it cannot carry a sign-in. AI_SDK=chatgpt
// would otherwise pass the IsLLM check and seed a provider row that the file
// naming it can never complete.
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
	// The message lists what is allowed, and must not list the value it just
	// refused -- that is the whole failure mode it exists to avoid.
	if strings.Contains(err.Error(), "want one of") && strings.Count(err.Error(), aiprovider.SDKChatGPT) > 1 {
		t.Errorf("the error offers chatgpt as an alternative to itself: %v", err)
	}
}

// OCR_SDK=chatgpt is valid as an SDK and useless for this job, the same
// situation `local` is in. The message used to say "it serves embeddings only",
// which was true of the only SDK that could reach it at the time.
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
}

// providerRecord is a detached ai_providers row loaded the way PocketBase loads
// one: PostScan is what fills Original(), which is the whole basis of the
// rotation test below.
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

// An hourly refresh must not rebuild every AI client, and a sign-in or sign-out
// must. Both write the token and nothing else, so "oauth moved" cannot tell
// them apart -- only whether the row was, and stays, able to serve.
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

// A write that moves the token and something else is a configuration change,
// whatever else it does, so it reloads.
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
