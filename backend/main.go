package main

import (
	"log"
	"os"
	"path/filepath"

	"lemmary/backend/internal/appapi"
	"lemmary/backend/internal/appwire"
	"lemmary/backend/internal/boot"
	"lemmary/backend/internal/config"

	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/osutils"

	_ "lemmary/backend/migrations"
)

func main() {
	// run owns the exit status so boot.Result.Close always runs (os.Exit skips defers).
	os.Exit(run())
}

func run() int {
	loadEnvFile()

	// Before pocketbase.New; see internal/boot.
	pre, err := boot.Prepare(os.Args[1:])
	if err != nil {
		log.Printf("boot: %v", err)
		return 1
	}
	if pre.Close != nil {
		defer func() {
			if cerr := pre.Close(); cerr != nil {
				log.Printf("boot: cleanup failed: %v", cerr)
			}
		}()
	}
	if pre.Handled {
		return pre.Code
	}

	// Before pocketbase.New: a managed instance with a bad AI environment must
	// not come up; nobody inside it can repair it. See config.AIEnv.
	aiEnv, err := config.AIEnvFromEnv()
	if err != nil {
		log.Printf("config: %v", err)
		return 1
	}

	app := pocketbase.New()
	if pre.DataDir != "" {
		// Data directory is fixed at construction; there is no setter.
		app = pocketbase.NewWithConfig(pocketbase.Config{
			DefaultDataDir: pre.DataDir,
			DefaultDev:     osutils.IsProbablyGoRun(),
		})
	}

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

	rt := config.NewRuntime(aiEnv)
	appwire.Register(app, rt, publicDir, indexFallback)
	// After appwire so the vault gate binds outside every core route.
	if pre.Register != nil {
		pre.Register(app)
	}
	// Register serve/superuser before Execute so CLI hooks can wrap them.
	// Do not call app.Start() — it would re-add the same commands.
	appapi.RegisterSystemCommands(app, true)

	if err := app.Execute(); err != nil {
		log.Print(err)
		return 1
	}
	return 0
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
