package appapi

import (
	"strconv"
	"strings"
	"testing"

	"lemmary/backend/internal/chunk"
)

func longText(paragraphs int) string {
	var b strings.Builder
	for i := 0; i < paragraphs; i++ {
		b.WriteString(strings.Repeat("Paragraph ", 20))
		b.WriteString("number ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(".\n\n")
	}
	return b.String()
}

func TestMarkChunksLandsAMarkerAtEveryChunkStart(t *testing.T) {
	text := longText(60)
	chunks, _ := chunk.Split(text, chunk.DefaultOptions())
	if len(chunks) < 3 {
		t.Fatalf("fixture too short: %d chunks", len(chunks))
	}
	marked := markChunks(text)
	if strings.Count(marked, "[chunk ") != len(chunks) {
		t.Fatalf("markers = %d, want %d", strings.Count(marked, "[chunk "), len(chunks))
	}
	if !strings.HasPrefix(marked, "[chunk 0]\n") {
		t.Fatalf("first marker missing: %q", marked[:20])
	}
	for i := range chunks {
		marker := "[chunk " + strconv.Itoa(i) + "]\n"
		at := strings.Index(marked, marker)
		if at < 0 {
			t.Fatalf("marker %d missing", i)
		}
		head := text[chunks[i].Start:min(chunks[i].End, chunks[i].Start+20)]
		if !strings.HasPrefix(marked[at+len(marker):], head) {
			t.Fatalf("marker %d does not sit at its chunk", i)
		}
	}
	if markChunks("") != "" {
		t.Fatal("empty text should stay empty")
	}
}

func TestSliceChunksMergesRunsAndReportsUnknownOrdinals(t *testing.T) {
	text := longText(60)
	chunks, _ := chunk.Split(text, chunk.DefaultOptions())
	out, total, unknown := sliceChunks(text, []int{3, 1, 2, 99, 2, -1})
	if total != len(chunks) {
		t.Fatalf("total = %d, want %d", total, len(chunks))
	}
	if len(unknown) != 2 || unknown[0] != -1 || unknown[1] != 99 {
		t.Fatalf("unknown = %v", unknown)
	}
	// One run, one header, one contiguous slice: the overlap between
	// consecutive chunks appears once.
	if want := "[chunk 1-3]\n" + text[chunks[1].Start:chunks[3].End]; out != want {
		t.Fatalf("run = %q, want %q", out[:min(len(out), 60)], want[:60])
	}

	out, _, _ = sliceChunks(text, []int{0, 4})
	if strings.Count(out, "[chunk ") != 2 || !strings.Contains(out, "\n\n…\n\n") {
		t.Fatalf("two separate chunks should be two runs with a gap: %q", out)
	}
}

func TestChunkExcerptShowsAShortDocumentWholeAndALongOneByBestChunks(t *testing.T) {
	short := "Just a note about the leak.\n"
	out, total := chunkExcerpt("d", short, "leak", nil, 16_000)
	if total != 1 || !strings.HasPrefix(out, "[chunk 0]\n") || !strings.Contains(out, short) {
		t.Fatalf("short document: total=%d out=%q", total, out)
	}

	text := longText(200)
	chunks, _ := chunk.Split(text, chunk.DefaultOptions())
	needle := "The insurer wrote about the water leak in the kitchen."
	target := len(chunks) / 2
	text = text[:chunks[target].Start] + needle + " " + text[chunks[target].Start:]
	chunks, _ = chunk.Split(text, chunk.DefaultOptions())

	budget := 4000
	out, total = chunkExcerpt("d", text, "insurer water leak", nil, budget)
	if total != len(chunks) {
		t.Fatalf("total = %d, want %d", total, len(chunks))
	}
	if !strings.Contains(out, needle) {
		t.Fatal("the chunk matching the question should be kept")
	}
	if len(out) > budget+200 {
		t.Fatalf("excerpt %d bytes exceeds the budget %d", len(out), budget)
	}
	ord := -1
	for i, c := range chunks {
		if strings.Contains(text[c.Start:c.End], needle) {
			ord = i
			break
		}
	}
	if !strings.Contains(out, "[chunk "+strconv.Itoa(ord)+"]") && !strings.Contains(out, strconv.Itoa(ord)+"]") {
		t.Fatalf("marker should carry the real ordinal %d: %q", ord, out[:min(len(out), 60)])
	}
}
