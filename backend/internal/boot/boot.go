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

// Result is what Prepare tells main to do with the rest of startup.
//
// The zero value means "carry on unchanged", which is what makes this free for
// an install that has encryption off: every field is an opt-in.
type Result struct {
	// Handled means the invocation is complete and main should exit with Code
	// without constructing an app at all. This is how a subcommand that must
	// not bootstrap the database gets answered.
	Handled bool

	// Code is the process exit status when Handled is set.
	Code int

	// DataDir overrides PocketBase's data directory. Empty leaves the default,
	// including the --dir flag's handling of it.
	//
	// Whatever sets this owns the directory's lifetime: it exists before
	// Prepare returns and stays valid until Close.
	DataDir string

	// Register runs after appwire.Register, with the live app, for wiring that
	// belongs to whatever Prepare set up and could not be built before it.
	Register func(app *pocketbase.PocketBase)

	// Close runs on the way out, whether the app served or Prepare handled the
	// invocation itself, and whether Execute succeeded or failed. Cleanup that
	// must not be skipped goes here.
	//
	// It is why main.go is structured as run() int rather than calling
	// log.Fatal: os.Exit skips deferred functions, so a fatal on any path
	// between Prepare and here would silently drop this.
	Close func() error
}

// Prepare runs the pre-boot step.
func Prepare(argv []string) (Result, error) { return vaultPrepare(argv) }
