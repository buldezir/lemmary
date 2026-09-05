package appapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chatgpt"
	"lemmary/backend/internal/config"
)

// Shown when the chatgpt SDK is used on an instance that has not opted in.
const chatgptDisabledMessage = "Signing in with a ChatGPT subscription is not enabled on this instance. Set AI_CHATGPT_LOGIN=1 to turn it on."

// refuseWhenChatGPTDisabled guards every route that only makes sense for a
// signed-in ChatGPT provider, and every write that would create one.
//
// The twin of refuseWhenManaged, and for the same reason: hiding the SDK in
// Settings is a courtesy, and these endpoints stay reachable with any admin
// session.
func refuseWhenChatGPTDisabled(e *core.RequestEvent, rt *config.Runtime) (bool, error) {
	if rt.ChatGPTLogin() {
		return false, nil
	}
	return true, writeError(e, http.StatusForbidden, chatgptDisabledMessage)
}

// pendingLogins holds the device-code logins waiting on a browser.
//
// In memory, keyed by provider, and never sent to the client: the device auth
// id is the half that completes a sign-in, so handing it out would let anyone
// who saw one finish somebody else's login. The browser gets the user code and
// the URL, which are useless without an approval.
//
// A restart drops them. That is correct rather than a limitation -- the code is
// good for fifteen minutes and the operator is standing right there.
var pendingLogins sync.Map // provider id -> *chatgpt.Pending

func chatgptProvider(app core.App, e *core.RequestEvent) (*core.Record, error) {
	id := strings.TrimSpace(e.Request.PathValue("id"))
	record, err := app.FindRecordById(aiprovider.CollectionName, id)
	if err != nil {
		return nil, writeError(e, http.StatusNotFound, "Provider not found.")
	}
	if record.GetString("sdk") != aiprovider.SDKChatGPT {
		return nil, writeError(e, http.StatusBadRequest, "This provider does not sign in with ChatGPT.")
	}
	return record, nil
}

// handleChatGPTDeviceStart asks OpenAI for a user code and hands the operator
// the code and the URL to type it into.
func handleChatGPTDeviceStart(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		if refused, err := refuseWhenChatGPTDisabled(e, rt); refused {
			return err
		}
		record, err := chatgptProvider(app, e)
		if record == nil {
			return err
		}

		pending, startErr := chatgpt.NewClient(nil).StartDeviceLogin(e.Request.Context())
		if startErr != nil {
			return writeChatGPTAuthError(e, app, startErr)
		}
		pendingLogins.Store(record.Id, pending)

		return writeJSON(e, http.StatusOK, map[string]any{
			"user_code":        pending.UserCode,
			"verification_url": pending.VerificationURL,
			"interval_seconds": int(pending.Interval / time.Second),
			"expires_in":       int(time.Until(pending.ExpiresAt) / time.Second),
		})
	}
}

// handleChatGPTDevicePoll checks once whether the operator has approved, and
// stores the token if they have.
//
// One attempt per request so the browser owns the interval; a handler that
// blocked for the full fifteen minutes would hold a connection open and give
// the operator no way to cancel.
func handleChatGPTDevicePoll(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		if refused, err := refuseWhenChatGPTDisabled(e, rt); refused {
			return err
		}
		record, err := chatgptProvider(app, e)
		if record == nil {
			return err
		}

		value, ok := pendingLogins.Load(record.Id)
		if !ok {
			return writeJSON(e, http.StatusOK, map[string]any{"status": "expired"})
		}
		pending := value.(*chatgpt.Pending)

		tok, pollErr := chatgpt.NewClient(nil).PollDeviceLogin(e.Request.Context(), pending)
		switch {
		case errors.Is(pollErr, chatgpt.ErrAuthPending):
			return writeJSON(e, http.StatusOK, map[string]any{"status": "pending"})
		case errors.Is(pollErr, chatgpt.ErrAuthExpired):
			pendingLogins.Delete(record.Id)
			return writeJSON(e, http.StatusOK, map[string]any{"status": "expired"})
		case pollErr != nil:
			pendingLogins.Delete(record.Id)
			return writeChatGPTAuthError(e, app, pollErr)
		}
		pendingLogins.Delete(record.Id)

		raw, err := tok.Marshal()
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to store the ChatGPT sign-in.")
		}
		record.Set(aiprovider.OAuthField, raw)
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to store the ChatGPT sign-in.")
		}
		// Drop any source built from the previous token, so the next request
		// picks the new one up rather than refreshing a credential this
		// sign-in has just replaced.
		chatgpt.Forget(record.Id)

		return writeJSON(e, http.StatusOK, map[string]any{
			"status":   "complete",
			"provider": providerJSON(aiprovider.FromRecord(record)),
		})
	}
}

// handleChatGPTSignOut clears the stored token. The provider row stays: an
// operator signing out usually means signing in as somebody else.
//
// Deliberately not behind refuseWhenChatGPTDisabled. Turning the flag off is
// exactly when an operator most wants the stored token gone, and a sign-out
// they cannot perform would leave it sitting in the row.
func handleChatGPTSignOut(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		record, err := chatgptProvider(app, e)
		if record == nil {
			return err
		}
		pendingLogins.Delete(record.Id)
		record.Set(aiprovider.OAuthField, "")
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to sign out.")
		}
		chatgpt.Forget(record.Id)
		return writeJSON(e, http.StatusOK, providerJSON(aiprovider.FromRecord(record)))
	}
}

// writeChatGPTAuthError passes OpenAI's refusal through in words an admin can
// act on. The disabled-device-code case gets its own status because it is the
// first thing most operators hit and the fix is a setting, not a retry.
func writeChatGPTAuthError(e *core.RequestEvent, app core.App, err error) error {
	if errors.Is(err, chatgpt.ErrDeviceAuthDisabled) {
		return writeError(e, http.StatusConflict, chatgpt.ErrDeviceAuthDisabled.Error())
	}
	app.Logger().Warn("chatgpt device sign-in failed", "error", err)
	return writeError(e, http.StatusBadGateway, "ChatGPT sign-in failed: "+err.Error())
}
