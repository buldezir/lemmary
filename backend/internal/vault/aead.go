package vault

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"lemmary/backend/internal/crypt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Stream framing.
//
// A sealed stream is a 6-byte header followed by one or more chunks:
//
//	header: "LMVB" | version(1) | chunkSizeLog2(1)
//	chunk:  nonce(24) | AEAD(plaintext chunk)+tag(16)
//
// Chunking keeps memory flat — a 20 MiB upload is never fully resident — and
// bounds the blast radius of a corrupt region to one chunk rather than the whole
// archive.
//
// Every chunk authenticates additional data covering the stream version, its
// kind, the stream identity, the chunk index, and whether it is the final chunk.
// That closes the three attacks a naive per-chunk AEAD leaves open:
//
//   - truncation, because the new last chunk was sealed with isFinal=0 and will
//     not authenticate as isFinal=1;
//   - reordering, because the index is bound;
//   - splicing a chunk out of a different blob, because the stream id is bound.
//
// A stream always carries at least one chunk, so an empty payload is
// distinguishable from a stream truncated down to its header.
const (
	streamMagic     = "LMVB"
	streamVersion   = 1
	chunkSizeLog2   = 20 // 1 MiB
	chunkSize       = 1 << chunkSizeLog2
	streamHeaderLen = len(streamMagic) + 2
)

// Stream kinds, bound into the additional data so a manifest can never be
// opened as a blob or vice versa.
const (
	kindBlob     byte = 1
	kindManifest byte = 2
)

// StreamID identifies a sealed stream. For blobs it is the keyed content
// address; for manifests it is derived from the generation number.
type StreamID [32]byte

// ErrCorrupt reports a sealed stream that failed authentication or whose
// framing is inconsistent. As in the crypt package it deliberately does not
// distinguish wrong-key from tampered-with.
var ErrCorrupt = errors.New("vault: sealed stream failed authentication")

// chunkAAD builds the additional data for one chunk.
func chunkAAD(kind byte, id StreamID, index uint32, final bool) []byte {
	aad := make([]byte, 0, 1+1+len(id)+4+1)
	aad = append(aad, streamVersion, kind)
	aad = append(aad, id[:]...)
	aad = binary.BigEndian.AppendUint32(aad, index)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return aad
}

// SealStream encrypts everything readable from src into dst.
//
// It returns the number of plaintext bytes consumed.
func SealStream(dst io.Writer, src io.Reader, key crypt.Key, kind byte, id StreamID) (int64, error) {
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return 0, err
	}

	header := make([]byte, 0, streamHeaderLen)
	header = append(header, streamMagic...)
	header = append(header, streamVersion, chunkSizeLog2)
	if _, err := dst.Write(header); err != nil {
		return 0, err
	}

	var (
		total   int64
		index   uint32
		plain   = make([]byte, chunkSize)
		sealed  = make([]byte, 0, chunkSize+aead.Overhead())
		nonce   = make([]byte, aead.NonceSize())
		pending []byte
		havePkt bool
	)

	// Read one chunk ahead so the final chunk can be flagged as such.
	flush := func(buf []byte, final bool) error {
		if err := randRead(nonce); err != nil {
			return err
		}
		sealed = aead.Seal(sealed[:0], nonce, buf, chunkAAD(kind, id, index, final))
		if _, err := dst.Write(nonce); err != nil {
			return err
		}
		if _, err := dst.Write(sealed); err != nil {
			return err
		}
		index++
		return nil
	}

	for {
		n, readErr := io.ReadFull(src, plain)
		if n > 0 {
			if havePkt {
				if err := flush(pending, false); err != nil {
					return total, err
				}
			}
			pending = append(pending[:0], plain[:n]...)
			havePkt = true
			total += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return total, readErr
		}
	}

	// Always emit a final chunk, even for empty input.
	if !havePkt {
		pending = pending[:0]
	}
	if err := flush(pending, true); err != nil {
		return total, err
	}
	return total, nil
}

// OpenStream decrypts a stream produced by SealStream into dst.
//
// It returns the number of plaintext bytes written.
func OpenStream(dst io.Writer, src io.Reader, key crypt.Key, kind byte, id StreamID) (int64, error) {
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return 0, err
	}

	encChunk := aead.NonceSize() + chunkSize + aead.Overhead()
	br := bufio.NewReaderSize(src, encChunk+1)

	header := make([]byte, streamHeaderLen)
	if _, err := io.ReadFull(br, header); err != nil {
		return 0, fmt.Errorf("%w: short header", ErrCorrupt)
	}
	if string(header[:len(streamMagic)]) != streamMagic {
		return 0, fmt.Errorf("%w: bad magic", ErrCorrupt)
	}
	if header[len(streamMagic)] != streamVersion {
		return 0, fmt.Errorf("%w: unsupported stream version %d", ErrCorrupt, header[len(streamMagic)])
	}
	gotLog2 := header[len(streamMagic)+1]
	if gotLog2 != chunkSizeLog2 {
		return 0, fmt.Errorf("%w: unsupported chunk size 2^%d", ErrCorrupt, gotLog2)
	}

	var (
		total  int64
		index  uint32
		buf    = make([]byte, encChunk)
		opened = make([]byte, 0, chunkSize)
	)

	for {
		n, readErr := io.ReadFull(br, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			if readErr == io.EOF {
				// A well-formed stream always ends on a final chunk, which is
				// consumed below; reaching EOF here means it was truncated.
				return total, fmt.Errorf("%w: stream ended without a final chunk", ErrCorrupt)
			}
			return total, readErr
		}
		if n < aead.NonceSize()+aead.Overhead() {
			return total, fmt.Errorf("%w: runt chunk", ErrCorrupt)
		}

		// If nothing follows, this is the final chunk.
		final := false
		if _, err := br.Peek(1); err != nil {
			if err != io.EOF {
				return total, err
			}
			final = true
		}

		nonce, ct := buf[:aead.NonceSize()], buf[aead.NonceSize():n]
		opened, err = aead.Open(opened[:0], nonce, ct, chunkAAD(kind, id, index, final))
		if err != nil {
			return total, fmt.Errorf("%w: chunk %d", ErrCorrupt, index)
		}
		if len(opened) > 0 {
			if _, err := dst.Write(opened); err != nil {
				return total, err
			}
			total += int64(len(opened))
		}
		index++

		if final {
			return total, nil
		}
	}
}
