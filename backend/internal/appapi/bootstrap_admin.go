package appapi

import (
	"errors"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	EnvSetupAdminEmail    = "SETUP_ADMIN_EMAIL"
	EnvSetupAdminPassword = "SETUP_ADMIN_PASSWORD"
)

// RegisterAdminBootstrap creates the first admin from SETUP_ADMIN_* if absent.
// Never upsert: that would reset the password on every restart.
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
			// Email is not a secret; the password is never logged.
			e.App.Logger().Info("created the first admin account from the environment",
				"env", EnvSetupAdminEmail, "email", email)
		case errors.Is(err, errAdminExists):
			// already exists
		case errors.Is(err, errInvalidAdmin):
			e.App.Logger().Error("ignoring the admin bootstrap: the credentials are not usable",
				"env", EnvSetupAdminEmail, "error", err)
		default:
			// Warn rather than fail so the wizard can offer the account.
			e.App.Logger().Warn("admin bootstrap failed; the setup wizard will ask instead",
				"error", err)
		}
		return nil
	})
}
