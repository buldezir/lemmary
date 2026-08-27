// Package boot is the seam a build attaches to before PocketBase is
// constructed.
//
// It is the earlier sibling of internal/ext. An ext.Edition attaches to a live
// app: routes, hooks, pipeline stages, cron jobs. That covers almost
// everything, and almost everything should use it. What it cannot cover is
// work that must happen before the app exists at all:
//
//   - PocketBase fixes its data directory at construction time, so a build
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
// The open-source build supplies the zero Boot, which does none of this and
// leaves main.go running exactly the sequence it ran before this package
// existed. A private build supplies a populated one from a file guarded by its
// own build tag, so the two never appear in the same compilation and a fork
// carrying it adds files rather than editing shared ones.
//
// Reach for ext.Edition first. This seam exists for the cases above and is
// deliberately narrow; a build that can express what it needs as an Edition
// should, because an Edition attaches to an app that is already known to be
// working.
package boot

import "github.com/pocketbase/pocketbase"

// Boot is what one build of this binary does before the app is constructed.
// The zero value is the open-source build: nothing.
type Boot struct {
	// Name appears in the startup log line, alongside the edition name, so a
	// running container can be identified from its logs alone. Empty means the
	// open-source build.
	Name string

	// Prepare runs once, before pocketbase.New, with the arguments after the
	// program name. It may relocate the data directory, answer the invocation
	// outright, or block until some precondition is met.
	//
	// Returning an error aborts startup; main reports it and exits non-zero.
	Prepare func(argv []string) (Result, error)
}

// Result is what a Prepare tells main to do with the rest of startup.
//
// The zero value means "carry on unchanged", which is what makes the seam free
// for the open-source build: every field is an opt-in.
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
	// A build that sets this owns the directory's lifetime: it exists before
	// Prepare returns and stays valid until Close.
	DataDir string

	// Register runs after appwire.Register, with the live app, for wiring that
	// belongs to whatever Prepare set up. It is not a second edition seam —
	// ext.Edition.Register is that, runs with the deps appwire built, and is
	// where anything not tied to pre-boot state belongs.
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

// Prepare runs this build's pre-boot step.
//
// The zero Boot, and a Boot with no Prepare, both return the zero Result: main
// proceeds exactly as it would have.
func Prepare(argv []string) (Result, error) {
	b := current()
	if b.Prepare == nil {
		return Result{}, nil
	}
	return b.Prepare(argv)
}

// Name reports this build's boot name, or "" for the open-source build.
func Name() string { return current().Name }
