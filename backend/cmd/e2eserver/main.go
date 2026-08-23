package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"lemmary/backend/e2e"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:18090", "listen address")
	publicDir := flag.String("publicDir", "", "SPA public directory (defaults to ../../public from backend)")
	envFile := flag.String("env-file", "", "optional path to write E2E_* env vars for Playwright")
	flag.Parse()

	dir := *publicDir
	if dir == "" {
		dir = defaultPublicDir()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fail(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "index.html")); err != nil {
		fail(fmt.Errorf("publicDir %s missing index.html (run frontend build first): %w", abs, err))
	}

	h, err := e2e.Start(e2e.Options{
		PublicDir: abs,
		HTTPAddr:  *httpAddr,
	})
	if err != nil {
		fail(err)
	}
	defer h.Close()

	if *envFile != "" {
		content := fmt.Sprintf(
			"E2E_BASE_URL=%s\nE2E_USER_EMAIL=%s\nE2E_USER_PASSWORD=%s\nE2E_SUPER_EMAIL=%s\nE2E_SUPER_PASSWORD=%s\n",
			h.BaseURL, e2e.UserEmail, e2e.UserPassword, e2e.SuperEmail, e2e.SuperPassword,
		)
		if err := os.WriteFile(*envFile, []byte(content), 0o600); err != nil {
			fail(err)
		}
	}

	fmt.Printf("e2e server ready at %s\n", h.BaseURL)
	fmt.Printf("E2E_BASE_URL=%s\n", h.BaseURL)
	fmt.Printf("E2E_USER_EMAIL=%s\n", e2e.UserEmail)
	fmt.Printf("E2E_SUPER_EMAIL=%s\n", e2e.SuperEmail)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func defaultPublicDir() string {
	// backend/cmd/e2eserver -> ../../public
	return filepath.Clean(filepath.Join("..", "..", "public"))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "e2eserver: %v\n", err)
	os.Exit(1)
}
