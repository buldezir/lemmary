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

// pendingLogins is never sent to the client: the device auth id is the half
// that completes a sign-in, so handing it out would let anyone who saw one
// finish somebody else's login. A restart drops them, which is fine for a code
// good for fifteen minutes.
var pendingLogins sync.Map // provider id -> *chatgpt.Pending

// loginLocks serializes poll-exchange-save per provider: the authorization code
// a poll returns is single-use, so two overlapping polls would both read the
// same pending login and the second exchange would be refused for a login that
// had just succeeded. Never pruned; one mutex per chatgpt provider row.
var loginLocks sync.Map // provider id -> *sync.Mutex

func lockLogin(providerID string) func() {
	value, _ := loginLocks.LoadOrStore(providerID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// chatgptClient reads the endpoints rather than closing over them, so the
// e2e suite's stub host is picked up by a handler registered before it existed.
func chatgptClient() *chatgpt.Client {
	return chatgpt.NewClient(nil).WithEndpoints(chatgpt.AuthEndpoints())
}

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

func handleChatGPTDeviceStart(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		record, err := chatgptProvider(app, e)
		if record == nil {
			return err
		}

		pending, startErr := chatgptClient().StartDeviceLogin(e.Request.Context())
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

// handleChatGPTDevicePoll makes one attempt per request so the browser owns the
// interval; blocking for the full fifteen minutes would hold a connection open
// and give the operator no way to cancel.
func handleChatGPTDevicePoll(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		record, err := chatgptProvider(app, e)
		if record == nil {
			return err
		}

		unlock := lockLogin(record.Id)
		defer unlock()

		// Read after the lock, not before: a poll that queued behind another
		// one is asking about a login that may already be finished.
		value, ok := pendingLogins.Load(record.Id)
		if !ok {
			// A poll whose turn came after the login completed: the row is
			// signed in, so reporting expired would put an error on a row
			// that is fine.
			if fresh, findErr := app.FindRecordById(aiprovider.CollectionName, record.Id); findErr == nil &&
				strings.TrimSpace(fresh.GetString(aiprovider.OAuthField)) != "" {
				return writeJSON(e, http.StatusOK, map[string]any{
					"status":   "complete",
					"provider": providerJSON(aiprovider.FromRecord(fresh)),
				})
			}
			return writeJSON(e, http.StatusOK, map[string]any{"status": "expired"})
		}
		pending := value.(*chatgpt.Pending)

		tok, pollErr := chatgptClient().PollDeviceLogin(e.Request.Context(), pending)
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
		// Retired before the write: saving fires the provider hook, which
		// registers a source for the new token, and forgetting afterwards would
		// unregister the source those clients hold.
		chatgpt.Forget(record.Id)
		record.Set(aiprovider.OAuthField, raw)
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to store the ChatGPT sign-in.")
		}

		return writeJSON(e, http.StatusOK, map[string]any{
			"status":   "complete",
			"provider": providerJSON(aiprovider.FromRecord(record)),
		})
	}
}

// handleChatGPTSignOut clears the token but keeps the provider row.
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
		// Before the write, so a refresh already on the wire cannot store its
		// rotated pair and undo this. If the save then fails the row keeps its
		// token but this process serves nothing from it.
		chatgpt.Forget(record.Id)
		record.Set(aiprovider.OAuthField, "")
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to sign out.")
		}
		return writeJSON(e, http.StatusOK, providerJSON(aiprovider.FromRecord(record)))
	}
}

// The disabled-device-code case gets its own status because it is the first
// thing most operators hit and the fix is a setting, not a retry.
func writeChatGPTAuthError(e *core.RequestEvent, app core.App, err error) error {
	if errors.Is(err, chatgpt.ErrDeviceAuthDisabled) {
		return writeError(e, http.StatusConflict, chatgpt.ErrDeviceAuthDisabled.Error())
	}
	app.Logger().Warn("chatgpt device sign-in failed", "error", err)
	return writeError(e, http.StatusBadGateway, "ChatGPT sign-in failed: "+err.Error())
}
