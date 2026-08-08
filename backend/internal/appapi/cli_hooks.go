package appapi

import (
	"fmt"
	"log"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
)

// RegisterSuperuserCLIHooks wraps PocketBase superuser create/update/upsert so
// a matching users-collection account is kept in sync (document ownership).
func RegisterSuperuserCLIHooks(app *pocketbase.PocketBase) {
	for _, name := range []string{"upsert", "create", "update"} {
		cmd, _, err := app.RootCmd.Find([]string{"superuser", name})
		if err != nil || cmd == nil || cmd.RunE == nil {
			continue
		}
		orig := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if err := orig(c, args); err != nil {
				return err
			}
			if len(args) < 2 {
				return nil
			}
			if _, err := UpsertPairedUser(app, args[0], args[1]); err != nil {
				return fmt.Errorf("superuser saved but paired users account failed: %w", err)
			}
			log.Printf("Also saved paired users account %q", args[0])
			return nil
		}
	}
}
