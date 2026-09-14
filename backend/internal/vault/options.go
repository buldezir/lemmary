package vault

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Read from the environment rather than app_settings: a toggle stored in the
// database would live inside the very file it is meant to protect, and could
// not be consulted before the database is decrypted.
const (
	EnvEnabled     = "VAULT_ENABLED"
	EnvDir         = "VAULT_DIR"
	EnvWorkDir     = "VAULT_WORKDIR"
	EnvKeep        = "VAULT_KEEP_GENERATIONS"
	EnvAllowShrink = "VAULT_ALLOW_SHRINK"
	// EnvAllowDiskWorkDir permits decrypting into a working directory that is not
	// memory-backed. Tests and local development only.
	EnvAllowDiskWorkDir = "VAULT_ALLOW_DISK_WORKDIR"
	// EnvAllowInsecureGate accepts serving the unlock form over cleartext HTTP.
	EnvAllowInsecureGate = "VAULT_ALLOW_INSECURE_GATE"
	// EnvPassphrase unlocks non-interactively, for CLI subcommands and tests: a
	// passphrase in the environment sits next to the ciphertext it protects.
	//
	// The exception to the read-once rule below. It is a credential, not a setting,
	// so it is read with os.Getenv at each of the three points that consume it and
	// never stored on Options, which is logged, compared and kept for the life of
	// the process.
	EnvPassphrase = "VAULT_PASSPHRASE"
)

// OptionsFromEnv reads every switch once and carries it in Options: a getenv
// buried in a path reached only while locked is a setting nobody can answer
// questions about, and one of these decides whether the archive's password may
// cross a network in the clear.
//
// WorkDir defaults to a sibling of the vault directory rather than somewhere
// under it, so a misconfiguration cannot write plaintext inside the directory
// meant to hold only ciphertext.
func OptionsFromEnv() (Options, error) {
	o := Options{
		Dir:     os.Getenv(EnvDir),
		WorkDir: os.Getenv(EnvWorkDir),
		Log: func(format string, args ...any) {
			slog.Info(strings.TrimSpace(fmt.Sprintf(format, args...)))
		},
	}

	var err error
	for _, f := range []struct {
		key string
		dst *bool
	}{
		{EnvEnabled, &o.Enabled},
		{EnvAllowShrink, &o.AllowShrink},
		{EnvAllowDiskWorkDir, &o.AllowDiskWorkDir},
		{EnvAllowInsecureGate, &o.AllowInsecureGate},
	} {
		if *f.dst, err = envBool(f.key); err != nil {
			return Options{}, err
		}
	}

	// Unlike the booleans, an unparseable generation count falls back to the
	// default: too few generations costs rollback depth, never the archive.
	if n, convErr := strconv.Atoi(os.Getenv(EnvKeep)); convErr == nil && n > 0 {
		o.KeepGenerations = n
	}

	if o.Enabled {
		if o.Dir == "" {
			o.Dir = "pb_data"
		}
		if o.WorkDir == "" {
			o.WorkDir = filepath.Join(filepath.Dir(strings.TrimRight(o.Dir, string(filepath.Separator))), "pb_work")
		}
	}
	return o, nil
}

// envBool refuses a value it does not recognise. VAULT_ENABLED=Y is a plausible
// thing to write, and falling back to false would leave encryption off while the
// operator believed it on, with the volume filling with plaintext and nothing
// anywhere saying so. The escape hatches cut the same way in the other
// direction.
func envBool(key string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf(
			"vault: %s=%q is not a boolean; use 1/true/yes/on or 0/false/no/off, or leave it unset",
			key, os.Getenv(key))
	}
}
