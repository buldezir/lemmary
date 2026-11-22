package pdftool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DecryptTimeout bounds one qpdf run, which rewrites the whole file.
const DecryptTimeout = 2 * time.Minute

var ErrIncorrectPassword = errors.New("pdftool: incorrect password")

// Decrypt writes pdfPath to outPath without its encryption. qpdf rather than
// poppler, which cannot write a PDF back, and it keeps every object as it was
// instead of re-rendering the pages.
//
// The password goes in on stdin: on the command line any process on the host
// could read it. It is never part of a returned error.
func Decrypt(ctx context.Context, pdfPath, outPath, password string) error {
	if err := RequirePDF(pdfPath); err != nil {
		return err
	}
	path, err := exec.LookPath("qpdf")
	if err != nil {
		return fmt.Errorf("pdftool: qpdf not found (install qpdf): %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, DecryptTimeout)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "--password-file=-", "--decrypt", pdfPath, outPath)
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return nil
	}
	// Exit status 3 is qpdf's "succeeded with warnings": the output is written.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 3 {
		return nil
	}
	if strings.Contains(stderr.String(), "invalid password") {
		return ErrIncorrectPassword
	}
	return fmt.Errorf("pdftool: qpdf: %w: %s", err, strings.TrimSpace(stderr.String()))
}
