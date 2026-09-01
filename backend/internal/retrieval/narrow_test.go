package retrieval

import (
	"strings"
	"testing"
)

// TestNarrowShrinksAChunkToItsMatch: a stored chunk is a block chosen for
// embedding, and quoting its first 600 bytes back is quoting whatever happened
// to start it.
func TestNarrowShrinksAChunkToItsMatch(t *testing.T) {
	head := strings.Repeat("Vorspann ohne Bedeutung. ", 30)
	text := head + "Die monatliche Kaltmiete beträgt 1234 EUR. " +
		strings.Repeat("Nachspann ohne Bedeutung. ", 30)

	chunk := ChunkHit{DocumentID: "doc1", Ord: 0, StartByte: 0, EndByte: len(text), Text: text}
	got := Narrow(text, "Kaltmiete", []ChunkHit{chunk})
	if len(got) != 1 {
		t.Fatalf("got = %#v", got)
	}
	if !strings.Contains(got[0].Text, "1234 EUR") {
		t.Fatalf("the narrowed passage lost the match: %q", got[0].Text)
	}
	if len(got[0].Text) >= len(text) {
		t.Fatalf("nothing was narrowed: %d of %d bytes", len(got[0].Text), len(text))
	}
	if got[0].StartByte < 0 || got[0].EndByte > len(text) || got[0].EndByte <= got[0].StartByte {
		t.Fatalf("offsets no longer point into the document: %+v", got[0])
	}
	if text[got[0].StartByte:got[0].EndByte] == "" {
		t.Fatal("offsets do not slice the text they came from")
	}
}

func TestNarrowKeepsWhatItCannotLocate(t *testing.T) {
	text := "Die monatliche Kaltmiete beträgt 1234 EUR."

	// Stale offsets: the chunk was cut from a longer revision of the text.
	stale := ChunkHit{DocumentID: "doc1", StartByte: 9000, EndByte: 9500, Text: "stored copy"}
	got := Narrow(text, "Kaltmiete", []ChunkHit{stale})
	if len(got) != 1 || got[0].Text != "stored copy" {
		t.Fatalf("a hit with stale offsets should pass through untouched: %#v", got)
	}

	// A chunk that matched by meaning rather than by any word in the query.
	dense := ChunkHit{DocumentID: "doc1", StartByte: 0, EndByte: len(text), Text: text}
	kept := Narrow(text, "Selbstbeteiligung", []ChunkHit{dense})
	if len(kept) != 1 || kept[0].Text != text {
		t.Fatalf("a hit with no locatable term should pass through untouched: %#v", kept)
	}

	if Narrow(text, "Kaltmiete", nil) != nil {
		t.Fatal("no hits in, no hits out")
	}
}
