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

// This file is what internal/boot calls. It exists so the vault owns its own
// startup sequence: main.go knows only that something may need to act before
// the app is constructed, never that the reason is encryption.

// Open prepares encryption at rest and, for the serve command, blocks until
// somebody unlocks the archive.
//
// The unlock has to happen before PocketBase is constructed, because
// PocketBase's data directory is fixed at construction time and because the
// users collection that would authenticate the request lives inside the
// encrypted database. That ordering is the whole reason internal/boot exists;
// an ordinary Register on a live app runs far too late to be of any use here.
//
// A disabled vault returns a usable zero vault immediately and the application
// runs exactly as it did before.
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

	// From here on the vault holds the directory lock, and past the unlock below
	// it also holds a fully decrypted copy of the archive in the working
	// directory. Every failure after this point has to clean both up: main only
	// defers Close for a Result it actually received, so an error return would
	// otherwise leave the plaintext sitting there — persistent, under
	// VAULT_ALLOW_DISK_WORKDIR, and on tmpfs for as long as a boot loop keeps
	// failing. That is precisely what Close exists to prevent.
	defer func() {
		if err != nil {
			if cerr := v.Close(); cerr != nil {
				v.opts.Log("vault: cleanup after a failed start also failed: %v", cerr)
			}
			v = nil
		}
	}()

	// Only serving needs the interactive gate. CLI subcommands unlock from the
	// environment or fail, rather than hanging on a web form nobody is watching.
	if !appargs.IsServe(argv) {
		if !v.Initialized() {
			return nil, fmt.Errorf("this instance is not initialised yet; start the server once and set an unlock password")
		}
		if err = v.Unlock(Credential{Password: os.Getenv(EnvPassphrase)}); err != nil {
			return nil, err
		}
		// Subcommands get the redirect too. None of them touches a document
		// today, so this changes nothing now -- but the day one does, the
		// alternative is plaintext copies written to the container overlay,
		// which is real disk, by a path nobody would think to check.
		if err = v.InstallTempDir(); err != nil {
			return nil, err
		}
		return v, nil
	}

	// The gate takes exactly the address the server is about to take over, and
	// is told whether that address will carry cleartext — with domain arguments
	// PocketBase serves HTTPS on :443 via autocert and uses :80 only to
	// redirect, but nothing is listening on :443 while the instance is locked,
	// so a browser reaching :80 has no TLS to fall back to and the password
	// typed into the unlock form would travel in the clear.
	addr, cleartext := appargs.ServeAddr(argv)
	res, err := v.Gate(context.Background(), addr, cleartext)
	if err != nil {
		return nil, err
	}
	if res.Initialized && res.RecoveryCode != "" {
		// Never log the code itself. It is a standalone, unrevokable credential
		// for the whole archive, and container logs land unencrypted on the very
		// host disk this feature exists to protect. The browser that initialised
		// the vault already received it in the response; this path had no
		// browser, so point the operator at the API instead.
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
// volume.
//
// checkNoPlaintextInstall guards the other direction — switching encryption on
// over existing plaintext. This is the same mistake mirrored, and it is by far
// the easier of the two to make, because it is made by *omitting* something:
// running `docker compose up` without the encrypted overlay, or dropping one
// line from an environment file.
//
// Nothing else would notice. Open returns the zero vault, no data directory is
// overridden, and PocketBase opens the same volume the ciphertext is on, finds
// no data.db, creates one, and serves a setup wizard — a fresh empty install
// with the encrypted archive sitting beside it, and a plaintext database now
// written into the volume whose whole documented guarantee was that it holds
// only ciphertext. The operator's first evidence is an archive that appears to
// have lost every document.
//
// It looks where PocketBase is actually about to look, which is why the --dir
// flag and the executable-relative default are both consulted rather than just
// VAULT_DIR: the mistake being caught is an environment that is already wrong,
// so its VAULT_DIR may well be the thing that went missing.
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
	// Set but disregarded is the exact shape of the accident: an environment
	// that still describes an encrypted install with the switch turned off.
	if opts.Dir != "" {
		dirs = append(dirs, opts.Dir)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "pb_data"))
	}
	return dirs
}

// looksLikeVault reports whether a directory holds a vault rather than a
// PocketBase data directory.
//
// Both markers are required. A keyring alone can be left behind by a `vault
// init` that was never used, and refusing to start over that would strand an
// install nobody had put any data into yet; CURRENT beside it means a generation
// was committed, so there is an archive here to lose.
func looksLikeVault(dir string) bool {
	for _, name := range []string{keyringName, currentName} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// IsCommand reports whether argv asks for a vault subcommand, and returns the
// operand after it.
//
// Matched on argv directly rather than through cobra because cobra runs inside
// app.Execute, by which point PocketBase has bootstrapped — the thing these
// commands must not do. appargs applies the same flag conventions cobra does,
// so a flag whose value happens to be "vault" or "init" is never mistaken for
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

// RunCommand answers a vault subcommand and returns the process exit status.
//
// Anything but `init` is refused here rather than left to fall through: Open
// would try to unlock from the environment and app.Execute would then report an
// unknown command, which reads as an encryption failure rather than a typo.
func RunCommand(op string) int {
	if op != "init" {
		fmt.Fprintln(os.Stderr, "usage: vault init")
		return 1
	}
	return runInit()
}

// runInit creates the keyring for a brand new instance and prints the recovery
// code exactly once.
//
// It exists because there was otherwise no way to reach one. A vault could only
// be created from serve, which never returns, and the recovery code Init
// produces is deliberately never logged — logs land unencrypted on the very host
// disk this feature exists to protect. The endpoint that mints a replacement
// needs superuser auth, which does not exist yet at the moment a fresh vault is
// unlocked with no account in it. So whoever provisions an instance had no path
// to the code at all, and this command is that path.
//
// The contract is meant to be read by a script:
//
//	exit 0, stdout "vault: already initialised"     a keyring is already there
//	exit 0, stdout "vault-recovery-code: <code>"    one was created
//	exit 1, stderr <reason>                         anything else
//
// Re-running it is safe, which is what lets a provisioning step retry after a
// failure later in the sequence without a special case for "maybe it worked".
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
		// Wipes the plaintext working directory and releases the lock. A
		// one-shot that skipped this would leave the decrypted (empty) archive
		// on a tmpfs and the lock held, and the serving container would then
		// refuse to start.
		if cerr := v.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "vault init: cleanup failed: %v\n", cerr)
		}
	}()

	if v.Initialized() {
		fmt.Println("vault: already initialised")
		return 0
	}

	// An empty user id: no account exists yet. The wrap this creates is removed
	// the first time a real credential is enrolled — see
	// Keyring.RemoveBootstrapWrap — so the password used here does not stay a
	// valid key to the archive for the life of the instance.
	code, err := v.Init("", passphrase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vault init: %v\n", err)
		return 1
	}

	// The only time this string is ever printed. It is not logged, and there is
	// no way to ask for it again.
	fmt.Printf("vault-recovery-code: %s\n", code)
	return 0
}
