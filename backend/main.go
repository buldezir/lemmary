package main

import (
	"log"
	"os"
	"path/filepath"

	"paperless-go/backend/internal/appapi"
	"paperless-go/backend/internal/appwire"
	"paperless-go/backend/internal/config"

	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/osutils"

	_ "paperless-go/backend/migrations"
)

func main() {
	loadEnvFile()

	app := pocketbase.New()

	var publicDir string
	app.RootCmd.PersistentFlags().StringVar(
		&publicDir,
		"publicDir",
		defaultPublicDir(),
		"the directory to serve static files",
	)

	var indexFallback bool
	app.RootCmd.PersistentFlags().BoolVar(
		&indexFallback,
		"indexFallback",
		true,
		"fallback the request to index.html on missing static path (SPA)",
	)

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: osutils.IsProbablyGoRun(),
	})

	rt := config.NewRuntime()
	appwire.Register(app, rt, publicDir, indexFallback)
	// Register serve/superuser before Execute so CLI hooks can wrap them.
	// Do not call app.Start() — it would re-add the same commands.
	appapi.RegisterSystemCommands(app, true)

	if err := app.Execute(); err != nil {
		log.Fatal(err)
	}
}

func loadEnvFile() {
	for _, path := range []string{".env", "../.env"} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if osutils.IsProbablyGoRun() {
			if err := godotenv.Overload(path); err != nil {
				log.Printf("warning: failed to load %s: %v", path, err)
			}
		} else {
			if err := godotenv.Load(path); err != nil {
				log.Printf("warning: failed to load %s: %v", path, err)
			}
		}
		return
	}
}

func defaultPublicDir() string {
	if osutils.IsProbablyGoRun() {
		return filepath.Clean("../public")
	}

	exe, err := os.Executable()
	if err != nil {
		return filepath.Clean("../public")
	}

	exeDir := filepath.Dir(exe)
	if filepath.Base(exeDir) == "backend" {
		return filepath.Join(exeDir, "..", "public")
	}

	return filepath.Join(exeDir, "public")
}
