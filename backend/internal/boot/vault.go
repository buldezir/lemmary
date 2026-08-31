package boot

import (
	"github.com/pocketbase/pocketbase"

	"lemmary/backend/internal/vault"
)

// vaultPrepare is the pre-boot step: encryption at rest.
//
// Inert unless VAULT_ENABLED is set — a disabled vault leaves the data
// directory alone, so the same binary serves an ordinary unencrypted install
// unchanged.
//
// Why this cannot attach to a live app the way every other feature does:
// PocketBase fixes its data directory at construction, so pointing it at the
// decrypted working directory has to be decided before pocketbase.New; the
// users collection that would authenticate an unlock lives inside the
// encrypted database, so the gate cannot be a route; and `vault init` has to
// run with no database open at all, which a cobra subcommand cannot do because
// cobra runs inside app.Execute.
func vaultPrepare(argv []string) (Result, error) {
	// Answered without constructing an app: bootstrap would run migrations
	// inside a working directory that is about to be replaced by the restore,
	// and would leave an un-bootstrapped setup wizard reachable at the one
	// moment the archive is unlocked with no account in it.
	if op, ok := vault.IsCommand(argv); ok {
		return Result{Handled: true, Code: vault.RunCommand(op)}, nil
	}

	v, err := vault.Open(argv)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		Register: func(app *pocketbase.PocketBase) { vault.Register(app, v) },
		// Flushes the archive and wipes the plaintext working directory. main
		// runs this through a defer, which is why it is structured as
		// os.Exit(run()) — a log.Fatal anywhere after Open would skip it and
		// leave the decrypted archive behind.
		Close: v.Close,
	}
	if v.Enabled() {
		res.DataDir = v.WorkDir()
	}
	return res, nil
}
