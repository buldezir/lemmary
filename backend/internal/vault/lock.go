package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockName = "vault.lock"

// acquireLock takes an exclusive advisory lock on the vault directory, held for
// the process lifetime.
//
// Without encryption, SQLite's own file locking serialised a CLI invocation
// against a running server because both opened the same files. Once each process
// materialises its own private plaintext copy that protection is gone, and two
// processes would each flush their own view — last writer wins, silently, losing
// whatever the other did. Refusing to start is far better than that.
//
// The visible cost is that `superuser upsert` and friends no longer work while
// the server is running; docs/development.md says so.
func acquireLock(dir string) (*os.File, error) {
	path := filepath.Join(dir, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf(
			"vault: another process already holds %s — stop the running instance first (a vault cannot be shared between processes): %w",
			path, err)
	}
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
