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
// immutable. Immutability is what makes the commit protocol in flush.go safe
// without a journal: a blob is either fully written and fsynced before any
// manifest references it, or it is unreferenced garbage.
type blobStore struct {
	dir     string
	blobKey crypt.Key
	nameKey crypt.Key
}

// blobID is an HMAC rather than a bare hash: an unkeyed content address turns
// the volume into a confirmation oracle, letting an attacker check whether a
// suspected document is in this archive.
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

func (s *blobStore) has(id StreamID) bool {
	st, err := os.Stat(s.path(id))
	return err == nil && st.Mode().IsRegular()
}

// hashFile is needed up front because the blob id is bound into every chunk's
// additional data, so the file is read twice. Both passes read from the
// memory-backed working directory, so the second costs no disk I/O.
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

// putAttempts bounds the retry when a file is rewritten while being stored.
// Losing the race twice running means something is rewriting that path
// continuously, which no number of retries will outlast.
const putAttempts = 3

// hookAfterHash lets a test rewrite a file in the window between the two passes,
// which is otherwise a race no test could hit on purpose. Nil in every build.
var hookAfterHash func()

// put stores a file, returning its blob id and whether it had to be written.
//
// The two passes are a race, and it is checked rather than assumed: a flush
// walks a live working directory, and the application can rewrite a path
// between the hash pass and the seal pass. The blob would then be stored under
// the content address of bytes it does not contain, which nothing downstream
// could detect, because the AEAD authenticates the blob against its id and it
// is the name that lies. It surfaces much later as silent corruption: some
// unrelated file that genuinely hashes to that address is uploaded, dedupe
// reuses the address, and that document materialises holding the other file's
// bytes. So the seal pass re-hashes what it sealed and compares, at the cost of
// one SHA-256 over data already in memory.
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

		// The file changed between the passes, so what was written is a blob whose name
		// addresses content it does not hold. Removing it is safe: flushes are
		// serialised and this id was absent a moment ago.
		if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
			return StreamID{}, false, fmt.Errorf("vault: remove mis-addressed blob for %s: %w", srcPath, rmErr)
		}
		if attempt >= putAttempts {
			return StreamID{}, false, fmt.Errorf(
				"vault: %s was rewritten during each of %d attempts to store it", srcPath, putAttempts)
		}
	}
}

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

// verify proves the stored ciphertext really yields the bytes its name claims,
// rather than merely authenticating. Used by `vault verify` and the
// post-adoption read-back.
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

// gc removes every blob not in the live set, which must be computed from all
// surviving manifests: from the newest alone, a rollback to a retained
// generation would find its blobs gone.
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
