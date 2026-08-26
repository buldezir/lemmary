package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// EnvAppliedField holds a digest per tracked environment variable of the value
// this install last acted on. See migrations/1730000013_env_applied.go for why
// it exists and why it stores digests.
const EnvAppliedField = "env_applied"

// envBinding is one environment variable that keeps authority over a stored
// setting after first boot.
//
// Apply is called only when the variable's value differs from the digest
// recorded on the settings record, and receives the current value — empty when
// the variable has been removed, which is a change like any other: a plan that
// stops pinning a model should stop pinning it, and the stored empty value then
// falls through applyBindingFallbacks the way an unconfigured install does.
type envBinding struct {
	EnvKey string
	Apply  func(app core.App, settings *core.Record, value string) error
}

// envBindings lists what the environment may change after first boot.
//
// Deliberately not everything env can seed. A variable belongs here when the
// operator owns the setting for the life of the install — model routing, the
// credentials behind it, which provider serves OCR. Timeouts and retry counts
// are left out: they are tuning an admin does from the Settings page, and there
// is no operator decision they express.
func envBindings() []envBinding {
	return []envBinding{
		// Extraction is the root of the binding chain and nothing falls back to
		// it, so removing the variable restores the code default rather than
		// leaving the install with no extraction model at all.
		settingsRequiredText("OPENAI_MODEL", "extract_model", func() string {
			return DefaultsFromEnv().ExtractModel
		}),
		// Chat and Deep Search do fall back — through applyBindingFallbacks, to
		// chat and then to extraction — so empty is exactly the right way to
		// express "no longer pinned".
		settingsOptionalText("OPENAI_CHAT_MODEL", "chat_model"),
		settingsOptionalText("OPENAI_SEARCH_MODEL", "search_model"),
		settingsBool("NEAR_DUPLICATE_DETECTION_ENABLED", "near_duplicate_detection_enabled", false),

		providerText("OPENAI_API_KEY", aiprovider.SDKOpenAI, "api_key"),
		providerText("OPENAI_BASE_URL", aiprovider.SDKOpenAI, "base_url"),
		providerText("MISTRAL_API_KEY", aiprovider.SDKMistral, "api_key"),
		providerText("MISTRAL_API_BASE_URL", aiprovider.SDKMistral, "base_url"),
		providerText("GOOGLE_VISION_API_KEY", aiprovider.SDKGoogleVision, "api_key"),

		{EnvKey: "OCR_PROVIDER", Apply: applyOCRProviderBinding},
	}
}

// settingsOptionalText writes the value through, empty included. Use it where
// an empty stored value already means something sensible.
func settingsOptionalText(envKey, field string) envBinding {
	return envBinding{
		EnvKey: envKey,
		Apply: func(_ core.App, settings *core.Record, value string) error {
			settings.Set(field, value)
			return nil
		},
	}
}

// settingsRequiredText writes the value, or fallback() when the variable has
// been removed — for fields where an empty stored value would break the install
// rather than mean "unset".
func settingsRequiredText(envKey, field string, fallback func() string) envBinding {
	return envBinding{
		EnvKey: envKey,
		Apply: func(_ core.App, settings *core.Record, value string) error {
			if value == "" {
				settings.Set(field, fallback())
				return nil
			}
			settings.Set(field, value)
			return nil
		},
	}
}

// settingsBool writes a boolean setting. def is where a removed variable leaves
// the field: the same default DefaultsFromEnv uses, so removing the variable
// leaves the install where one that never set it would be. An unparseable value
// lands on def too, for the reason envIntDefault gives — a typo must not be
// read as a deliberate "off".
func settingsBool(envKey, field string, def bool) envBinding {
	return envBinding{
		EnvKey: envKey,
		Apply: func(_ core.App, settings *core.Record, value string) error {
			if value == "" {
				settings.Set(field, def)
				return nil
			}
			settings.Set(field, getEnvBool(envKey, def))
			return nil
		},
	}
}

// providerText updates a field on the env-seeded provider record for sdk.
//
// The record is found by its default alias, which is what SeedFromEnv names it.
// A renamed or deleted provider is skipped rather than recreated: recreating
// would resurrect a provider an admin deliberately removed, and every boot
// after that would do it again.
func providerText(envKey, sdk, field string) envBinding {
	return envBinding{
		EnvKey: envKey,
		Apply: func(app core.App, _ *core.Record, value string) error {
			alias := aiprovider.DefaultAlias(sdk)
			record, err := aiprovider.FindByAlias(app, alias)
			if err != nil || record == nil {
				app.Logger().Warn("environment change skipped: no provider to apply it to",
					"env", envKey, "alias", alias)
				return nil
			}
			record.Set(field, value)
			if err := app.Save(record); err != nil {
				return fmt.Errorf("apply %s to provider %s: %w", envKey, alias, err)
			}
			return nil
		},
	}
}

// applyOCRProviderBinding repoints ocr_provider_id at the env-named provider.
//
// The variable names an SDK ("mistral"), not a record id, because an id is
// generated at seed time and an orchestrator cannot know it.
func applyOCRProviderBinding(app core.App, settings *core.Record, value string) error {
	sdk := strings.TrimSpace(value)
	if sdk == "" {
		return nil
	}
	if !aiprovider.ValidSDK(sdk) {
		app.Logger().Warn("environment change ignored: not a known provider sdk",
			"env", "OCR_PROVIDER", "value", sdk)
		return nil
	}
	alias := aiprovider.DefaultAlias(sdk)
	record, err := aiprovider.FindByAlias(app, alias)
	if err != nil || record == nil {
		app.Logger().Warn("environment change skipped: no provider to bind",
			"env", "OCR_PROVIDER", "alias", alias)
		return nil
	}
	settings.Set("ocr_provider_id", record.Id)
	return nil
}

// ApplyEnvChanges applies the tracked environment variables whose values differ
// from what this install last acted on, and records the new digests.
//
// Runs on every bootstrap, before the runtime reload, so a container recreated
// with different environment serves the new configuration on its first request.
func ApplyEnvChanges(app core.App) error {
	settings, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return fmt.Errorf("load %s: %w", CollectionName, err)
	}

	applied := decodeEnvApplied(settings)
	changed := make([]string, 0, len(envBindings()))

	for _, binding := range envBindings() {
		value := os.Getenv(binding.EnvKey)
		digest := envDigest(value)
		if recorded, ok := applied[binding.EnvKey]; ok && recorded == digest {
			continue
		}
		if err := binding.Apply(app, settings, value); err != nil {
			return err
		}
		if digest == "" {
			delete(applied, binding.EnvKey)
		} else {
			applied[binding.EnvKey] = digest
		}
		changed = append(changed, binding.EnvKey)
	}

	if len(changed) == 0 {
		return nil
	}

	encoded, err := json.Marshal(applied)
	if err != nil {
		return fmt.Errorf("encode %s: %w", EnvAppliedField, err)
	}
	settings.Set(EnvAppliedField, string(encoded))
	if err := app.Save(settings); err != nil {
		return fmt.Errorf("save %s: %w", CollectionName, err)
	}

	// The variable names are safe to log; the values are not, and several are
	// API keys.
	app.Logger().Info("applied environment changes to stored settings", "variables", changed)
	return nil
}

// RecordEnvApplied stamps the current values of every tracked variable without
// applying anything. Called right after the singleton is seeded, whose values
// already came from the environment — without it the first boot after seeding
// would see every variable as changed and rewrite what it just wrote.
func RecordEnvApplied(settings *core.Record) error {
	applied := map[string]string{}
	for _, binding := range envBindings() {
		if digest := envDigest(os.Getenv(binding.EnvKey)); digest != "" {
			applied[binding.EnvKey] = digest
		}
	}
	encoded, err := json.Marshal(applied)
	if err != nil {
		return fmt.Errorf("encode %s: %w", EnvAppliedField, err)
	}
	settings.Set(EnvAppliedField, string(encoded))
	return nil
}

// envDigest hashes a value for change detection. An unset variable digests to
// the empty string so "never set" and "set to empty" are the same thing, which
// is how the shell treats them too.
func envDigest(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// decodeEnvApplied reads the digest map, treating anything unreadable as empty.
//
// Unreadable means an older install (the field did not exist) or hand-edited
// JSON. Both should re-apply the environment once rather than fail the boot: the
// worst case is that a value the environment already agrees with is written
// again.
func decodeEnvApplied(settings *core.Record) map[string]string {
	raw := strings.TrimSpace(settings.GetString(EnvAppliedField))
	if raw == "" {
		return map[string]string{}
	}
	applied := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &applied); err != nil {
		return map[string]string{}
	}
	return applied
}
