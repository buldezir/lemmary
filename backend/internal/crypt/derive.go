package crypt

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// Info strings for subkey derivation. Each distinct use of a master key gets its
// own, so no two uses ever share key material.
const (
	infoKeyID    = "lemmary/kid/v1"
	InfoBlob     = "lemmary/vault/v1/blob"
	InfoManifest = "lemmary/vault/v1/manifest"
	InfoBlobName = "lemmary/vault/v1/blobname"
	InfoVerifier = "lemmary/vault/v1/verifier"
)

// Subkey derives a purpose-separated subkey from a master key.
func Subkey(master Key, salt []byte, info string) (Key, error) {
	out, err := hkdf.Key(sha256.New, master[:], salt, info, KeyLen)
	if err != nil {
		return Key{}, err
	}
	var k Key
	copy(k[:], out)
	clear(out)
	return k, nil
}

// subkey is the unexported spelling used by helpers within this package.
func subkey(master Key, salt []byte, info string) (Key, error) {
	return Subkey(master, salt, info)
}
