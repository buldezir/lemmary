package vault

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment configuration.
//
// These are read from the environment rather than from app_settings on purpose:
// a toggle stored in the database would live inside the very file it is meant to
// protect, and could not be consulted before the database is decrypted.
const (
	EnvEnabled     = "VAULT_ENABLED"
	EnvDir         = "VAULT_DIR"
	EnvWorkDir     = "VAULT_WORKDIR"
	EnvKeep        = "VAULT_KEEP_GENERATIONS"
	EnvAllowShrink = "VAULT_ALLOW_SHRINK"
	// EnvAllowDiskWorkDir permits decrypting into a working directory that is
	// not memory-backed. Tests and local development only.
	EnvAllowDiskWorkDir = "VAULT_ALLOW_DISK_WORKDIR"
	// EnvAllowInsecureGate accepts serving the unlock form over cleartext HTTP.
	EnvAllowInsecureGate = "VAULT_ALLOW_INSECURE_GATE"
	// EnvPassphrase unlocks non-interactively. It exists for CLI subcommands
	// and for tests, never as the recommended way to run a server: a passphrase
	// in the environment sits next to the ciphertext it protects.
	//
	// The deliberate exception to the read-once rule below: it is a credential,
	// not a setting, so it is read with os.Getenv at each of the three points
	// that consume it and never stored on Options. Options is logged, compared
	// and carried for the life of the process; the master password for the whole
	// archive should live in none of those places.
	EnvPassphrase = "VAULT_PASSPHRASE"
)

// OptionsFromEnv builds vault options from the process environment.
//
// Every switch is read here, once, and carried in Options from then on — the
// house rule for environment flags, and load-bearing for these in particular:
// a getenv buried in a code path reached only while locked is a setting nobody
// can answer questions about, and one of these decides whether the archive's
// password may cross a network in the clear.
//
// WorkDir defaults to a sibling of the vault directory rather than to somewhere
// under it, so a misconfiguration cannot end up writing plaintext inside the
// directory that is supposed to hold only ciphertext.
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

	// Unlike the booleans, a generation count that does not parse falls back to
	// the default: too few generations costs rollback depth, never the archive,
	// and refusing to boot over it would be out of proportion.
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

// envBool reads a boolean environment variable, refusing a value it does not
// recognise.
//
// Falling back to false is the wrong answer for this family. VAULT_ENABLED=Y is
// a plausible thing to write and would leave encryption off while the operator
// believed it on — the volume filling with plaintext, every guarantee in the
// documentation silently void, and nothing anywhere saying so. The same
// reasoning applies to the escape hatches in the other direction. An unparseable
// value is a mistake, and the only safe response to a mistake in this file is to
// refuse to start.
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
