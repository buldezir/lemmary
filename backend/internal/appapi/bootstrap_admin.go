package appapi

import (
	"errors"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// The credentials an instance can be handed its first admin account with.
const (
	EnvSetupAdminEmail    = "SETUP_ADMIN_EMAIL"
	EnvSetupAdminPassword = "SETUP_ADMIN_PASSWORD"
)

// RegisterAdminBootstrap creates the first admin from the environment.
//
// This is what turns `docker compose up` into a usable instance: with these two
// set beside an AI key, a fresh volume comes up already signed-in-able, and
// nobody has to walk the setup wizard to test a change. In a development build
// the SPA reads the same pair for its auto-login, which is why they are one
// variable each rather than a backend pair and a VITE_ pair that had to be kept
// in step by hand.
//
// Create-if-absent, never upsert. Upserting would reset the password on every
// restart of a long-lived container, silently undoing an admin who had changed
// it; and the wizard is still there for an instance that would rather be asked.
// An account that already exists is left alone without comment, because that is
// the ordinary state of every boot after the first.
func RegisterAdminBootstrap(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}

		email := strings.TrimSpace(os.Getenv(EnvSetupAdminEmail))
		password := os.Getenv(EnvSetupAdminPassword)
		if email == "" || password == "" {
			return nil
		}

		_, _, err := CreateFirstAdmin(e.App, email, password)
		switch {
		case err == nil:
			// The email is not a secret and naming it is the point: it is the
			// account somebody is about to sign in with. The password is never
			// logged, here or anywhere.
			e.App.Logger().Info("created the first admin account from the environment",
				"env", EnvSetupAdminEmail, "email", email)
		case errors.Is(err, errAdminExists):
			// Every boot after the first.
		case errors.Is(err, errInvalidAdmin):
			e.App.Logger().Error("ignoring the admin bootstrap: the credentials are not usable",
				"env", EnvSetupAdminEmail, "error", err)
		default:
			// Warn rather than fail, for the same reason bootstrap tolerates a
			// bad settings record: the app must come up so the wizard can offer
			// the account this could not create.
			e.App.Logger().Warn("admin bootstrap failed; the setup wizard will ask instead",
				"error", err)
		}
		return nil
	})
}
