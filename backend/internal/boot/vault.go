package boot

import (
	"github.com/pocketbase/pocketbase"

	"lemmary/backend/internal/vault"
)

// vaultPrepare is encryption at rest. Inert unless VAULT_ENABLED is set.
// Why this cannot be an ordinary Register: see the package comment.
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
		Close:    v.Close, // flush + wipe plaintext; main defers this
	}
	if v.Enabled() {
		res.DataDir = v.WorkDir()
	}
	return res, nil
}
