package vault

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"lemmary/backend/internal/crypt"
)

func testKey(t *testing.T) crypt.Key {
	t.Helper()
	k, err := crypt.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func testID(seed byte) StreamID {
	var id StreamID
	for i := range id {
		id[i] = seed ^ byte(i)
	}
	return id
}

func seal(t *testing.T, key crypt.Key, id StreamID, plain []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	n, err := SealStream(&out, bytes.NewReader(plain), key, kindBlob, id)
	if err != nil {
		t.Fatalf("SealStream: %v", err)
	}
	if n != int64(len(plain)) {
		t.Fatalf("SealStream consumed %d bytes, want %d", n, len(plain))
	}
	return out.Bytes()
}

func open(key crypt.Key, id StreamID, sealed []byte) ([]byte, error) {
	var out bytes.Buffer
	_, err := OpenStream(&out, bytes.NewReader(sealed), key, kindBlob, id)
	return out.Bytes(), err
}

func TestSealOpenRoundTripAcrossSizes(t *testing.T) {
	key := testKey(t)
	id := testID(0x11)

	sizes := []int{0, 1, 2, 4095, 4096, chunkSize - 1, chunkSize, chunkSize + 1, 2 * chunkSize, 4*chunkSize + 7}
	for _, size := range sizes {
		plain := make([]byte, size)
		if _, err := rand.New(rand.NewSource(int64(size))).Read(plain); err != nil {
			t.Fatalf("fill: %v", err)
		}

		sealed := seal(t, key, id, plain)
		got, err := open(key, id, sealed)
		if err != nil {
			t.Fatalf("size %d: OpenStream: %v", size, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("size %d: round trip mismatch (got %d bytes)", size, len(got))
		}
	}
}

func TestSealStreamIsNonDeterministic(t *testing.T) {
	key, id := testKey(t), testID(0x22)
	plain := []byte("the same plaintext twice")
	if bytes.Equal(seal(t, key, id, plain), seal(t, key, id, plain)) {
		t.Fatal("two seals of the same input are identical; nonces are not random")
	}
}

func TestSealedStreamLeaksNoPlaintext(t *testing.T) {
	key, id := testKey(t), testID(0x33)

	// Low-entropy input is the case where a framing bug would show up as
	// recognisable structure in the output.
	plain := bytes.Repeat([]byte("A"), 3*chunkSize+11)
	sealed := seal(t, key, id, plain)

	if bytes.Contains(sealed[streamHeaderLen:], bytes.Repeat([]byte("A"), 64)) {
		t.Fatal("sealed stream contains a run of plaintext")
	}
	// No 64-byte run of any single byte should survive encryption.
	run, last := 1, byte(0)
	for i, b := range sealed[streamHeaderLen:] {
		if i > 0 && b == last {
			run++
			if run >= 64 {
				t.Fatalf("sealed stream has a %d-byte run of 0x%02x at offset %d", run, b, i)
			}
		} else {
			run = 1
		}
		last = b
	}
}

func TestOpenStreamRejectsWrongKeyIDAndKind(t *testing.T) {
	key, id := testKey(t), testID(0x44)
	plain := bytes.Repeat([]byte("payload"), 1000)
	sealed := seal(t, key, id, plain)

	if _, err := open(testKey(t), id, sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong key: got %v, want ErrCorrupt", err)
	}
	if _, err := open(key, testID(0x45), sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong stream id: got %v, want ErrCorrupt", err)
	}

	var out bytes.Buffer
	if _, err := OpenStream(&out, bytes.NewReader(sealed), key, kindManifest, id); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong kind: got %v, want ErrCorrupt", err)
	}
}

// A blob sealed under one id must not open under another even when the attacker
// controls both, which is what stops a chunk being spliced between blobs.
func TestOpenStreamRejectsSplicedChunk(t *testing.T) {
	key := testKey(t)
	idA, idB := testID(0x55), testID(0x66)

	plain := bytes.Repeat([]byte("x"), 2*chunkSize)
	a := seal(t, key, idA, plain)
	b := seal(t, key, idB, plain)

	encChunk := 24 + chunkSize + 16
	spliced := append([]byte(nil), a...)
	copy(spliced[streamHeaderLen:streamHeaderLen+encChunk], b[streamHeaderLen:streamHeaderLen+encChunk])

	if _, err := open(key, idA, spliced); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("spliced chunk from another blob opened: %v", err)
	}
}

func TestOpenStreamRejectsReorderedChunks(t *testing.T) {
	key, id := testKey(t), testID(0x77)
	plain := bytes.Repeat([]byte("y"), 3*chunkSize)
	sealed := seal(t, key, id, plain)

	encChunk := 24 + chunkSize + 16
	swapped := append([]byte(nil), sealed...)
	c0 := streamHeaderLen
	c1 := streamHeaderLen + encChunk
	tmp := append([]byte(nil), swapped[c0:c0+encChunk]...)
	copy(swapped[c0:c0+encChunk], swapped[c1:c1+encChunk])
	copy(swapped[c1:c1+encChunk], tmp)

	if _, err := open(key, id, swapped); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("reordered chunks opened: %v", err)
	}
}

// Truncation is the attack the isFinal flag exists to stop: dropping trailing
// chunks must not yield a shorter but valid-looking document.
func TestOpenStreamRejectsTruncation(t *testing.T) {
	key, id := testKey(t), testID(0x88)
	plain := bytes.Repeat([]byte("z"), 3*chunkSize)
	sealed := seal(t, key, id, plain)

	encChunk := 24 + chunkSize + 16
	for _, cut := range []int{
		streamHeaderLen,                 // header only
		streamHeaderLen + encChunk,      // one chunk short
		streamHeaderLen + 2*encChunk,    // two chunks short
		len(sealed) - 1,                 // final tag clipped
		len(sealed) - 16,                // whole final tag gone
		streamHeaderLen + encChunk + 10, // mid-chunk
	} {
		if _, err := open(key, id, sealed[:cut]); err == nil {
			t.Fatalf("truncation to %d bytes still opened", cut)
		}
	}

	// A stream cut down to nothing must not read as an empty payload.
	if _, err := open(key, id, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("empty input: got %v, want ErrCorrupt", err)
	}
}

// An empty payload is legitimate and must round-trip, so it has to stay
// distinguishable from a header-only truncation (covered above).
func TestEmptyPayloadRoundTripsAndIsNotHeaderOnly(t *testing.T) {
	key, id := testKey(t), testID(0x99)
	sealed := seal(t, key, id, nil)

	if len(sealed) <= streamHeaderLen {
		t.Fatal("empty payload produced no chunk; truncation would be undetectable")
	}
	got, err := open(key, id, sealed)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty payload round-tripped to %d bytes", len(got))
	}
}

// Every byte matters. Flipping a bit anywhere in the stream must be detected;
// offsets are sampled rather than exhaustive so the suite stays fast, but the
// sample covers all framing-critical positions plus a spread of body bytes.
func TestOpenStreamDetectsBitFlipAnywhere(t *testing.T) {
	key, id := testKey(t), testID(0xAA)
	plain := bytes.Repeat([]byte("tamper"), chunkSize) // ~2.9 chunks
	sealed := seal(t, key, id, plain)

	encChunk := 24 + chunkSize + 16
	offsets := map[int]bool{}
	for i := 0; i < streamHeaderLen; i++ {
		offsets[i] = true // magic, version, chunk size
	}
	for c := 0; c*encChunk+streamHeaderLen < len(sealed); c++ {
		base := streamHeaderLen + c*encChunk
		for _, o := range []int{0, 23, 24, 25, encChunk / 2, encChunk - 17, encChunk - 16, encChunk - 1} {
			if base+o < len(sealed) {
				offsets[base+o] = true
			}
		}
	}
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 150; i++ {
		offsets[rnd.Intn(len(sealed))] = true
	}

	for off := range offsets {
		bad := append([]byte(nil), sealed...)
		bad[off] ^= 0x01
		got, err := open(key, id, bad)
		if err == nil {
			t.Fatalf("bit flip at offset %d/%d was not detected", off, len(sealed))
		}
		if bytes.Equal(got, plain) {
			t.Fatalf("bit flip at offset %d returned the full plaintext", off)
		}
	}
}

func TestOpenStreamRejectsBadHeader(t *testing.T) {
	key, id := testKey(t), testID(0xBB)
	sealed := seal(t, key, id, []byte("hello"))

	cases := map[string]func([]byte){
		"bad magic":          func(b []byte) { b[0] = 'X' },
		"future version":     func(b []byte) { b[4] = 99 },
		"foreign chunk size": func(b []byte) { b[5] = 16 },
	}
	for name, mutate := range cases {
		bad := append([]byte(nil), sealed...)
		mutate(bad)
		if _, err := open(key, id, bad); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%s: got %v, want ErrCorrupt", name, err)
		}
	}

	if _, err := open(key, id, []byte("LMV")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short header: got %v, want ErrCorrupt", err)
	}
}

func TestErrCorruptMessagesCarryNoKeyMaterial(t *testing.T) {
	key, id := testKey(t), testID(0xCC)
	sealed := seal(t, key, id, []byte("secret payload"))
	sealed[len(sealed)-1] ^= 0xFF

	_, err := open(key, id, sealed)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), string(key[:])) || strings.Contains(err.Error(), "secret payload") {
		t.Fatalf("error leaks material: %v", err)
	}
}
