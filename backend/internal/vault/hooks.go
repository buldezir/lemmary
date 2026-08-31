package vault

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Flush pacing.
const (
	// debounceDelay is the workhorse trigger: a flush fires this long after the
	// last write, so a burst of uploads produces one flush rather than dozens.
	debounceDelay = 10 * time.Second
	// minFlushInterval keeps a steady stream of writes from flushing constantly.
	minFlushInterval = 15 * time.Second
	// dirtyThreshold forces a flush even while writes keep arriving, so a bulk
	// import cannot defer durability indefinitely.
	dirtyThreshold = 100
	// flushCron is the backstop for anything the record hooks do not observe.
	flushCron = "* * * * *"
)

// terminatePriority runs the shutdown flush after PocketBase has drained the
// HTTP server (its own handler sits at -9999) but before the databases close,
// since the snapshot needs them open.
const terminatePriority = -1000

// Register wires the vault into a PocketBase application.
//
// Everything the vault needs from the app is bound here: no other package
// imports it, and with encryption disabled every binding is skipped, so the
// application behaves exactly as it does today.
func Register(app *pocketbase.PocketBase, v *Vault) {
	if !v.Enabled() {
		return
	}
	f := &flusher{v: v}

	app.OnBootstrap().Bind(&hook.Handler[*core.BootstrapEvent]{
		Func: func(e *core.BootstrapEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			// The databases only exist after bootstrap, so the snapshotter is
			// installed here rather than at construction.
			v.SetSnapshotter(NewPocketBaseSnapshotter(e.App.NonconcurrentDB(), e.App.AuxNonconcurrentDB()))
			return nil
		},
	})

	registerFlushTriggers(app, f)
	registerGuards(app, v)
	registerEnrollment(app, v)
	registerAPI(app, v)
}

// flusher serialises and debounces flush requests.
type flusher struct {
	v *Vault

	mu        sync.Mutex
	timer     *time.Timer
	lastFlush time.Time
	stopped   bool
}

// run performs a flush and records when it happened.
//
// It is deliberately not gated on stopped: the shutdown path cancels the
// debounce and then flushes, and an early return here would turn the most
// important flush of all into a silent no-op.
func (f *flusher) run(reason string) {
	f.mu.Lock()
	f.lastFlush = time.Now()
	f.mu.Unlock()

	if err := f.v.Flush(reason); err != nil {
		f.v.opts.Log("vault: flush (%s) failed: %v", reason, err)
	}
}

// touch records a write and schedules a debounced flush.
func (f *flusher) touch() {
	pending := f.v.MarkDirty()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}

	// A long enough burst must not defer durability forever.
	if pending >= dirtyThreshold && time.Since(f.lastFlush) >= minFlushInterval {
		if f.timer != nil {
			f.timer.Stop()
			f.timer = nil
		}
		go f.run("dirty threshold")
		return
	}

	if f.timer != nil {
		f.timer.Reset(debounceDelay)
		return
	}
	f.timer = time.AfterFunc(debounceDelay, func() {
		f.mu.Lock()
		f.timer = nil
		f.mu.Unlock()
		f.run("debounce")
	})
}

// stop cancels the pending debounce and prevents further scheduling. It does
// not prevent an explicit flush.
func (f *flusher) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
}

func registerFlushTriggers(app *pocketbase.PocketBase, f *flusher) {
	// Dirty tracking with no collection filter. This is how "flush after each
	// processed document" is achieved without touching internal/worker: the
	// pipeline's writes are ordinary record writes.
	mark := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		f.touch()
		return nil
	}
	app.OnRecordAfterCreateSuccess().BindFunc(mark)
	app.OnRecordAfterUpdateSuccess().BindFunc(mark)
	app.OnRecordAfterDeleteSuccess().BindFunc(mark)

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if err := e.App.Cron().Add("vault_flush", flushCron, func() {
			if f.v.dirty.get() == 0 {
				return
			}
			f.run("cron")
		}); err != nil {
			return err
		}
		s := f.v.Stats()
		f.v.opts.Log(
			"vault: serving unlocked at generation %d (%d entries, %d MiB plaintext); flush debounce %s, loss window on hard kill is bounded by it",
			s.Generation, s.Entries, s.PlainBytes>>20, debounceDelay)
		return e.Next()
	})

	// The shutdown flush. It runs before e.Next() because PocketBase's own
	// terminate handler closes the databases, and the snapshot needs them open.
	app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Priority: terminatePriority,
		Func: func(e *core.TerminateEvent) error {
			f.stop()
			// Flush before e.Next(): PocketBase's terminate finalizer calls
			// ResetBootstrapState, which closes the databases the snapshot
			// needs, and App.Restart() execve's the process from inside that
			// same finalizer — anything deferred past e.Next() would never run
			// at all. This is the flush that makes a clean stop lossless.
			f.v.Finalize()
			return e.Next()
		},
	})
}

// registerGuards closes the two documented ways data could leave the encryption
// boundary in the clear.
func registerGuards(app *pocketbase.PocketBase, v *Vault) {
	// PocketBase's backup writes a plaintext zip of the whole data dir into the
	// data dir — in RAM here, doubling memory — and ships it to S3 when
	// configured. One binding blocks the HTTP route, the autobackup cron and the
	// CLI at once. The vault directory is itself a consistent encrypted backup,
	// so nothing of value is lost.
	refuse := func(e *core.BackupEvent) error {
		return fmt.Errorf(
			"backups are disabled while encryption at rest is on: a PocketBase backup would write an unencrypted archive of every document. Copy the vault directory %s instead — it is already encrypted and internally consistent", v.Dir())
	}
	app.OnBackupCreate().Bind(&hook.Handler[*core.BackupEvent]{Priority: -99999, Func: refuse})
	app.OnBackupRestore().Bind(&hook.Handler[*core.BackupEvent]{Priority: -99999, Func: refuse})

	// With S3 record storage enabled PocketBase never touches the local data
	// dir, so every uploaded document would leave the boundary entirely.
	checkSettings := func(s *core.Settings) error {
		if s.S3.Enabled {
			return fmt.Errorf("S3 file storage cannot be used with encryption at rest: documents would be stored unencrypted outside the vault")
		}
		if s.Backups.S3.Enabled {
			return fmt.Errorf("S3 backup storage cannot be used with encryption at rest: it would upload an unencrypted archive")
		}
		if s.Backups.Cron != "" {
			return fmt.Errorf("scheduled backups cannot be used with encryption at rest: they would write unencrypted archives")
		}
		return nil
	}

	app.OnBootstrap().Bind(&hook.Handler[*core.BootstrapEvent]{
		Priority: 100,
		Func: func(e *core.BootstrapEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			if err := checkSettings(e.App.Settings()); err != nil {
				return fmt.Errorf("vault: refusing to start: %w", err)
			}
			return nil
		},
	})
	app.OnSettingsUpdateRequest().BindFunc(func(e *core.SettingsUpdateRequestEvent) error {
		if err := checkSettings(e.NewSettings); err != nil {
			return err
		}
		return e.Next()
	})
}

// registerEnrollment keeps the keyring in step with the accounts that exist.
//
// Every user of an instance can unlock it, so each one needs their own wrap of
// the master key. These are the *model* hooks, not the request hooks: accounts
// are also created server-side — the setup wizard, `superuser upsert`, the
// paired-admin path — and none of those go through the record API, so binding
// only the request hooks would silently leave those users unable to unlock.
//
// The password is read before e.Next() because PocketBase clears the plaintext
// once the record is persisted, and the wrap is added after, so a save that
// fails does not leave a credential behind for a user that does not exist.
func registerEnrollment(app *pocketbase.PocketBase, v *Vault) {
	enrollOnSave := func(e *core.RecordEvent) error {
		password := e.Record.GetString("password")
		if err := e.Next(); err != nil {
			return err
		}
		if password == "" {
			return nil
		}
		return enroll(v, e.Record.Id, password)
	}

	// Both collections: an operator may only ever have a superuser account, and
	// it still has to be able to unlock the archive.
	app.OnRecordCreate("users", "_superusers").BindFunc(enrollOnSave)
	app.OnRecordUpdate("users", "_superusers").BindFunc(enrollOnSave)

	app.OnRecordAfterDeleteSuccess("users", "_superusers").BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if v.Keyring() == nil {
			return nil
		}
		err := v.UpdateKeyring(func(kr *Keyring) error {
			return kr.RemoveWrapsForUser(e.Record.Id)
		})
		if errors.Is(err, ErrLastWrap) {
			// Refusing to remove the last credential is correct, not an error
			// worth failing the delete over.
			v.opts.Log("vault: keeping the wrap for deleted account %s: %v", e.Record.Id, err)
		} else if err != nil {
			v.opts.Log("vault: failed to persist keyring after deleting account %s: %v", e.Record.Id, err)
		}
		return nil
	})
}

func enroll(v *Vault, userID, password string) error {
	if !v.Loaded() {
		return fmt.Errorf("the instance is locked, so this password cannot be enrolled for decryption; unlock it first")
	}
	if v.Keyring() == nil {
		return nil
	}
	err := v.UpdateKeyring(func(kr *Keyring) error {
		if err := kr.AddPassword(v.MasterKey(), userID, password); err != nil {
			return fmt.Errorf("vault: enroll %s: %w", userID, err)
		}
		// A real credential now exists, so the one the vault was created with
		// stops being the only way in and starts being a permanent spare key
		// held by whoever provisioned the instance. Removed in the same save as
		// the wrap that replaces it, so no window exists where neither is on
		// disk.
		if err := kr.RemoveBootstrapWrap(); err != nil {
			return fmt.Errorf("vault: revoke the bootstrap credential for %s: %w", userID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	v.opts.Log("vault: enrolled a credential for user %s", userID)
	return nil
}
