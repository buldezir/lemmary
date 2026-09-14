package vault

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lemmary/backend/internal/crypt"
)

const (
	manifestVersion = 1
	currentName     = "CURRENT"
	manifestPrefix  = "manifest."
	manifestSuffix  = ".enc"
	// keepGenerations bounds how far back a rollback can reach. Three survives "the
	// newest manifest is unreadable" twice over without letting dead blobs pile up.
	keepGenerations = 3
)

// Entry.Size and MTime are not integrity metadata, the AEAD tag is. They let a
// flush skip re-hashing an unchanged file, which keeps a steady-state flush
// proportional to new data rather than to the whole archive.
type Entry struct {
	Path  string `json:"p"`
	Size  int64  `json:"s"`
	MTime int64  `json:"m"`
	Mode  uint32 `json:"o"`
	Blob  string `json:"b"`
}

// Manifest is the file table for one generation.
type Manifest struct {
	Version int     `json:"v"`
	Gen     uint64  `json:"g"`
	Created int64   `json:"c"`
	Entries []Entry `json:"e"`
}

func (e Entry) blobID() (StreamID, error) {
	raw, err := hex.DecodeString(e.Blob)
	if err != nil || len(raw) != len(StreamID{}) {
		return StreamID{}, fmt.Errorf("%w: entry %q has a malformed blob address", ErrCorrupt, e.Path)
	}
	var id StreamID
	copy(id[:], raw)
	return id, nil
}

func (m *Manifest) TotalSize() int64 {
	var n int64
	for _, e := range m.Entries {
		n += e.Size
	}
	return n
}

// index maps paths to entries for the unchanged-file fast path.
func (m *Manifest) index() map[string]Entry {
	if m == nil {
		return map[string]Entry{}
	}
	idx := make(map[string]Entry, len(m.Entries))
	for _, e := range m.Entries {
		idx[e.Path] = e
	}
	return idx
}

// hasDatabases is false for a nil manifest, the never-flushed vault, which
// correctly carries no databases.
func (m *Manifest) hasDatabases() bool {
	if m == nil {
		return false
	}
	for _, e := range m.Entries {
		for _, db := range databaseFiles {
			if e.Path == db {
				return true
			}
		}
	}
	return false
}

// manifestStreamID binds a manifest's ciphertext to its generation, so an old
// manifest cannot be renamed forward over a newer one.
func manifestStreamID(gen uint64) StreamID {
	var id StreamID
	h := sha256.New()
	h.Write([]byte("lemmary/vault/manifest/v1"))
	_ = binary.Write(h, binary.BigEndian, gen)
	copy(id[:], h.Sum(nil))
	return id
}

func manifestPath(dir string, gen uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%s%06d%s", manifestPrefix, gen, manifestSuffix))
}

func writeManifest(dir string, m *Manifest, key crypt.Key) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeStreamAtomic(manifestPath(dir, m.Gen), 0o600, func(w io.Writer) error {
		_, err := SealStream(w, bytes.NewReader(body), key, kindManifest, manifestStreamID(m.Gen))
		return err
	})
}

func readManifest(dir string, gen uint64, key crypt.Key) (*Manifest, error) {
	f, err := os.Open(manifestPath(dir, gen))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	if _, err := OpenStream(&body, f, key, kindManifest, manifestStreamID(gen)); err != nil {
		return nil, fmt.Errorf("vault: manifest %d: %w", gen, err)
	}
	var m Manifest
	if err := json.Unmarshal(body.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("%w: manifest %d is not valid json", ErrCorrupt, gen)
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("vault: manifest version %d is not supported", m.Version)
	}
	if m.Gen != gen {
		// A renamed manifest. The AAD makes this unreachable in practice; the check is
		// here so a future format change cannot make it reachable.
		return nil, fmt.Errorf("%w: manifest %d claims generation %d", ErrCorrupt, gen, m.Gen)
	}
	return &m, nil
}

// listGenerations returns every manifest generation present, newest first.
func listGenerations(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var gens []uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, manifestPrefix) || !strings.HasSuffix(name, manifestSuffix) {
			continue
		}
		digits := strings.TrimSuffix(strings.TrimPrefix(name, manifestPrefix), manifestSuffix)
		gen, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			continue
		}
		gens = append(gens, gen)
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i] > gens[j] })
	return gens, nil
}

// currentMAC guards CURRENT against accidental corruption rather than an
// attacker, who could simply delete newer manifests: the manifest it names is
// itself authenticated and carries its own generation. True rollback protection
// is not achievable against someone who controls the volume.
func currentMAC(key crypt.Key, gen uint64) string {
	mac := hmac.New(sha256.New, key[:])
	fmt.Fprintf(mac, "lemmary/vault/current/v1|%d", gen)
	return hex.EncodeToString(mac.Sum(nil))
}

func writeCurrent(dir string, gen uint64, key crypt.Key) error {
	body := fmt.Sprintf("gen=%d\nmac=%s\n", gen, currentMAC(key, gen))
	return writeFileAtomic(filepath.Join(dir, currentName), []byte(body), 0o600)
}

// readCurrent returns false when CURRENT is absent or unauthentic.
func readCurrent(dir string, key crypt.Key) (uint64, bool) {
	b, err := os.ReadFile(filepath.Join(dir, currentName))
	if err != nil {
		return 0, false
	}
	var gen uint64
	var mac string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "gen":
			parsed, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, false
			}
			gen = parsed
		case "mac":
			mac = v
		}
	}
	if mac == "" || !hmac.Equal([]byte(mac), []byte(currentMAC(key, gen))) {
		return 0, false
	}
	return gen, true
}

// loadLatest starts from CURRENT and walks back through retained generations, so
// one unreadable manifest costs a flush interval rather than the archive. Each
// fallback is loud: silently serving stale data is the worst outcome here.
func loadLatest(dir string, key crypt.Key, warn func(string, ...any)) (*Manifest, error) {
	gens, err := listGenerations(dir)
	if err != nil {
		return nil, err
	}
	if len(gens) == 0 {
		return nil, nil
	}

	if cur, ok := readCurrent(dir, key); ok {
		// Try CURRENT first, then anything older.
		ordered := []uint64{cur}
		for _, g := range gens {
			if g != cur {
				ordered = append(ordered, g)
			}
		}
		gens = ordered
	} else {
		warn("vault: CURRENT is missing or unauthentic; falling back to the newest readable manifest")
	}

	var firstErr error
	for i, gen := range gens {
		m, err := readManifest(dir, gen, key)
		if err == nil {
			if i > 0 {
				warn("vault: recovered on generation %d after %d unreadable manifest(s)", gen, i)
			}
			return m, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		warn("vault: manifest generation %d is unreadable: %v", gen, err)
	}
	return nil, fmt.Errorf("vault: no readable manifest: %w", firstErr)
}

func liveBlobs(dir string, key crypt.Key, keep []uint64) (map[StreamID]bool, error) {
	live := map[StreamID]bool{}
	for _, gen := range keep {
		m, err := readManifest(dir, gen, key)
		if err != nil {
			// An unreadable retained generation must not have its blobs collected: they may
			// be the only copy the fallback path can use.
			return nil, fmt.Errorf("vault: cannot compute the live set, generation %d is unreadable: %w", gen, err)
		}
		for _, e := range m.Entries {
			id, err := e.blobID()
			if err != nil {
				return nil, err
			}
			live[id] = true
		}
	}
	return live, nil
}

// pruneGenerations deletes manifests older than the retained window and returns
// the generations that remain.
func pruneGenerations(dir string, keepN int) ([]uint64, error) {
	gens, err := listGenerations(dir)
	if err != nil {
		return nil, err
	}
	if len(gens) <= keepN {
		return gens, nil
	}
	for _, gen := range gens[keepN:] {
		if err := os.Remove(manifestPath(dir, gen)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return gens[:keepN], nil
}
