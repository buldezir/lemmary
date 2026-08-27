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
	// run, not main, owns the process exit status. A build can attach cleanup
	// to boot.Result.Close that must not be skipped, and log.Fatal calls
	// os.Exit, which skips deferred functions — so nothing between Prepare and
	// the return below may exit the process directly.
	os.Exit(run())
}

func run() int {
	loadEnvFile()

	// Before pocketbase.New on purpose. A build may need the data directory
	// placed somewhere the default cannot reach, or need to answer this
	// invocation with no database open at all; neither is expressible once the
	// app exists. Upstream this returns the zero Result and nothing below
	// changes. See internal/boot.
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
	// Logged separately from the edition line appwire emits, and earlier than
	// it: a build whose pre-boot step fails never reaches that line, so this is
	// what distinguishes "the wrong image was deployed" from "the right image
	// could not start".
	if name := boot.Name(); name != "" {
		log.Printf("boot: %s", name)
	}

	app := pocketbase.New()
	if pre.DataDir != "" {
		// Mirrors what pocketbase.New does, with the directory replaced: the
		// data directory is fixed at construction and there is no setter.
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

	rt := config.NewRuntime()
	appwire.Register(app, rt, publicDir, indexFallback)
	// After appwire so anything the pre-boot step wires binds behind every
	// core route and hook, the same ordering ext.Edition.Register gets.
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
