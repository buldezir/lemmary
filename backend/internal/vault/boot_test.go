package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Booting without VAULT_ENABLED against an encrypted volume must refuse.
//
// checkNoPlaintextInstall guards the other direction — switching encryption on
// over existing plaintext. This is the same mistake mirrored and much the easier
// of the two to make, because it is made by omitting something: bringing the
// instance up without the compose overlay, or dropping one line from an
// environment file. Nothing else would notice. PocketBase would open the volume,
// find no data.db, create one, and serve a setup wizard — a fresh empty install
// with the ciphertext beside it, and a plaintext database now written into the
// volume documented to hold only ciphertext.
func TestOpenRefusesAPlaintextBootOnAnEncryptedVolume(t *testing.T) {
	dir := t.TempDir()
	writeVaultMarkers(t, dir)

	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvWorkDir, "")

	_, err := Open([]string{"serve"})
	if err == nil {
		t.Fatal("a disabled vault started against an encrypted volume")
	}
	if !strings.Contains(err.Error(), EnvEnabled) {
		t.Fatalf("the refusal does not name %s: %v", EnvEnabled, err)
	}
}

// The --dir flag is consulted too, because the environment being wrong is the
// whole premise: VAULT_DIR may well be the thing that went missing.
func TestOpenRefusesAPlaintextBootPointedAtAVaultWithDir(t *testing.T) {
	dir := t.TempDir()
	writeVaultMarkers(t, dir)

	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvDir, "")
	t.Setenv(EnvWorkDir, "")

	if _, err := Open([]string{"serve", "--dir", dir}); err == nil {
		t.Fatal("a disabled vault started against an encrypted volume named by --dir")
	}
}

// An ordinary unencrypted install must be entirely unaffected: this feature is
// off by default and must cost nothing when off.
func TestOpenLeavesAnOrdinaryInstallAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvWorkDir, "")

	v, err := Open([]string{"serve", "--dir", dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if v.Enabled() {
		t.Fatal("the vault reports enabled with the switch off")
	}
}

// A keyring with no committed generation is a `vault init` nobody ever used.
// Refusing over that would strand an install with no data in it yet.
func TestOpenAllowsADirectoryWithOnlyAKeyring(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keyringName), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvWorkDir, "")

	if _, err := Open([]string{"serve", "--dir", dir}); err != nil {
		t.Fatalf("Open: %v", err)
	}
}

// An unparseable switch must stop the process rather than pick a meaning.
// VAULT_ENABLED=Y reading as "off" would run an install the operator believes is
// encrypted.
func TestOpenRefusesAnUnparseableEnabledFlag(t *testing.T) {
	t.Setenv(EnvEnabled, "Y")
	t.Setenv(EnvDir, t.TempDir())
	t.Setenv(EnvWorkDir, "")

	if _, err := Open([]string{"serve"}); err == nil {
		t.Fatal("VAULT_ENABLED=Y was accepted")
	}
}

func writeVaultMarkers(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{keyringName, currentName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
