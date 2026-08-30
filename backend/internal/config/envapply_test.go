package config

import (
	"encoding/json"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func newTestSettingsRecord(t testing.TB) *core.Record {
	t.Helper()

	collection := core.NewBaseCollection(CollectionName)
	collection.Fields.Add(
		&core.TextField{Name: "extract_model"},
		&core.TextField{Name: "chat_model"},
		&core.TextField{Name: "search_model"},
		&core.BoolField{Name: "near_duplicate_detection_enabled"},
		&core.JSONField{Name: EnvAppliedField, MaxSize: 20000},
	)
	return core.NewRecord(collection)
}

// An unset variable and one set to empty are the same thing, which is how the
// shell treats them: os.Getenv cannot tell them apart.
func TestEnvDigestTreatsUnsetAndEmptyAlike(t *testing.T) {
	t.Parallel()

	if got := envDigest(""); got != "" {
		t.Fatalf("empty digested to %q", got)
	}
	if envDigest("a") == envDigest("b") {
		t.Fatal("different values digested the same")
	}
	if envDigest("a") != envDigest("a") {
		t.Fatal("digest is not stable")
	}
}

// The digest must not be the value: several tracked variables are API keys.
func TestEnvDigestDoesNotContainTheValue(t *testing.T) {
	t.Parallel()

	const secret = "sk-super-secret-key"
	if got := envDigest(secret); got == secret || len(got) != 64 {
		t.Fatalf("digest %q is not a sha256 hex of the value", got)
	}
}

func TestDecodeEnvAppliedToleratesGarbage(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"missing field": "",
		"blank":         "   ",
		"not json":      "{oh no",
		"wrong shape":   `["a","b"]`,
		// What an install that predates the field actually holds: the column is
		// SQL NULL and the JSON field reads it back as this literal.
		"json null": "null",
	} {
		t.Run(name, func(t *testing.T) {
			record := newTestSettingsRecord(t)
			record.Set(EnvAppliedField, raw)

			got := decodeEnvApplied(record)
			if len(got) != 0 {
				t.Fatalf("expected an empty map, got %v", got)
			}
			// len() cannot tell an empty map from a nil one, and ApplyEnvChanges
			// writes into whatever it gets back.
			got["OPENAI_MODEL"] = envDigest("anything")
		})
	}
}

func TestDecodeEnvAppliedReadsWhatRecordEnvAppliedWrote(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "written-model")
	t.Setenv("OPENAI_CHAT_MODEL", "")

	record := newTestSettingsRecord(t)
	if err := RecordEnvApplied(record); err != nil {
		t.Fatalf("RecordEnvApplied: %v", err)
	}

	applied := decodeEnvApplied(record)
	if applied["OPENAI_MODEL"] != envDigest("written-model") {
		t.Fatalf("OPENAI_MODEL digest=%q", applied["OPENAI_MODEL"])
	}
	// An unset variable is absent rather than recorded as empty, so a later boot
	// that sets it sees a change.
	if _, ok := applied["OPENAI_CHAT_MODEL"]; ok {
		t.Fatal("an unset variable was recorded")
	}
}

// Removing the variable that pins extraction must not leave the install with no
// extraction model: nothing falls back to it.
func TestRequiredTextRestoresTheDefaultWhenRemoved(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "")

	record := newTestSettingsRecord(t)
	record.Set("extract_model", "was-pinned")

	binding := settingsRequiredText("OPENAI_MODEL", "extract_model", func() string { return "code-default" })
	if err := binding.Apply(nil, record, ""); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := record.GetString("extract_model"); got != "code-default" {
		t.Fatalf("extract_model=%q, want the code default", got)
	}
}

// Chat and search do fall back through applyBindingFallbacks, so empty is the
// correct way to say "no longer pinned".
func TestOptionalTextClearsWhenRemoved(t *testing.T) {
	t.Parallel()

	record := newTestSettingsRecord(t)
	record.Set("chat_model", "was-pinned")

	binding := settingsOptionalText("OPENAI_CHAT_MODEL", "chat_model")
	if err := binding.Apply(nil, record, ""); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := record.GetString("chat_model"); got != "" {
		t.Fatalf("chat_model=%q, want empty", got)
	}
}

func TestSettingsBoolFallsBackOnRemovalAndOnGarbage(t *testing.T) {
	binding := settingsBool("NEAR_DUPLICATE_DETECTION_ENABLED", "near_duplicate_detection_enabled", false)

	t.Run("removal restores the default", func(t *testing.T) {
		t.Setenv("NEAR_DUPLICATE_DETECTION_ENABLED", "")
		record := newTestSettingsRecord(t)
		record.Set("near_duplicate_detection_enabled", true)

		if err := binding.Apply(nil, record, ""); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if record.GetBool("near_duplicate_detection_enabled") {
			t.Fatal("expected the default (off) after removal")
		}
	})

	t.Run("a typo does not read as a deliberate off", func(t *testing.T) {
		t.Setenv("NEAR_DUPLICATE_DETECTION_ENABLED", "yes-please")
		record := newTestSettingsRecord(t)

		binding := settingsBool("NEAR_DUPLICATE_DETECTION_ENABLED", "near_duplicate_detection_enabled", true)
		if err := binding.Apply(nil, record, "yes-please"); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if !record.GetBool("near_duplicate_detection_enabled") {
			t.Fatal("a malformed value overrode the default")
		}
	})

	t.Run("a real value is honoured", func(t *testing.T) {
		t.Setenv("NEAR_DUPLICATE_DETECTION_ENABLED", "true")
		record := newTestSettingsRecord(t)

		if err := binding.Apply(nil, record, "true"); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if !record.GetBool("near_duplicate_detection_enabled") {
			t.Fatal("expected true")
		}
	})
}

// Every binding must have a distinct key, or one silently shadows another in the
// digest map and its setting stops being applied.
func TestEnvBindingKeysAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, binding := range envBindings() {
		if binding.EnvKey == "" {
			t.Fatal("a binding has no environment key")
		}
		if binding.Apply == nil {
			t.Fatalf("binding %s has no Apply", binding.EnvKey)
		}
		if seen[binding.EnvKey] {
			t.Fatalf("duplicate binding for %s", binding.EnvKey)
		}
		seen[binding.EnvKey] = true
	}
}

// The stamp has to round-trip as JSON: it is stored in a JSON field, and a
// value that does not encode would fail every boot.
func TestRecordEnvAppliedProducesValidJSON(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "m")

	record := newTestSettingsRecord(t)
	if err := RecordEnvApplied(record); err != nil {
		t.Fatalf("RecordEnvApplied: %v", err)
	}

	var applied map[string]string
	if err := json.Unmarshal([]byte(record.GetString(EnvAppliedField)), &applied); err != nil {
		t.Fatalf("stamp is not valid JSON: %v", err)
	}
}

// The bug this guards: a variable that is unset has no entry in the digest map
// and digests to the empty string, and the old check ("an entry exists and it
// matches") read that as a change on every boot. Recreating the container then
// re-applied the empty value each time, clearing the chat and search models and
// the provider keys an admin had set in the Settings page.
func TestEnvChangedTreatsAnAbsentEntryAsUnset(t *testing.T) {
	t.Parallel()

	applied := map[string]string{"OPENAI_MODEL": envDigest("pinned")}

	if envChanged(applied, "OPENAI_CHAT_MODEL", envDigest("")) {
		t.Fatal("an unset variable that was never recorded read as changed")
	}
	if !envChanged(applied, "OPENAI_CHAT_MODEL", envDigest("now-set")) {
		t.Fatal("setting a previously unset variable did not read as changed")
	}
	if envChanged(applied, "OPENAI_MODEL", envDigest("pinned")) {
		t.Fatal("an unchanged variable read as changed")
	}
	if !envChanged(applied, "OPENAI_MODEL", envDigest("repinned")) {
		t.Fatal("a changed variable did not read as changed")
	}
	// Removing a variable that was applied is still a change, once: the removal
	// lands, the entry is deleted, and the next boot is quiet.
	if !envChanged(applied, "OPENAI_MODEL", envDigest("")) {
		t.Fatal("removing a set variable did not read as changed")
	}
}
