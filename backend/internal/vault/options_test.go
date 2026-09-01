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
		o, err := OptionsFromEnv()
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		if !o.Enabled {
			t.Errorf("%q should enable the vault", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off"} {
		t.Setenv(EnvEnabled, v)
		o, err := OptionsFromEnv()
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		if o.Enabled {
			t.Errorf("%q should not enable the vault", v)
		}
	}
}

// A value that is neither on nor off must stop the process, not pick one.
// VAULT_ENABLED=Y is an easy thing to write, and reading it as "off" would run
// an install the operator believes is encrypted, filling the volume with
// plaintext while every guarantee in the documentation quietly did not hold.
func TestOptionsFromEnvRefusesUnrecognisedBooleans(t *testing.T) {
	for _, key := range []string{EnvEnabled, EnvAllowShrink, EnvAllowDiskWorkDir, EnvAllowInsecureGate} {
		for _, v := range []string{"maybe", "2", "Y", "enabled"} {
			t.Setenv(key, v)
			if _, err := OptionsFromEnv(); err == nil {
				t.Errorf("%s=%q was accepted", key, v)
			}
			t.Setenv(key, "")
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

	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("OptionsFromEnv: %v", err)
	}
	if o.Dir != "/srv/app/pb_data" {
		t.Fatalf("Dir = %q", o.Dir)
	}
	if o.WorkDir == "" {
		t.Fatal("WorkDir was left empty")
	}
	rel, relErr := filepath.Rel(o.Dir, o.WorkDir)
	if relErr == nil && rel != ".." && !filepath.IsAbs(rel) && rel[:2] != ".." {
		t.Fatalf("WorkDir %q is inside the vault directory %q", o.WorkDir, o.Dir)
	}

	// A trailing separator must not push the default inside either.
	t.Setenv(EnvDir, "/srv/app/pb_data/")
	trailing, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("OptionsFromEnv: %v", err)
	}
	if got := trailing.WorkDir; got != "/srv/app/pb_work" {
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

	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("OptionsFromEnv: %v", err)
	}
	if o.KeepGenerations != 7 || !o.AllowShrink || !o.AllowDiskWorkDir {
		t.Fatalf("unexpected options: %+v", o)
	}

	// Nonsense or dangerous generation counts fall back to the default rather
	// than leaving no rollback depth at all.
	for _, bad := range []string{"", "0", "-3", "abc"} {
		t.Setenv(EnvKeep, bad)
		o, err := OptionsFromEnv()
		if err != nil {
			t.Fatalf("OptionsFromEnv: %v", err)
		}
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

	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("OptionsFromEnv: %v", err)
	}
	if o.Enabled || o.Dir != "" || o.WorkDir != "" {
		t.Fatalf("a disabled vault produced %+v", o)
	}
}
