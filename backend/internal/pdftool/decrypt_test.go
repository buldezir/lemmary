package pdftool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lemmary/backend/internal/pdftool/testpdf"
)

// requireQPDF skips a test that needs an encrypted fixture: qpdf builds them
// (testpdf.Encrypt), since PDFium cannot encrypt.
func requireQPDF(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf not installed")
	}
}

func writeEncryptedPDF(t *testing.T, pageCount int, userPassword, ownerPassword string) string {
	t.Helper()
	requireQPDF(t)
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

func TestPasswordRequiredIsReported(t *testing.T) {
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
	count, err := PageCount(context.Background(), writeEncryptedPDF(t, 2, "", "owner"))
	if err != nil || count != 2 {
		t.Fatalf("PageCount = %d, %v; want 2 pages", count, err)
	}
}

func TestDecrypt(t *testing.T) {
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
			// The text layer survives: the objects are written back, not
			// re-rendered.
			text, err := PageText(context.Background(), out, 3)
			if err != nil || !strings.Contains(text, "Page 3") {
				t.Fatalf("decrypted page 3 text = %q, %v", text, err)
			}
		})
	}
}

func TestDecryptErrorNeverCarriesThePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(path, []byte("not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Decrypt(context.Background(), path, filepath.Join(t.TempDir(), "out.pdf"), "hunter2")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("expected an error without the password, got %v", err)
	}
}
