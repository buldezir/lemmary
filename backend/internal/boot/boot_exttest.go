//go:build lemmary_exttest

package boot

import (
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/appargs"
)

// This file is the throwaway pre-boot step the seam tests build against, under
// the lemmary_exttest tag. It ships in the open-source repository for the same
// reason appwire/edition_exttest.go does: the private build lives in a fork
// nobody here can build, so without a stand-in the only thing verifying this
// seam would be that fork's own CI, and it would rot upstream between merges
// with nothing failing.
//
// It exercises every field of Boot and Result. A field that stops being wired
// makes boot_exttest_test.go fail here, and the HTTP-level assertions in the
// private development suite (dev/e2e/boot_exttest_test.go, present only when
// that overlay is cloned into backend/dev/) fail too.

// ExtTestSubcommand is answered without constructing an app, which is what
// Result.Handled is for.
const ExtTestSubcommand = "exttest-boot"

// ExtTestHandledCode is the exit status that subcommand reports. Deliberately
// not 0 or 1, so a test cannot pass against an accidental success or a generic
// failure.
const ExtTestHandledCode = 7

// ExtTestDataDirName is the subdirectory Result.DataDir relocates the app into.
const ExtTestDataDirName = "boot-relocated"

// extTestClosed counts Close calls so a test can prove the cleanup deferred in
// main actually runs. A counter rather than a bool: closing twice is as much a
// bug as never closing.
var extTestClosed atomic.Int64

// ExtTestCloseCount reports how many times Result.Close has run.
func ExtTestCloseCount() int64 { return extTestClosed.Load() }

func current() Boot {
	return Boot{
		Name: "exttest",

		Prepare: func(argv []string) (Result, error) {
			closeFn := func() error {
				extTestClosed.Add(1)
				return nil
			}

			// Answered here rather than as a cobra command, which is the whole
			// point of Handled: a cobra command runs inside app.Execute, after
			// the database this must not open.
			if bare := appargs.Bare(argv); len(bare) > 0 && bare[0] == ExtTestSubcommand {
				return Result{Handled: true, Code: ExtTestHandledCode, Close: closeFn}, nil
			}

			// Relocation is opt-in via --dir so that every other test under
			// this tag keeps running against the default path. Reading it
			// through appargs is also what pins appargs to a caller: a change
			// there that stops finding a flag's value fails this seam's tests.
			dir := appargs.Flag(argv, "--dir")
			if dir == "" {
				return Result{Close: closeFn}, nil
			}
			relocated := filepath.Join(dir, ExtTestDataDirName)
			if err := os.MkdirAll(relocated, 0o700); err != nil {
				return Result{}, err
			}

			return Result{
				DataDir: relocated,
				Register: func(app *pocketbase.PocketBase) {
					app.OnServe().BindFunc(func(e *core.ServeEvent) error {
						e.Router.GET("/api/exttest/boot", func(re *core.RequestEvent) error {
							return re.JSON(http.StatusOK, map[string]any{
								"boot":     "exttest",
								"data_dir": app.DataDir(),
							})
						})
						return e.Next()
					})
				},
				Close: closeFn,
			}, nil
		},
	}
}
