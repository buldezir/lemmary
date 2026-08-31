package vault

import (
	"path/filepath"
	"testing"
)

// A parsing slip here fails open: encryption is silently off and everything
// still appears to work, which is the worst possible failure for this feature.
func TestOptionsFromEnvEnabledParsing(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on", " 1 "} {
		t.Setenv(EnvEnabled, v)
		if !OptionsFromEnv().Enabled {
			t.Errorf("%q should enable the vault", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "maybe", "2"} {
		t.Setenv(EnvEnabled, v)
		if OptionsFromEnv().Enabled {
			t.Errorf("%q should not enable the vault", v)
		}
	}
}

// The working directory must default beside the vault directory, never inside
// it: a default that nested plaintext under the encrypted volume would defeat
// the feature for anyone who did not set it explicitly.
func TestOptionsFromEnvWorkDirDefaultsOutsideTheVault(t *testing.T) {
	t.Setenv(EnvEnabled, "1")
	t.Setenv(EnvDir, "/srv/app/pb_data")
	t.Setenv(EnvWorkDir, "")

	o := OptionsFromEnv()
	if o.Dir != "/srv/app/pb_data" {
		t.Fatalf("Dir = %q", o.Dir)
	}
	if o.WorkDir == "" {
		t.Fatal("WorkDir was left empty")
	}
	rel, err := filepath.Rel(o.Dir, o.WorkDir)
	if err == nil && rel != ".." && !filepath.IsAbs(rel) && rel[:2] != ".." {
		t.Fatalf("WorkDir %q is inside the vault directory %q", o.WorkDir, o.Dir)
	}

	// A trailing separator must not push the default inside either.
	t.Setenv(EnvDir, "/srv/app/pb_data/")
	if got := OptionsFromEnv().WorkDir; got != "/srv/app/pb_work" {
		t.Fatalf("WorkDir with a trailing separator = %q, want /srv/app/pb_work", got)
	}
}

func TestOptionsFromEnvRemainingFields(t *testing.T) {
	t.Setenv(EnvEnabled, "1")
	t.Setenv(EnvDir, "/v")
	t.Setenv(EnvWorkDir, "/w")
	t.Setenv(EnvKeep, "7")
	t.Setenv(EnvAllowShrink, "true")
	t.Setenv(EnvAllowDiskWorkDir, "1")

	o := OptionsFromEnv()
	if o.KeepGenerations != 7 || !o.AllowShrink || !o.AllowDiskWorkDir {
		t.Fatalf("unexpected options: %+v", o)
	}

	// Nonsense or dangerous generation counts fall back to the default rather
	// than leaving no rollback depth at all.
	for _, bad := range []string{"", "0", "-3", "abc"} {
		t.Setenv(EnvKeep, bad)
		o := OptionsFromEnv()
		o.applyDefaults()
		if o.KeepGenerations != keepGenerations {
			t.Fatalf("KeepGenerations for %q = %d, want the default %d", bad, o.KeepGenerations, keepGenerations)
		}
	}
}

// A disabled vault must not invent paths; nothing should be created anywhere.
func TestOptionsFromEnvDisabledLeavesPathsEmpty(t *testing.T) {
	t.Setenv(EnvEnabled, "0")
	t.Setenv(EnvDir, "")
	t.Setenv(EnvWorkDir, "")

	o := OptionsFromEnv()
	if o.Enabled || o.Dir != "" || o.WorkDir != "" {
		t.Fatalf("a disabled vault produced %+v", o)
	}
}
