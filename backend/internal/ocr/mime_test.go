package ocr

import "testing"

func TestGuessMimeType(t *testing.T) {
	cases := map[string]string{
		"doc.PDF": "application/pdf",
		"a.jpg":   "image/jpeg",
		"a.jpeg":  "image/jpeg",
		"a.png":   "image/png",
		"a.webp":  "image/webp",
		"a.txt":   "text/plain",
		"a.csv":   "text/csv",
		"a.docx":  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"a.xlsx":  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"a.pptx":  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"a.bin":   "application/octet-stream",
		"noext":   "application/octet-stream",
	}
	for name, want := range cases {
		if got := GuessMimeType(name); got != want {
			t.Errorf("GuessMimeType(%q)=%q want %q", name, got, want)
		}
	}
}
