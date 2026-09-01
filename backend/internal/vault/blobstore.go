package vault

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"lemmary/backend/internal/crypt"
)

// blobStore holds every file the vault protects, content-addressed and
// immutable.
//
// Immutability is what makes the commit protocol in flush.go safe without a
// journal: a blob is either fully written and fsynced before any manifest
// references it, or it is unreferenced garbage. Nothing is ever updated in
// place, so there is no torn-write window to reason about.
type blobStore struct {
	dir     string
	blobKey crypt.Key
	nameKey crypt.Key
}

// blobID is the keyed content address of a blob.
//
// It is an HMAC rather than a bare hash on purpose. An unkeyed content address
// turns the volume into a confirmation oracle: an attacker holding a suspected
// document could hash it and check whether this archive contains that exact
// file. Keying it means blob names say nothing without the master key.
func (s *blobStore) blobID(contentHash []byte) StreamID {
	mac := hmac.New(sha256.New, s.nameKey[:])
	mac.Write(contentHash)
	var id StreamID
	copy(id[:], mac.Sum(nil))
	return id
}

func (s *blobStore) path(id StreamID) string {
	h := hex.EncodeToString(id[:])
	return filepath.Join(s.dir, h[0:2], h[2:4], h)
}

// has reports whether a blob is already stored.
func (s *blobStore) has(id StreamID) bool {
	st, err := os.Stat(s.path(id))
	return err == nil && st.Mode().IsRegular()
}

// hashFile returns the SHA-256 of a file's contents.
//
// Sealing needs the blob id up front because the id is bound into every chunk's
// additional data, so the file is read twice: once to hash, once to encrypt.
// Both passes read from the memory-backed working directory, so the second read
// costs no disk I/O.
func hashFile(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

// putAttempts bounds the retry when a file is rewritten while it is being
// stored. Two passes losing the race twice in a row means something is
// rewriting that path continuously, which no number of retries will outlast.
const putAttempts = 3

// hookAfterHash lets a test rewrite a file in the window between the two passes,
// which is otherwise a race no test could hit on purpose. Nil in every build;
// the same seam flush.go uses to prove its commit ordering.
var hookAfterHash func()

// put stores a file, returning its blob id and whether it had to be written.
//
// The two passes are a race, and it is checked rather than assumed. A flush
// walks the working directory of a live system: between the pass that hashes a
// file and the pass that seals it, the application can rewrite that path — a
// regenerated thumbnail, an overwritten storage file — and the blob would then
// be stored under the content address of bytes it does not contain. Nothing
// downstream could detect that. The AEAD authenticates the blob against its id,
// and the blob is internally consistent; it is the *name* that lies. The damage
// surfaces much later and as silent corruption: some unrelated file whose
// content genuinely hashes to that address is uploaded, dedupe finds the address
// already present and reuses it, and after the next restart that document
// materialises holding the other file's bytes.
//
// So the seal pass re-hashes what it actually sealed and compares. This costs
// one SHA-256 over data already in memory, and turns an undetectable corruption
// into a retry.
func (s *blobStore) put(srcPath string) (StreamID, bool, error) {
	for attempt := 1; ; attempt++ {
		sum, _, err := hashFile(srcPath)
		if err != nil {
			return StreamID{}, false, err
		}
		id := s.blobID(sum)
		if s.has(id) {
			return id, false, nil
		}
		if hookAfterHash != nil {
			hookAfterHash()
		}

		var sealedSum []byte
		dst := s.path(id)
		err = writeStreamAtomic(dst, 0o600, func(w io.Writer) error {
			src, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer src.Close()
			h := sha256.New()
			if _, err := SealStream(w, io.TeeReader(src, h), s.blobKey, kindBlob, id); err != nil {
				return err
			}
			sealedSum = h.Sum(nil)
			return nil
		})
		if err != nil {
			return StreamID{}, false, fmt.Errorf("vault: seal %s: %w", srcPath, err)
		}
		if bytes.Equal(sum, sealedSum) {
			return id, true, nil
		}

		// The file changed between the passes. What was just written is a blob
		// whose name addresses content it does not hold, so it has to go before
		// anything can find it: flushes are serialised, and this id was absent a
		// moment ago, so nothing else can be relying on it.
		if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
			return StreamID{}, false, fmt.Errorf("vault: remove mis-addressed blob for %s: %w", srcPath, rmErr)
		}
		if attempt >= putAttempts {
			return StreamID{}, false, fmt.Errorf(
				"vault: %s was rewritten during each of %d attempts to store it", srcPath, putAttempts)
		}
	}
}

// get decrypts a blob to dstPath, creating parent directories as needed.
func (s *blobStore) get(id StreamID, dstPath string, mode os.FileMode) error {
	src, err := os.Open(s.path(id))
	if err != nil {
		return fmt.Errorf("vault: open blob %x: %w", id[:8], err)
	}
	defer src.Close()

	return writeStreamAtomic(dstPath, mode, func(w io.Writer) error {
		_, err := OpenStream(w, src, s.blobKey, kindBlob, id)
		return err
	})
}

// verify decrypts a blob and checks it re-hashes to its own address.
//
// This is what `vault verify` and the post-adoption read-back use: it proves the
// stored ciphertext really does yield the bytes its name claims, rather than
// merely authenticating.
func (s *blobStore) verify(id StreamID) error {
	src, err := os.Open(s.path(id))
	if err != nil {
		return err
	}
	defer src.Close()

	h := sha256.New()
	if _, err := OpenStream(h, src, s.blobKey, kindBlob, id); err != nil {
		return err
	}
	if got := s.blobID(h.Sum(nil)); got != id {
		return fmt.Errorf("%w: blob %x decrypts to content addressed %x", ErrCorrupt, id[:8], got[:8])
	}
	return nil
}

// gc removes every blob not referenced by the live set.
//
// The live set must be computed from *all* surviving manifests, not just the
// newest, or rolling back to a retained generation would find its blobs gone.
func (s *blobStore) gc(live map[StreamID]bool) (removed int, err error) {
	err = filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		raw, decErr := hex.DecodeString(filepath.Base(path))
		if decErr != nil || len(raw) != len(StreamID{}) {
			// Not a blob name: a leftover temp file from an interrupted write.
			return os.Remove(path)
		}
		var id StreamID
		copy(id[:], raw)
		if live[id] {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return rmErr
		}
		removed++
		return nil
	})
	return removed, err
}
