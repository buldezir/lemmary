package vault

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"lemmary/backend/internal/appargs"
	"lemmary/backend/internal/crypt"
)

// Open prepares encryption at rest and, for serve, blocks until unlock.
// Disabled, it returns a usable zero vault. Unlock has to precede
// pocketbase.New; see internal/boot.
func Open(argv []string) (v *Vault, err error) {
	opts, err := OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	if !opts.Enabled {
		if err := checkNotEncrypted(argv, opts); err != nil {
			return nil, err
		}
		return New(opts)
	}
	if err := GuardDataDirFlag(argv); err != nil {
		return nil, err
	}

	v, err = New(opts)
	if err != nil {
		return nil, err
	}

	// Vault holds the directory lock here, and plaintext after unlock. Close both
	// on any error: main only defers Close for a Result it received.
	defer func() {
		if err != nil {
			if cerr := v.Close(); cerr != nil {
				v.opts.Log("vault: cleanup after a failed start also failed: %v", cerr)
			}
			v = nil
		}
	}()

	// Only serving needs the interactive gate. CLI subcommands unlock from the
	// environment or fail, rather than hanging on a form nobody is watching.
	if !appargs.IsServe(argv) {
		if !v.Initialized() {
			return nil, fmt.Errorf("this instance is not initialised yet; start the server once and set an unlock password")
		}
		if err = v.Unlock(Credential{Password: os.Getenv(EnvPassphrase)}); err != nil {
			return nil, err
		}
		// Subcommands get the redirect too. None touches a document today, but the day
		// one does, the alternative is plaintext written to the container overlay,
		// which is real disk, by a path nobody would think to check.
		if err = v.InstallTempDir(); err != nil {
			return nil, err
		}
		return v, nil
	}

	// The gate takes exactly the address the server is about to take, and is told
	// whether it will carry cleartext: with domain arguments PocketBase serves
	// HTTPS on :443 via autocert, but nothing listens there while locked, so a
	// browser reaching :80 has no TLS and the password would travel in the clear.
	addr, cleartext := appargs.ServeAddr(argv)
	res, err := v.Gate(context.Background(), addr, cleartext)
	if err != nil {
		return nil, err
	}
	if res.Initialized && res.RecoveryCode != "" {
		// Never log the code itself: it is a standalone, unrevokable credential for the
		// whole archive, and container logs land unencrypted on the host disk this
		// feature exists to protect. This path had no browser to receive it, so point
		// the operator at the API instead.
		log.Printf("vault: initialised. A recovery code ending %q was generated but deliberately not logged — "+
			"mint one you can record with POST /api/vault/recovery-code once signed in.",
			crypt.RecoveryHint(res.RecoveryCode))
	}
	if err := v.InstallTempDir(); err != nil {
		return nil, err
	}
	return v, nil
}

// checkNotEncrypted refuses to boot a plaintext install on top of an encrypted
// volume. checkNoPlaintextInstall guards the other direction; this one is by
// far the easier mistake, because it is made by omitting something: bringing an
// instance up without the encrypted overlay, or dropping one line from an
// environment file.
//
// Nothing else would notice. Open returns the zero vault, PocketBase opens the
// volume the ciphertext is on, finds no data.db, creates one and serves a setup
// wizard, writing plaintext into the volume documented to hold only ciphertext.
// The operator's first evidence is an archive that appears to have lost every
// document.
//
// It looks where PocketBase is about to look, consulting --dir and the
// executable-relative default as well as VAULT_DIR: the environment being
// caught is already wrong, so VAULT_DIR may be the thing that went missing.
func checkNotEncrypted(argv []string, opts Options) error {
	for _, dir := range candidateDataDirs(argv, opts) {
		if !looksLikeVault(dir) {
			continue
		}
		return fmt.Errorf(
			"%s holds an encrypted vault (%s and %s are there) but %s is not set, so this process would create a "+
				"fresh plaintext install beside the ciphertext and serve an empty archive. Set %s=1 — with docker "+
				"compose, bring the instance up with -f docker-compose.encrypted.yml as docs/encryption.md describes",
			dir, keyringName, currentName, EnvEnabled, EnvEnabled)
	}
	return nil
}

// candidateDataDirs lists the directories PocketBase might use as its data
// directory for this invocation.
func candidateDataDirs(argv []string, opts Options) []string {
	dirs := []string{}
	if d := appargs.Flag(argv, "--dir"); d != "" {
		dirs = append(dirs, d)
	}
	// Set but disregarded is the exact shape of the accident: an environment that
	// still describes an encrypted install with the switch turned off.
	if opts.Dir != "" {
		dirs = append(dirs, opts.Dir)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "pb_data"))
	}
	return dirs
}

// looksLikeVault requires both markers. A keyring alone can be left by a `vault
// init` that was never used, and refusing to start over that would strand an
// install with no data in it; CURRENT beside it means a generation was
// committed, so there is an archive here to lose.
func looksLikeVault(dir string) bool {
	for _, name := range []string{keyringName, currentName} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// IsCommand matches argv directly rather than through cobra, because cobra runs
// inside app.Execute, by which point PocketBase has bootstrapped, which is the
// thing these commands must not do.
// the subcommand.
func IsCommand(argv []string) (op string, ok bool) {
	bare := appargs.Bare(argv)
	if len(bare) == 0 || bare[0] != "vault" {
		return "", false
	}
	if len(bare) >= 2 {
		return bare[1], true
	}
	return "", true
}

// RunCommand refuses anything but `init` here rather than letting it fall
// through: Open would try to unlock from the environment and app.Execute would
// then report an unknown command, which reads as an encryption failure.
func RunCommand(op string) int {
	if op != "init" {
		fmt.Fprintln(os.Stderr, "usage: vault init")
		return 1
	}
	return runInit()
}

// runInit creates the keyring for a brand new instance and prints the recovery
// code exactly once. Nothing else reaches one: a vault could only be created
// from serve, which never returns, the code is deliberately never logged, and
// the endpoint that mints a replacement needs superuser auth, which does not
// exist when a fresh vault is unlocked.
//
// The contract is meant to be read by a script:
//
//	exit 0, stdout "vault: already initialised"     a keyring is already there
//	exit 0, stdout "vault-recovery-code: <code>"    one was created
//	exit 1, stderr <reason>                         anything else
//
// Re-running is safe, so a provisioning step can retry after a later failure.
func runInit() int {
	opts, err := OptionsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vault init: %v\n", err)
		return 1
	}
	if !opts.Enabled {
		fmt.Fprintf(os.Stderr, "vault init: %s must be set\n", EnvEnabled)
		return 1
	}
	passphrase := os.Getenv(EnvPassphrase)
	if passphrase == "" {
		fmt.Fprintf(os.Stderr, "vault init: %s must be set\n", EnvPassphrase)
		return 1
	}

	v, err := New(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vault init: %v\n", err)
		return 1
	}
	defer func() {
		// Wipes the plaintext working directory and releases the lock; skipping it
		// would leave the decrypted archive on a tmpfs and the lock held, and the
		// serving container would then refuse to start.
		if cerr := v.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "vault init: cleanup failed: %v\n", cerr)
		}
	}()

	if v.Initialized() {
		fmt.Println("vault: already initialised")
		return 0
	}

	// An empty user id: no account exists yet. The wrap this creates is removed the
	// first time a real credential is enrolled (Keyring.RemoveBootstrapWrap), so
	// this password does not stay a valid key to the archive.
	code, err := v.Init("", passphrase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vault init: %v\n", err)
		return 1
	}

	// The only time this string is ever printed; there is no way to ask again.
	fmt.Printf("vault-recovery-code: %s\n", code)
	return 0
}
