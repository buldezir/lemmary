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
	EnvPassphrase = "VAULT_PASSPHRASE"
)

// OptionsFromEnv builds vault options from the process environment.
//
// WorkDir defaults to a sibling of the vault directory rather than to somewhere
// under it, so a misconfiguration cannot end up writing plaintext inside the
// directory that is supposed to hold only ciphertext.
func OptionsFromEnv() Options {
	o := Options{
		Enabled: envBool(EnvEnabled),
		Dir:     os.Getenv(EnvDir),
		WorkDir: os.Getenv(EnvWorkDir),
		Log: func(format string, args ...any) {
			slog.Info(strings.TrimSpace(fmt.Sprintf(format, args...)))
		},
		AllowShrink:      envBool(EnvAllowShrink),
		AllowDiskWorkDir: envBool(EnvAllowDiskWorkDir),
	}
	if n, err := strconv.Atoi(os.Getenv(EnvKeep)); err == nil && n > 0 {
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
	return o
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
