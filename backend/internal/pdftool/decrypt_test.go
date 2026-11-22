package pdftool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lemmary/backend/internal/pdftool/testpdf"
)

func writeEncryptedPDF(t *testing.T, pageCount int, userPassword, ownerPassword string) string {
	t.Helper()
	requirePoppler(t, "qpdf")
	data, err := testpdf.Encrypt(testpdf.Multipage(pageCount), userPassword, ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "locked.pdf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPopplerReportsAPasswordRequired(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdftoppm")
	path := writeEncryptedPDF(t, 3, "secret", "secret")

	if _, err := PageCount(context.Background(), path); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("PageCount: expected ErrPasswordRequired, got %v", err)
	}
	err := RenderPage(context.Background(), path, filepath.Join(t.TempDir(), "p.png"), 100, 1)
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("RenderPage: expected ErrPasswordRequired, got %v", err)
	}
}

func TestOwnerPasswordOnlyOpensWithoutPrompt(t *testing.T) {
	requirePoppler(t, "pdfinfo")
	count, err := PageCount(context.Background(), writeEncryptedPDF(t, 2, "", "owner"))
	if err != nil || count != 2 {
		t.Fatalf("PageCount = %d, %v; want 2 pages", count, err)
	}
}

func TestDecrypt(t *testing.T) {
	requirePoppler(t, "pdfinfo")
	for _, password := range []string{"secret", "pässwörd"} {
		t.Run(password, func(t *testing.T) {
			path := writeEncryptedPDF(t, 4, password, password)
			out := filepath.Join(t.TempDir(), "open.pdf")

			err := Decrypt(context.Background(), path, out, "wrong")
			if !errors.Is(err, ErrIncorrectPassword) {
				t.Fatalf("wrong password: expected ErrIncorrectPassword, got %v", err)
			}

			if err := Decrypt(context.Background(), path, out, password); err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			count, err := PageCount(context.Background(), out)
			if err != nil || count != 4 {
				t.Fatalf("decrypted PageCount = %d, %v; want 4", count, err)
			}
		})
	}
}

func TestDecryptErrorNeverCarriesThePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.pdf")
	requirePoppler(t, "qpdf")
	if err := os.WriteFile(path, []byte("not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Decrypt(context.Background(), path, filepath.Join(t.TempDir(), "out.pdf"), "hunter2")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("expected an error without the password, got %v", err)
	}
}
