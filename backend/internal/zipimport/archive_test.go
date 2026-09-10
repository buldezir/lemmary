package zipimport

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

func TestScanAmazonPicksOnlyPDFs(t *testing.T) {
	entries, ignored, err := scan(SourceAmazon, noDuplicates, amazonArchive(t))
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

func TestScanAmazonMarksKnownAndRepeatedFiles(t *testing.T) {
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

	entries, _, err := scan(SourceAmazon, lookup, zr)
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

func TestScanAmazonSkipsArchiverJunk(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"__MACOSX/orders/._1.pdf", "junk"},
		zipEntry{"orders/._2.pdf", "junk"},
		zipEntry{"orders/empty.pdf", ""},
		zipEntry{"orders/real.PDF", "%PDF-real"},
	)
	entries, ignored, err := scan(SourceAmazon, noDuplicates, zr)
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

func TestScanAmazonFlagsOversizedEntries(t *testing.T) {
	original := maxEntryBytes
	maxEntryBytes = 8
	t.Cleanup(func() { maxEntryBytes = original })

	zr := buildZip(t,
		zipEntry{"orders/small.pdf", "%PDF-s"},
		zipEntry{"orders/big.pdf", "%PDF-way-too-long"},
	)
	entries, _, err := scan(SourceAmazon, noDuplicates, zr)
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

func TestScanAmazonWithoutPDFs(t *testing.T) {
	zr := buildZip(t, zipEntry{"Your Orders/Order History.csv", "a,b"})
	if _, _, err := scan(SourceAmazon, noDuplicates, zr); !errors.Is(err, ErrNoFiles) {
		t.Fatalf("err=%v want ErrNoFiles", err)
	}
}

func TestScanAmazonPropagatesLookupError(t *testing.T) {
	zr := buildZip(t, zipEntry{"orders/1.pdf", "%PDF-one"})
	want := errors.New("db down")
	if _, _, err := scan(SourceAmazon, func(string) (string, error) { return "", want }, zr); !errors.Is(err, want) {
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

// A zip somebody packed themselves: one of every type the documents collection
// can store, plus three it cannot.
func mixedArchive(t *testing.T) *zip.Reader {
	t.Helper()
	return buildZip(t,
		zipEntry{"docs/invoice.pdf", "%PDF-invoice"},
		zipEntry{"docs/scan.jpg", "jpegdata"},
		zipEntry{"docs/scan2.jpeg", "jpegdata"},
		zipEntry{"docs/receipt.png", "pngdata"},
		zipEntry{"docs/photo.webp", "webpdata"},
		zipEntry{"docs/notes.txt", "notes"},
		zipEntry{"docs/ledger.csv", "a,b\n1,2"},
		zipEntry{"docs/letter.docx", "docxdata"},
		zipEntry{"docs/budget.xlsx", "xlsxdata"},
		zipEntry{"docs/manifest.json", "{}"},
		zipEntry{"docs/setup.exe", "MZbinary"},
		zipEntry{"docs/nested.zip", "PKzip"},
	)
}

// The whole feature in one test: the same archive, read two ways.
func TestSourceDecidesWhatCountsAsADocument(t *testing.T) {
	entries, ignored, err := scan(SourceFiles, noDuplicates, mixedArchive(t))
	if err != nil {
		t.Fatalf("scan files: %v", err)
	}
	if len(entries) != 9 {
		t.Fatalf("files entries=%d want 9", len(entries))
	}
	if ignored != 3 {
		t.Fatalf("files ignored=%d want 3 (json, exe, zip)", ignored)
	}

	entries, ignored, err = scan(SourceAmazon, noDuplicates, mixedArchive(t))
	if err != nil {
		t.Fatalf("scan amazon: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "docs-invoice.pdf" {
		t.Fatalf("amazon entries=%v want just the pdf", entries)
	}
	if ignored != 11 {
		t.Fatalf("amazon ignored=%d want 11", ignored)
	}
}

func TestSourceAcceptsIsCaseInsensitive(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"SCAN.PDF", "%PDF"},
		zipEntry{"Photo.JPEG", "jpegdata"},
		zipEntry{"Letter.DocX", "docxdata"},
	)
	entries, ignored, err := scan(SourceFiles, noDuplicates, zr)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 || ignored != 0 {
		t.Fatalf("entries=%d ignored=%d want 3/0", len(entries), ignored)
	}
}

// An empty entry would pass the extension filter and then fail mid-import,
// because filesystem.NewFileFromBytes refuses zero bytes. It is dropped in the
// scan instead, and does not even count as ignored -- it is not a file anyone
// meant to send.
func TestSourceFilesSkipsJunkAndEmptyEntries(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"docs/real.pdf", "%PDF"},
		zipEntry{"__MACOSX/docs/._real.pdf", "resourcefork"},
		zipEntry{"docs/._sidecar.png", "appledouble"},
		zipEntry{"docs/empty.png", ""},
	)
	entries, ignored, err := scan(SourceFiles, noDuplicates, zr)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "docs/real.pdf" {
		t.Fatalf("entries=%v want only docs/real.pdf", entries)
	}
	if ignored != 1 {
		t.Fatalf("ignored=%d want 1 (the empty png; the two junk entries do not count)", ignored)
	}
}
