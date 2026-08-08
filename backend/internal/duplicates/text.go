package duplicates

import (
	"encoding/binary"
	"hash/fnv"
	"math/bits"
	"strings"
	"unicode"
)

const (
	// MaxHammingDistance is the SimHash prefilter tolerance before Jaccard confirm.
	MaxHammingDistance = 3
	shingleSize        = 3
	minNormalizedRunes = 40
)

// NormalizeText lowercases, strips punctuation, and collapses whitespace.
func NormalizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// WordShingles returns overlapping word n-grams.
func WordShingles(normalized string, n int) []string {
	if n <= 0 {
		n = shingleSize
	}
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= n {
		return []string{strings.Join(words, " ")}
	}
	out := make([]string, 0, len(words)-n+1)
	for i := 0; i+n <= len(words); i++ {
		out = append(out, strings.Join(words[i:i+n], " "))
	}
	return out
}

// JaccardSimilarity is |A∩B| / |A∪B| over string sets.
func JaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, s := range a {
		setA[s] = struct{}{}
	}
	intersection := 0
	setB := make(map[string]struct{}, len(b))
	for _, s := range b {
		if _, ok := setB[s]; ok {
			continue
		}
		setB[s] = struct{}{}
		if _, ok := setA[s]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// SimHash64 computes a 64-bit SimHash over word shingles of normalized text.
func SimHash64(normalized string) uint64 {
	shingles := WordShingles(normalized, shingleSize)
	if len(shingles) == 0 {
		return 0
	}
	var weights [64]int
	for _, shingle := range shingles {
		h := fnv.New64a()
		_, _ = h.Write([]byte(shingle))
		v := h.Sum64()
		for i := 0; i < 64; i++ {
			if (v>>uint(i))&1 == 1 {
				weights[i]++
			} else {
				weights[i]--
			}
		}
	}
	var out uint64
	for i := 0; i < 64; i++ {
		if weights[i] >= 0 {
			out |= 1 << uint(i)
		}
	}
	return out
}

// FingerprintHex returns the 16-char hex SimHash for OCR text (empty if too short).
func FingerprintHex(ocrText string) string {
	normalized := NormalizeText(ocrText)
	if runeLen(normalized) < minNormalizedRunes {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], SimHash64(normalized))
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i, b := range buf {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

// ParseFingerprintHex parses a 16-char hex fingerprint.
func ParseFingerprintHex(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 16 {
		return 0, false
	}
	var buf [8]byte
	for i := 0; i < 8; i++ {
		hi, ok1 := fromHex(s[i*2])
		lo, ok2 := fromHex(s[i*2+1])
		if !ok1 || !ok2 {
			return 0, false
		}
		buf[i] = (hi << 4) | lo
	}
	return binary.BigEndian.Uint64(buf[:]), true
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// HammingDistance returns the bit distance between two 64-bit fingerprints.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// TextSimilarity returns Jaccard similarity of word trigrams after normalization.
// Returns 0 when either side is too short for a reliable comparison.
func TextSimilarity(a, b string) float64 {
	na := NormalizeText(a)
	nb := NormalizeText(b)
	if runeLen(na) < minNormalizedRunes || runeLen(nb) < minNormalizedRunes {
		return 0
	}
	return JaccardSimilarity(WordShingles(na, shingleSize), WordShingles(nb, shingleSize))
}

// IsNearDuplicate reports whether OCR texts are near-duplicates at the given threshold.
func IsNearDuplicate(a, b string, threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.92
	}
	fa := FingerprintHex(a)
	fb := FingerprintHex(b)
	if fa == "" || fb == "" {
		return false
	}
	ua, okA := ParseFingerprintHex(fa)
	ub, okB := ParseFingerprintHex(fb)
	if !okA || !okB {
		return false
	}
	if HammingDistance(ua, ub) > MaxHammingDistance {
		return false
	}
	return TextSimilarity(a, b) >= threshold
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
