package vault

import (
	"net/http"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/appapi"
)

// registerAPI exposes the small surface an operator and the SPA need.
//
// Everything here requires an authenticated session, which by definition means
// the instance is already unlocked — these endpoints never participate in
// unlocking, which happens before PocketBase exists at all.
func registerAPI(app *pocketbase.PocketBase, v *Vault) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		g := e.Router.Group("/api/vault")

		g.GET("/status", func(re *core.RequestEvent) error {
			if re.Auth == nil {
				return re.JSON(http.StatusUnauthorized, map[string]string{"message": "Authentication required."})
			}
			return re.JSON(http.StatusOK, v.Stats())
		})

		// Minting a recovery code needs the master key, so it is only possible
		// while unlocked. The code is returned exactly once.
		//
		// Admins only. A recovery code is a standalone credential for the whole
		// instance that outlives the account which created it: deleting a user
		// drops their own wrap, but a code they minted earlier would keep
		// working. Letting any signed-in user mint one hands a departing user a
		// permanent way back in.
		g.POST("/recovery-code", func(re *core.RequestEvent) error {
			if !appapi.IsAppAdmin(re) {
				return re.JSON(http.StatusForbidden, map[string]string{"message": "Admin access required."})
			}
			if !v.Loaded() {
				return re.JSON(http.StatusConflict, map[string]string{"message": "The vault is locked."})
			}
			var code string
			err := v.UpdateKeyring(func(kr *Keyring) error {
				var err error
				code, err = kr.AddRecoveryCode(v.MasterKey())
				return err
			})
			if err != nil {
				return err
			}
			v.opts.Log("vault: a new recovery code was issued")
			return re.JSON(http.StatusOK, map[string]string{
				"code": code,
				"note": "Write this down now. It is shown once, and it is the only way back in if every password and passkey is lost.",
			})
		})

		// An explicit flush, for operators who want a known-good point before
		// stopping a container.
		g.POST("/flush", func(re *core.RequestEvent) error {
			if !appapi.IsAppAdmin(re) {
				return re.JSON(http.StatusForbidden, map[string]string{"message": "Admin access required."})
			}
			if err := v.Flush("api"); err != nil {
				return err
			}
			return re.JSON(http.StatusOK, v.Stats())
		})

		return e.Next()
	})
}
