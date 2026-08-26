package e2e

import (
	"testing"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

// These run on an isolated instance rather than the shared harness: they mutate
// the settings singleton and process environment, and t.Setenv forbids
// t.Parallel for exactly that reason.
func startIsolated(t testing.TB) *Harness {
	t.Helper()

	h, err := Start(Options{})
	if err != nil {
		t.Fatalf("start isolated harness: %v", err)
	}
	t.Cleanup(h.Close)
	return h
}

func settingsField(t testing.TB, h *Harness, field string) string {
	t.Helper()

	record, err := h.App.FindRecordById(config.CollectionName, config.SingletonID)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return record.GetString(field)
}

func setSettingsField(t testing.TB, h *Harness, field, value string) {
	t.Helper()

	record, err := h.App.FindRecordById(config.CollectionName, config.SingletonID)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	record.Set(field, value)
	if err := h.App.Save(record); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

// The whole point of tracking what was applied: an unchanged environment must
// not revert a change somebody made in the Settings page, and .env.example
// ships OPENAI_MODEL set, so this is the common case rather than an edge one.
func TestUnchangedEnvLeavesASettingsEditAlone(t *testing.T) {
	h := startIsolated(t)

	setSettingsField(t, h, "extract_model", "chosen-in-settings")

	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("ApplyEnvChanges: %v", err)
	}

	if got := settingsField(t, h, "extract_model"); got != "chosen-in-settings" {
		t.Fatalf("extract_model=%q — an unchanged environment overwrote a Settings edit", got)
	}
}

// And the failure the tracking exists to fix: a container recreated with
// different environment has to actually re-route.
func TestChangedEnvIsApplied(t *testing.T) {
	h := startIsolated(t)

	setSettingsField(t, h, "extract_model", "chosen-in-settings")
	t.Setenv("OPENAI_MODEL", "pinned-by-the-plan")

	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("ApplyEnvChanges: %v", err)
	}

	if got := settingsField(t, h, "extract_model"); got != "pinned-by-the-plan" {
		t.Fatalf("extract_model=%q — a changed environment did not reach the settings", got)
	}

	// Applied once, then left alone: the next boot with the same environment
	// must not fight a later Settings edit either.
	setSettingsField(t, h, "extract_model", "edited-after-the-change")
	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("second ApplyEnvChanges: %v", err)
	}
	if got := settingsField(t, h, "extract_model"); got != "edited-after-the-change" {
		t.Fatalf("extract_model=%q — the same environment was applied twice", got)
	}
}

// Removing the pin restores the code default rather than emptying the field,
// which would leave the install with no extraction model at all.
func TestRemovingTheExtractionPinRestoresTheDefault(t *testing.T) {
	h := startIsolated(t)

	t.Setenv("OPENAI_MODEL", "")
	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("ApplyEnvChanges: %v", err)
	}

	if got := settingsField(t, h, "extract_model"); got == "" {
		t.Fatal("extract_model was emptied; nothing falls back to it")
	}
}

// An API key rotated in the environment has to reach the provider record the
// clients are built from, not just the settings singleton.
func TestChangedProviderKeyReachesTheProviderRecord(t *testing.T) {
	h := startIsolated(t)

	t.Setenv("OPENAI_API_KEY", "rotated-key")
	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("ApplyEnvChanges: %v", err)
	}

	record, err := aiprovider.FindByAlias(h.App, aiprovider.DefaultAlias(aiprovider.SDKOpenAI))
	if err != nil {
		t.Fatalf("find seeded openai provider: %v", err)
	}
	if got := record.GetString("api_key"); got != "rotated-key" {
		t.Fatalf("provider api_key=%q — a rotated key did not reach the provider record", got)
	}
}

// The digest map must not hold the key itself: it is stored in app_settings,
// which is a second place a secret could leak from.
func TestAppliedStampDoesNotStoreSecretValues(t *testing.T) {
	h := startIsolated(t)

	const secret = "sk-a-very-secret-value"
	t.Setenv("OPENAI_API_KEY", secret)
	if err := config.ApplyEnvChanges(h.App); err != nil {
		t.Fatalf("ApplyEnvChanges: %v", err)
	}

	stamp := settingsField(t, h, config.EnvAppliedField)
	if stamp == "" {
		t.Fatal("nothing was stamped")
	}
	requireNotContains(t, stamp, secret)
}

func requireNotContains(t testing.TB, haystack, needle string) {
	t.Helper()

	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			t.Fatalf("expected %q not to appear in %q", needle, haystack)
		}
	}
}
