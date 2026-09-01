package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lemmary/backend/internal/crypt"
)

// The keyring is the one file on the persistent volume that is not ciphertext.
// It holds the master key sealed once per credential, so any user of the
// instance can unlock it, and so adding a credential later re-seals one small
// blob instead of rewriting the archive.
//
// What it deliberately does not hold: email addresses or any other user
// identifier beyond opaque PocketBase record ids. This file sits on the
// untrusted volume, and a list of customer email addresses is exactly the kind
// of metadata an at-rest attacker should not get for free. The cost is that an
// unlock attempt has to try each password wrap in turn; with the handful of
// users a single-customer instance has, that is a few hundred milliseconds once
// per container start.
//
// There is no separate verifier blob. The AEAD tag on each wrap *is* the
// credential check: a wrong credential and a tampered wrap both fail to
// authenticate, and storing anything extra would only tell an attacker which of
// the two they achieved. MKFP exists solely to detect a keyring assembled from
// two different vaults, never to gate an unlock.
const (
	keyringName    = "keyring.json"
	keyringVersion = 1
)

// WrapType names a credential family.
type WrapType string

const (
	// WrapPassword is stretched with Argon2id because a human chose it.
	WrapPassword WrapType = "password"
	// WrapRecovery carries 160 bits of uniform entropy, so it is not stretched.
	WrapRecovery WrapType = "recovery"
	// WrapPasskey uses the WebAuthn PRF extension output directly as the KEK.
	WrapPasskey WrapType = "webauthn-prf"
)

// Wrap is one sealed copy of the master key.
type Wrap struct {
	ID     string           `json:"id"`
	User   string           `json:"user,omitempty"`
	Type   WrapType         `json:"type"`
	CredID string           `json:"cred_id,omitempty"`
	Hint   string           `json:"hint,omitempty"`
	KDF    *crypt.KDFParams `json:"kdf,omitempty"`
	CT     string           `json:"ct"`
}

// Keyring is the on-disk keyring document.
type Keyring struct {
	Version int    `json:"v"`
	Salt    []byte `json:"salt"`
	Wraps   []Wrap `json:"wraps"`
	MKFP    string `json:"mk_fp"`
}

var (
	// ErrNoKeyring reports that the instance has never been initialised.
	ErrNoKeyring = errors.New("vault: no keyring")
	// ErrWrongKey reports that no wrap could be opened with the credential.
	ErrWrongKey = errors.New("vault: wrong or missing credential")
	// ErrLastWrap reports an attempt to remove the only way back in.
	ErrLastWrap = errors.New("vault: refusing to remove the last credential")
)

// wrapAAD binds a wrap to its own metadata, including the Argon2 cost it was
// written with.
//
// It covers only this wrap, never the whole document: binding the full wrap list
// would mean enrolling one user invalidated everyone else's wrap. Including the
// KDF parameters is what stops an attacker who can edit this file from rewriting
// memory=64MiB down to memory=8KiB and making offline guessing cheap — the
// rewritten parameters simply fail to authenticate.
func wrapAAD(w Wrap) (string, error) {
	b, err := json.Marshal(struct {
		V      int              `json:"v"`
		ID     string           `json:"id"`
		User   string           `json:"user"`
		Type   WrapType         `json:"type"`
		CredID string           `json:"cred_id"`
		KDF    *crypt.KDFParams `json:"kdf"`
	}{keyringVersion, w.ID, w.User, w.Type, w.CredID, w.KDF})
	if err != nil {
		return "", err
	}
	return "lemmary/vault/wrap/v1|" + string(b), nil
}

// Credential is one attempt to open the keyring.
type Credential struct {
	// Password is used for WrapPassword wraps.
	Password string
	// RecoveryCode is used for WrapRecovery wraps.
	RecoveryCode string
	// PRF is the raw 32-byte WebAuthn PRF output for WrapPasskey wraps.
	PRF []byte
	// CredID narrows a passkey attempt to one credential.
	CredID string
}

// kekFor derives the key-encryption key a credential would use for a wrap, or
// reports false when the credential cannot address that wrap at all.
func kekFor(c Credential, w Wrap) (crypt.Key, bool, error) {
	switch w.Type {
	case WrapPassword:
		if c.Password == "" || w.KDF == nil {
			return crypt.Key{}, false, nil
		}
		k, err := crypt.DeriveKEK(c.Password, *w.KDF)
		return k, err == nil, err
	case WrapRecovery:
		if c.RecoveryCode == "" {
			return crypt.Key{}, false, nil
		}
		k, err := crypt.RecoveryKEK(c.RecoveryCode)
		if err != nil {
			// A malformed code is a failed attempt, not a fatal error.
			return crypt.Key{}, false, nil
		}
		return k, true, nil
	case WrapPasskey:
		if len(c.PRF) != crypt.KeyLen {
			return crypt.Key{}, false, nil
		}
		if c.CredID != "" && w.CredID != "" && c.CredID != w.CredID {
			return crypt.Key{}, false, nil
		}
		k, err := crypt.PasskeyKEK(c.PRF)
		return k, err == nil, err
	default:
		return crypt.Key{}, false, nil
	}
}

// Unlock recovers the master key using the first wrap the credential opens.
//
// It returns ErrWrongKey when nothing matches, and never reports which wrap was
// tried or how far it got.
//
// A wrap this cannot even attempt does not stop the loop. keyring.json sits on
// the untrusted volume with no whole-document MAC — the per-wrap AAD is checked
// at UnwrapKey, which a wrap with unsupported KDF parameters never reaches,
// because deriving the KEK validates them first. So one entry written by a newer
// build, hand-edited, or bit-flipped in a cost field used to abort the whole
// loop, and every user's perfectly good password wrap went untried: password
// sign-in dead for everybody, with only a recovery code left. Skipping the bad
// wrap costs nothing — an unopenable wrap is exactly as useless either way.
//
// The deferred error is reported only when no wrap was usable at all, so a
// genuine wrong password still reads as ErrWrongKey rather than as corruption,
// and an operator whose keyring really is damaged still gets told why.
func (kr *Keyring) Unlock(c Credential) (crypt.Key, string, error) {
	if kr == nil || len(kr.Wraps) == 0 {
		return crypt.Key{}, "", ErrNoKeyring
	}
	var (
		tried    bool
		deferred error
	)
	for _, w := range kr.Wraps {
		kek, usable, err := kekFor(c, w)
		if err != nil {
			if deferred == nil {
				deferred = fmt.Errorf("vault: wrap %q is unusable: %w", w.ID, err)
			}
			continue
		}
		if !usable {
			continue
		}
		aad, err := wrapAAD(w)
		if err != nil {
			if deferred == nil {
				deferred = fmt.Errorf("vault: wrap %q is unusable: %w", w.ID, err)
			}
			continue
		}
		tried = true
		mk, err := crypt.UnwrapKey(kek, w.CT, aad)
		kek.Zero()
		if err != nil {
			continue
		}
		if kr.MKFP != "" && crypt.KeyID(mk) != kr.MKFP {
			// The wrap authenticated but yields a different master key than the
			// rest of the document: this keyring was stitched together from two
			// vaults and using it would encrypt new data under a key the other
			// wraps cannot open.
			mk.Zero()
			return crypt.Key{}, "", fmt.Errorf("%w: wrap %q holds a foreign master key", ErrCorrupt, w.ID)
		}
		return mk, w.ID, nil
	}
	if !tried && deferred != nil {
		return crypt.Key{}, "", deferred
	}
	return crypt.Key{}, "", ErrWrongKey
}

// NewKeyring creates a keyring around a fresh master key, sealed under one
// initial credential plus a recovery code.
//
// The recovery code is returned once and never stored in recoverable form. It is
// mandatory rather than optional because every other wrap is derived from
// something a user can forget or lose, and there is deliberately no operator
// override.
func NewKeyring(userID, password string) (*Keyring, crypt.Key, string, error) {
	mk, err := crypt.NewKey()
	if err != nil {
		return nil, crypt.Key{}, "", err
	}
	salt, err := newSalt()
	if err != nil {
		return nil, crypt.Key{}, "", err
	}

	kr := &Keyring{Version: keyringVersion, Salt: salt, MKFP: crypt.KeyID(mk)}

	if err := kr.AddPassword(mk, userID, password); err != nil {
		return nil, crypt.Key{}, "", err
	}
	code, err := kr.AddRecoveryCode(mk)
	if err != nil {
		return nil, crypt.Key{}, "", err
	}
	return kr, mk, code, nil
}

func newSalt() ([]byte, error) {
	salt := make([]byte, crypt.SaltLen)
	if err := randRead(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// AddPassword seals the master key under a user's password, replacing any
// existing password wrap for that user.
func (kr *Keyring) AddPassword(mk crypt.Key, userID, password string) error {
	if password == "" {
		return errors.New("vault: empty password")
	}
	params, err := crypt.NewKDFParams()
	if err != nil {
		return err
	}
	kek, err := crypt.DeriveKEK(password, params)
	if err != nil {
		return err
	}
	defer kek.Zero()

	w := Wrap{ID: wrapID(userID, "pw"), User: userID, Type: WrapPassword, KDF: &params}
	return kr.sealInto(&w, kek, mk)
}

// AddPasskey seals the master key under a WebAuthn PRF secret.
func (kr *Keyring) AddPasskey(mk crypt.Key, userID, credID string, prf []byte) error {
	kek, err := crypt.PasskeyKEK(prf)
	if err != nil {
		return err
	}
	defer kek.Zero()

	w := Wrap{ID: wrapID(userID, "pk-"+shortID(credID)), User: userID, Type: WrapPasskey, CredID: credID}
	return kr.sealInto(&w, kek, mk)
}

// AddRecoveryCode mints a new recovery code and seals the master key under it.
func (kr *Keyring) AddRecoveryCode(mk crypt.Key) (string, error) {
	code, err := crypt.NewRecoveryCode()
	if err != nil {
		return "", err
	}
	kek, err := crypt.RecoveryKEK(code)
	if err != nil {
		return "", err
	}
	defer kek.Zero()

	w := Wrap{ID: "rc-" + crypt.RecoveryHint(code), Type: WrapRecovery, Hint: crypt.RecoveryHint(code)}
	if err := kr.sealInto(&w, kek, mk); err != nil {
		return "", err
	}
	return code, nil
}

func (kr *Keyring) sealInto(w *Wrap, kek, mk crypt.Key) error {
	if kr.MKFP == "" {
		kr.MKFP = crypt.KeyID(mk)
	} else if crypt.KeyID(mk) != kr.MKFP {
		return fmt.Errorf("vault: refusing to add a wrap for a different master key")
	}
	aad, err := wrapAAD(*w)
	if err != nil {
		return err
	}
	ct, err := crypt.WrapKey(kek, mk, aad)
	if err != nil {
		return err
	}
	w.CT = ct
	kr.replace(*w)
	return nil
}

func (kr *Keyring) replace(w Wrap) {
	for i := range kr.Wraps {
		if kr.Wraps[i].ID == w.ID {
			kr.Wraps[i] = w
			return
		}
	}
	kr.Wraps = append(kr.Wraps, w)
}

// RemoveWrapsForUser drops every wrap belonging to a user, refusing to leave the
// keyring with no way in.
func (kr *Keyring) RemoveWrapsForUser(userID string) error {
	kept := make([]Wrap, 0, len(kr.Wraps))
	for _, w := range kr.Wraps {
		if w.User != userID || w.User == "" {
			kept = append(kept, w)
		}
	}
	if len(kept) == len(kr.Wraps) {
		return nil
	}
	if len(kept) == 0 {
		return ErrLastWrap
	}
	kr.Wraps = kept
	return nil
}

// RemoveBootstrapWrap deletes the credential NewKeyring("", password) leaves
// behind, once a real user credential exists.
//
// A vault created before any account exists — `vault init`, run by an operator
// or by an orchestrator with a password supplied at order time — gets a wrap
// with no user. RemoveWrapsForUser keeps it on purpose: until somebody is
// enrolled it is one of only two ways in.
//
// The moment a real credential exists it stops being a fallback and becomes a
// standing one. That password passed through the memory of whatever created the
// instance, and through the environment of a container that anyone holding the
// daemon socket could inspect for the seconds it lived. Leaving its wrap in
// place would make it a valid key to the whole archive forever, and would mean
// a user who changed their password had revoked nothing.
//
// Two conditions guard the removal rather than the one strictly needed. Another
// wrap must survive, so a failure between creating the vault and enrolling the
// first account leaves the volume openable instead of stranded. And one of the
// survivors must belong to a user: the recovery code alone is not a credential
// anyone is guaranteed to still have, since it is printed once and whoever ran
// the command may not have kept it. Callers that enroll first satisfy both.
func (kr *Keyring) RemoveBootstrapWrap() error {
	bootstrapID := wrapID("", "pw")

	kept := make([]Wrap, 0, len(kr.Wraps))
	removed := false
	userWrapSurvives := false
	for _, w := range kr.Wraps {
		if w.User == "" && w.Type == WrapPassword && w.ID == bootstrapID {
			removed = true
			continue
		}
		if w.User != "" {
			userWrapSurvives = true
		}
		kept = append(kept, w)
	}

	if !removed {
		return nil
	}
	if len(kept) == 0 || !userWrapSurvives {
		return ErrLastWrap
	}
	kr.Wraps = kept
	return nil
}

// HasWrapForUser reports whether a user can currently unlock the instance.
func (kr *Keyring) HasWrapForUser(userID string) bool {
	for _, w := range kr.Wraps {
		if w.User == userID {
			return true
		}
	}
	return false
}

func wrapID(userID, slot string) string {
	if userID == "" {
		return slot
	}
	return "u_" + userID + ":" + slot
}

func shortID(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, s)
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "1"
	}
	return s
}

// LoadKeyring reads the keyring from a vault directory.
func LoadKeyring(dir string) (*Keyring, error) {
	b, err := os.ReadFile(filepath.Join(dir, keyringName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoKeyring
	}
	if err != nil {
		return nil, err
	}
	var kr Keyring
	if err := json.Unmarshal(b, &kr); err != nil {
		return nil, fmt.Errorf("vault: parse %s: %w", keyringName, err)
	}
	if kr.Version != keyringVersion {
		return nil, fmt.Errorf("vault: keyring version %d is not supported", kr.Version)
	}
	if len(kr.Salt) < crypt.SaltLen {
		return nil, fmt.Errorf("vault: keyring salt is %d bytes, need %d", len(kr.Salt), crypt.SaltLen)
	}
	if len(kr.Wraps) == 0 {
		return nil, fmt.Errorf("vault: keyring has no wraps")
	}
	return &kr, nil
}

// Save writes the keyring atomically.
func (kr *Keyring) Save(dir string) error {
	b, err := json.MarshalIndent(kr, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, keyringName), append(b, '\n'), 0o600)
}

// Subkeys derives the purpose-separated keys used to seal vault contents.
func (kr *Keyring) Subkeys(mk crypt.Key) (blob, manifest, name crypt.Key, err error) {
	if blob, err = crypt.Subkey(mk, kr.Salt, crypt.InfoBlob); err != nil {
		return
	}
	if manifest, err = crypt.Subkey(mk, kr.Salt, crypt.InfoManifest); err != nil {
		return
	}
	name, err = crypt.Subkey(mk, kr.Salt, crypt.InfoBlobName)
	return
}
