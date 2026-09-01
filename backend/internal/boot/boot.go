// Package boot is what happens before PocketBase is constructed.
//
// Almost nothing belongs here. Every feature package attaches to a live app
// through its own Register, which is simpler and attaches to an app already
// known to be working. This package exists for the things that cannot wait for
// an app to exist at all:
//
//   - PocketBase fixes its data directory at construction time, so a feature
//     that has to place the data somewhere other than the default must say so
//     before pocketbase.New.
//   - A subcommand that must run with no database open cannot be a cobra
//     command, because cobra runs inside app.Execute, by which point the app
//     has bootstrapped — migrations, SQLite, the search index, the setup
//     wizard route.
//   - Anything that has to occupy the listen address before the server takes
//     it over, or that has to finish before the first query, has no hook to
//     bind to yet.
//
// One feature needs all three: encryption at rest, in vault.go. With
// VAULT_ENABLED unset it does nothing, Prepare returns the zero Result, and
// main runs exactly the sequence it would have run without this package.
package boot

import "github.com/pocketbase/pocketbase"

// Result tells main what to do with the rest of startup. The zero value means
// carry on unchanged.
type Result struct {
	// Exit with Code without constructing an app (e.g. a vault subcommand).
	Handled bool

	// Process exit status when Handled is set.
	Code int

	// DataDir overrides PocketBase's data directory. Empty leaves the default.
	// Whatever sets this owns the directory's lifetime until Close.
	DataDir string

	// Register runs after appwire.Register for wiring that could not be built
	// before the app existed.
	Register func(app *pocketbase.PocketBase)

	// Close runs on the way out, success or failure. Cleanup that must not be
	// skipped goes here. main must not os.Exit between Prepare and this defer.
	Close func() error
}

func Prepare(argv []string) (Result, error) { return vaultPrepare(argv) }
