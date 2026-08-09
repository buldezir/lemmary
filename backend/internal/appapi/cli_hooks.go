package appapi

import (
	"fmt"
	"log"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/cmd"
	"github.com/spf13/cobra"
)

// RegisterSystemCommands registers PocketBase's built-in serve/superuser commands
// and wraps superuser create/update/upsert/delete so the paired users account
// stays in sync. Call this instead of app.Start(); then call app.Execute().
//
// app.Start() registers these commands itself, so hooks registered before Start
// never find them.
func RegisterSystemCommands(app *pocketbase.PocketBase, showStartBanner bool) {
	app.RootCmd.AddCommand(cmd.NewSuperuserCommand(app))
	app.RootCmd.AddCommand(cmd.NewServeCommand(app, showStartBanner))
	wrapSuperuserCLIHooks(app)
}

func wrapSuperuserCLIHooks(app *pocketbase.PocketBase) {
	for _, name := range []string{"upsert", "create", "update"} {
		sub, _, err := app.RootCmd.Find([]string{"superuser", name})
		if err != nil || sub == nil || sub.RunE == nil {
			log.Printf("warning: could not wrap superuser %s command", name)
			continue
		}
		orig := sub.RunE
		sub.RunE = func(c *cobra.Command, args []string) error {
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

	delCmd, _, err := app.RootCmd.Find([]string{"superuser", "delete"})
	if err != nil || delCmd == nil || delCmd.RunE == nil {
		log.Printf("warning: could not wrap superuser delete command")
		return
	}
	origDel := delCmd.RunE
	delCmd.RunE = func(c *cobra.Command, args []string) error {
		if err := origDel(c, args); err != nil {
			return err
		}
		if len(args) < 1 {
			return nil
		}
		if err := RevokePairedAdmin(app, args[0]); err != nil {
			return fmt.Errorf("superuser deleted but paired admin revoke failed: %w", err)
		}
		log.Printf("Also revoked paired admin on users account %q", args[0])
		return nil
	}
}
