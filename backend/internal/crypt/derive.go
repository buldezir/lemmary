package crypt

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// Each distinct use of a master key gets its own info string, so no two uses
// share key material.
const (
	infoKeyID    = "lemmary/kid/v1"
	InfoBlob     = "lemmary/vault/v1/blob"
	InfoManifest = "lemmary/vault/v1/manifest"
	InfoBlobName = "lemmary/vault/v1/blobname"
	InfoVerifier = "lemmary/vault/v1/verifier"
)

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

func subkey(master Key, salt []byte, info string) (Key, error) {
	return Subkey(master, salt, info)
}
