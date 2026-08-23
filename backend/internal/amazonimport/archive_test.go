package amazonimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	body string
}

func buildZip(t *testing.T, entries ...zipEntry) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, entry := range entries {
		f, err := w.Create(entry.name)
		if err != nil {
			t.Fatalf("create %s: %v", entry.name, err)
		}
		if _, err := f.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write %s: %v", entry.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return zr
}

func noDuplicates(string) (string, error) { return "", nil }

// A realistic slice of the Amazon export: reports, delivery photos, and the
// invoice PDFs in two numbered folders.
func amazonArchive(t *testing.T) *zip.Reader {
	t.Helper()
	return buildZip(t,
		zipEntry{"Your Orders/Your Amazon Orders/Order History.csv", "order,history"},
		zipEntry{"Your Orders/Your Amazon Orders/Media/YourOrders.PhotoOnDelivery/media/a1b2.jpeg", "jpegdata"},
		zipEntry{"Your Orders/Additional Data/Retail.TransactionalInvoicing.2.1/1.pdf", "%PDF-invoice-2.1-1"},
		zipEntry{"Your Orders/Additional Data/Retail.TransactionalInvoicing.2.1/2.pdf", "%PDF-invoice-2.1-2"},
		zipEntry{"Your Orders/Additional Data/Retail.TransactionalInvoicing.2.2/1.pdf", "%PDF-invoice-2.2-1"},
		zipEntry{"Your Orders/Additional Data/Your Orders.Returns.2/README.txt", "readme"},
	)
}

func TestScanPDFsPicksOnlyPDFs(t *testing.T) {
	entries, ignored, err := scanPDFs(noDuplicates, amazonArchive(t))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3: %+v", len(entries), entries)
	}
	if ignored != 3 {
		t.Fatalf("ignored=%d want 3 (csv, jpeg, txt)", ignored)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Path, ".pdf") {
			t.Fatalf("non-PDF entry slipped through: %s", entry.Path)
		}
		if entry.Duplicate || entry.Oversized {
			t.Fatalf("unexpected flags on %s: %+v", entry.Path, entry)
		}
		if entry.Size == 0 || entry.checksum == "" {
			t.Fatalf("entry not measured: %+v", entry)
		}
	}
	// Same numbering in both invoice folders must stay distinguishable.
	if entries[0].Name != "Retail.TransactionalInvoicing.2.1-1.pdf" {
		t.Fatalf("name=%q", entries[0].Name)
	}
	if entries[2].Name != "Retail.TransactionalInvoicing.2.2-1.pdf" {
		t.Fatalf("name=%q", entries[2].Name)
	}
}

func TestScanPDFsMarksKnownAndRepeatedFiles(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"orders/1.pdf", "%PDF-one"},
		zipEntry{"orders/2.pdf", "%PDF-two"},
		zipEntry{"copies/1-copy.pdf", "%PDF-one"},
	)
	lookup := func(checksum string) (string, error) {
		// Pretend the second invoice was imported before.
		if checksum == sha(t, "%PDF-two") {
			return "doc_existing", nil
		}
		return "", nil
	}

	entries, _, err := scanPDFs(lookup, zr)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3", len(entries))
	}
	if entries[0].Duplicate {
		t.Fatal("first invoice should be new")
	}
	if !entries[1].Duplicate || entries[1].DuplicateOf != "doc_existing" {
		t.Fatalf("known invoice not flagged: %+v", entries[1])
	}
	if !entries[2].Duplicate || entries[2].DuplicateOf != "" {
		t.Fatalf("in-archive repeat not flagged: %+v", entries[2])
	}
}

func TestScanPDFsSkipsArchiverJunk(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"__MACOSX/orders/._1.pdf", "junk"},
		zipEntry{"orders/._2.pdf", "junk"},
		zipEntry{"orders/empty.pdf", ""},
		zipEntry{"orders/real.PDF", "%PDF-real"},
	)
	entries, ignored, err := scanPDFs(noDuplicates, zr)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "orders/real.PDF" {
		t.Fatalf("entries=%+v want only the real PDF (extension match is case-insensitive)", entries)
	}
	if ignored != 1 {
		t.Fatalf("ignored=%d want 1 (the empty pdf); junk must not be counted", ignored)
	}
}

func TestScanPDFsFlagsOversizedEntries(t *testing.T) {
	original := maxEntryBytes
	maxEntryBytes = 8
	t.Cleanup(func() { maxEntryBytes = original })

	zr := buildZip(t,
		zipEntry{"orders/small.pdf", "%PDF-s"},
		zipEntry{"orders/big.pdf", "%PDF-way-too-long"},
	)
	entries, _, err := scanPDFs(noDuplicates, zr)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if entries[0].Oversized {
		t.Fatalf("small entry flagged: %+v", entries[0])
	}
	if !entries[1].Oversized {
		t.Fatalf("big entry not flagged: %+v", entries[1])
	}
	if entries[1].checksum != "" {
		t.Fatal("oversized entry must not be hashed for duplicate matching")
	}
}

func TestScanPDFsWithoutPDFs(t *testing.T) {
	zr := buildZip(t, zipEntry{"Your Orders/Order History.csv", "a,b"})
	if _, _, err := scanPDFs(noDuplicates, zr); !errors.Is(err, ErrNoPDFs) {
		t.Fatalf("err=%v want ErrNoPDFs", err)
	}
}

func TestScanPDFsPropagatesLookupError(t *testing.T) {
	zr := buildZip(t, zipEntry{"orders/1.pdf", "%PDF-one"})
	want := errors.New("db down")
	if _, _, err := scanPDFs(func(string) (string, error) { return "", want }, zr); !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
}

func TestDocumentName(t *testing.T) {
	cases := map[string]string{
		"Your Orders/Additional Data/Retail.TransactionalInvoicing.2.1/7.pdf": "Retail.TransactionalInvoicing.2.1-7.pdf",
		"invoice.pdf":                "invoice.pdf",
		"orders\\2024\\3.pdf":        "2024-3.pdf",
		"Your Orders/receipt-42.pdf": "Your Orders-receipt-42.pdf",
	}
	for input, want := range cases {
		if got := documentName(input); got != want {
			t.Fatalf("documentName(%q)=%q want %q", input, got, want)
		}
	}
}

func sha(t *testing.T, body string) string {
	t.Helper()
	zr := buildZip(t, zipEntry{"x.pdf", body})
	checksum, _, err := hashEntry(zr.File[0])
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return checksum
}
