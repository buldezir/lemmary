package appapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/passkey"
)

// One store for both registration and login: the handles are opaque and
// single-use, and go-webauthn checks the session data against the ceremony
// type, so a registration handle is worthless to the login endpoint.
var passkeyChallenges = passkey.NewChallengeStore()

var errLastSignInMethod = errors.New("passkey is the last sign-in method")

// passkeyMaxBodyBytes caps the attestation/assertion payload: real ones run to
// a few kilobytes, and PocketBase's route default is 32MB on an endpoint anyone
// can reach.
const passkeyMaxBodyBytes = 64 << 10

type passkeyBeginResponse struct {
	SessionID string `json:"session_id"`
	Options   any    `json:"options"`
}

type passkeyRegisterFinishRequest struct {
	SessionID  string          `json:"session_id"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyLoginFinishRequest struct {
	SessionID  string          `json:"session_id"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyRenameRequest struct {
	Name string `json:"name"`
}

// webauthnFor maps the "this address cannot carry a passkey" cases to a 4xx
// with an explanation rather than a 500.
func webauthnFor(app core.App, e *core.RequestEvent) (*webauthn.WebAuthn, error) {
	w, err := passkey.NewForRequest(e.Request, resolvedAppName(app))
	if err != nil {
		if passkey.IsOriginError(err) {
			return nil, writeError(e, http.StatusBadRequest, passkey.Message(err))
		}
		app.Logger().Error("passkey relying party config failed", "error", err)
		return nil, writeError(e, http.StatusInternalServerError, "Failed to prepare the passkey request.")
	}
	return w, nil
}

// passkeyLoginAvailable reports whether the login screen should offer the passkey
// button: the address can carry a ceremony, and at least one credential exists.
// Instance-wide rather than per-account so it is not an enumeration signal.
// Never errors — a failure to answer is "no", since the fallback is the password form.
func passkeyLoginAvailable(app core.App, e *core.RequestEvent) bool {
	if !passkey.Available(e.Request) {
		return false
	}
	total, err := app.CountRecords(passkey.CollectionName)
	if err != nil {
		// Most likely the migration has not applied: RunAppMigrations only warns
		// and continues, so the collection can be absent at runtime.
		return false
	}
	return total > 0
}

// passkeyAccount resolves the users record the caller's passkeys belong to. A
// superuser session acts on its paired users account: a credential enrolled
// against _superusers could only mint superuser tokens, which document
// ownership cannot use.
func passkeyAccount(app core.App, e *core.RequestEvent) (*core.Record, error) {
	userID, err := resolveOwnerUserID(app, e)
	if err != nil {
		return nil, writeOwnerError(e, err)
	}
	record, err := app.FindRecordById(passkey.UsersCollectionName, userID)
	if err != nil {
		app.Logger().Error("passkey account lookup failed", "user", userID, "error", err)
		return nil, writeError(e, http.StatusInternalServerError, "Failed to load the account.")
	}
	return record, nil
}

// maxExclusions caps the excludeCredentials list: some CTAP2 security keys
// error out on a long one, turning "you have a lot of passkeys" into "you can
// no longer add one". The list is only an optimisation, since a duplicate is
// caught by the unique index on credential_id anyway. Newest first.
const maxExclusions = 20

func exclusionList(credentials []webauthn.Credential) []protocol.CredentialDescriptor {
	if len(credentials) > maxExclusions {
		credentials = credentials[:maxExclusions]
	}
	return webauthn.Credentials(credentials).CredentialDescriptors()
}

// A full challenge store is load shedding, not a fault, so it answers 429 and
// says the attempt is worth repeating.
func writeChallengeError(app core.App, e *core.RequestEvent, err error, fallback string) error {
	if errors.Is(err, passkey.ErrTooManyChallenges) {
		return writeError(e, http.StatusTooManyRequests,
			"Too many sign-in attempts are in progress. Try again in a minute.")
	}
	app.Logger().Error("passkey issue challenge failed", "error", err)
	return writeError(e, http.StatusInternalServerError, fallback)
}

func handlePostPasskeyRegisterBegin(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		w, err := webauthnFor(app, e)
		if err != nil {
			return err
		}
		userRecord, err := passkeyAccount(app, e)
		if err != nil {
			return err
		}
		account, err := passkey.NewAccount(app, userRecord)
		if err != nil {
			app.Logger().Error("passkey load credentials failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to load existing passkeys.")
		}

		creation, session, err := w.BeginRegistration(
			account,
			// Discoverable credentials make the usernameless button possible:
			// the authenticator names the account without being told who is
			// signing in.
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
			// Enrolling the same authenticator twice becomes the browser's own
			// InvalidStateError instead of a duplicate row.
			webauthn.WithExclusions(exclusionList(account.WebAuthnCredentials())),
		)
		if err != nil {
			app.Logger().Error("passkey begin registration failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to start passkey registration.")
		}

		handle, err := passkeyChallenges.Issue(session)
		if err != nil {
			return writeChallengeError(app, e, err, "Failed to start passkey registration.")
		}
		return writeJSON(e, http.StatusOK, passkeyBeginResponse{SessionID: handle, Options: creation})
	}
}

func handlePostPasskeyRegisterFinish(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req passkeyRegisterFinishRequest
		if err := decodePasskeyBody(e, &req.SessionID, &req.Name, &req.Credential); err != nil {
			return err
		}

		w, err := webauthnFor(app, e)
		if err != nil {
			return err
		}
		userRecord, err := passkeyAccount(app, e)
		if err != nil {
			return err
		}
		account, err := passkey.NewAccount(app, userRecord)
		if err != nil {
			app.Logger().Error("passkey load credentials failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to load existing passkeys.")
		}

		session, err := passkeyChallenges.Consume(req.SessionID)
		if err != nil {
			return writeError(e, http.StatusBadRequest, "This passkey request expired. Start again.")
		}

		// FinishRegistration wants an *http.Request whose body is the bare
		// PublicKeyCredential, but this body also carries the handle and label.
		// Parse-then-validate is the path FinishRegistration takes internally.
		parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
		if err != nil {
			return writeError(e, http.StatusBadRequest, "The passkey response could not be read.")
		}
		credential, err := w.CreateCredential(account, session, parsed)
		if err != nil {
			app.Logger().Warn("passkey registration rejected", "error", err)
			return writeError(e, http.StatusBadRequest, "This passkey could not be verified.")
		}

		record, err := passkey.Create(app, userRecord.Id, req.Name, credential)
		if err != nil {
			app.Logger().Error("passkey create failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to save the passkey.")
		}
		return writeJSON(e, http.StatusCreated, map[string]any{"passkey": passkey.ToInfo(record)})
	}
}

func handleGetPasskeys(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		userRecord, err := passkeyAccount(app, e)
		if err != nil {
			return err
		}
		records, err := passkey.List(app, userRecord.Id)
		if err != nil {
			app.Logger().Error("passkey list failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list passkeys.")
		}
		infos := make([]passkey.Info, 0, len(records))
		for _, record := range records {
			infos = append(infos, passkey.ToInfo(record))
		}
		return writeJSON(e, http.StatusOK, map[string]any{"passkeys": infos})
	}
}

func handlePatchPasskey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req passkeyRenameRequest
		if err := e.BindBody(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		userRecord, err := passkeyAccount(app, e)
		if err != nil {
			return err
		}
		record, err := passkey.FindOwned(app, userRecord.Id, e.Request.PathValue("id"))
		if err != nil {
			return writePasskeyLookupError(app, e, err)
		}
		if err := passkey.Rename(app, record, req.Name); err != nil {
			app.Logger().Error("passkey rename failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to rename the passkey.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{"passkey": passkey.ToInfo(record)})
	}
}

func handleDeletePasskey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		userRecord, err := passkeyAccount(app, e)
		if err != nil {
			return err
		}
		recordID := e.Request.PathValue("id")

		// Count, guard and delete in one transaction: two concurrent deletes would
		// otherwise both read a total of two, both pass the guard, and leave a
		// passkey-only account with no way in. PocketBase serializes writes through
		// one connection, so the second transaction's count sees the first delete.
		err = app.RunInTransaction(func(txApp core.App) error {
			record, err := passkey.FindOwned(txApp, userRecord.Id, recordID)
			if err != nil {
				return err
			}
			last, err := isLastSignInMethod(txApp, userRecord)
			if err != nil {
				return err
			}
			if last {
				total, err := passkey.Count(txApp, userRecord.Id)
				if err != nil {
					return err
				}
				if total <= 1 {
					return errLastSignInMethod
				}
			}
			return txApp.Delete(record)
		})
		switch {
		case errors.Is(err, errLastSignInMethod):
			return writeError(e, http.StatusConflict,
				"This passkey is the only way to sign in to this account. Enable password sign-in or add another passkey first.")
		case errors.Is(err, passkey.ErrNotFound):
			return writePasskeyLookupError(app, e, err)
		case err != nil:
			app.Logger().Error("passkey delete failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to remove the passkey.")
		}
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handlePostPasskeyLoginBegin(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		w, err := webauthnFor(app, e)
		if err != nil {
			return err
		}
		// Discoverable login: no user is named, so an unauthenticated caller
		// learns only that the server can issue a challenge.
		assertion, session, err := w.BeginDiscoverableLogin()
		if err != nil {
			app.Logger().Error("passkey begin login failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to start passkey sign-in.")
		}
		handle, err := passkeyChallenges.Issue(session)
		if err != nil {
			return writeChallengeError(app, e, err, "Failed to start passkey sign-in.")
		}
		return writeJSON(e, http.StatusOK, passkeyBeginResponse{SessionID: handle, Options: assertion})
	}
}

func handlePostPasskeyLoginFinish(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req passkeyLoginFinishRequest
		if err := decodePasskeyBody(e, &req.SessionID, nil, &req.Credential); err != nil {
			return err
		}

		w, err := webauthnFor(app, e)
		if err != nil {
			return err
		}
		session, err := passkeyChallenges.Consume(req.SessionID)
		if err != nil {
			return writeError(e, http.StatusBadRequest, "This sign-in request expired. Try again.")
		}
		parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
		if err != nil {
			return writeError(e, http.StatusBadRequest, "The passkey response could not be read.")
		}

		// Captured by the handler below so the sign counter can be written back
		// to the right row afterwards.
		var matched *core.Record
		handler := func(rawID, userHandle []byte) (webauthn.User, error) {
			credentialRecord, err := passkey.FindByCredentialID(app, rawID)
			if err != nil {
				return nil, err
			}
			userRecord, err := app.FindRecordById(passkey.UsersCollectionName, credentialRecord.GetString("user"))
			if err != nil {
				return nil, err
			}
			// The user handle is the record id written at registration, so a
			// mismatch means another deployment's credential collided on raw ID.
			if len(userHandle) > 0 && string(userHandle) != userRecord.Id {
				return nil, errors.New("user handle does not match the credential owner")
			}
			matched = credentialRecord
			return passkey.NewAccount(app, userRecord)
		}

		user, credential, err := w.ValidatePasskeyLogin(handler, session, parsed)
		if err != nil || matched == nil {
			app.Logger().Warn("passkey login rejected", "error", err)
			return writeError(e, http.StatusUnauthorized, "That passkey was not accepted.")
		}
		account, ok := user.(*passkey.Account)
		if !ok {
			app.Logger().Error("passkey login returned an unexpected user type")
			return writeError(e, http.StatusInternalServerError, "Failed to complete passkey sign-in.")
		}

		// A counter that did not advance is a possible cloned authenticator, so
		// the session is refused. This does not catch synced passkeys: go-webauthn
		// leaves CloneWarning clear for an authenticator that reports zero forever
		// (iCloud Keychain, Google Password Manager).
		if credential.Authenticator.CloneWarning {
			app.Logger().Warn("passkey sign counter did not advance; refusing the session",
				"record", matched.Id)
			return writeError(e, http.StatusUnauthorized, "That passkey was not accepted.")
		}

		// Not writing the advanced counter back turns the clone detection above
		// into a no-op. A failure here does not fail the sign-in: the credential
		// is already verified, and refusing over a transient write would lock
		// someone out to protect bookkeeping.
		if err := passkey.TouchCredential(app, matched, credential); err != nil {
			app.Logger().Error("passkey counter write-back failed", "record", matched.Id, "error", err)
		}

		// PocketBase's own auth response, so the token, the auth hooks and the
		// _authOrigins bookkeeping behave as they do for a password login.
		return apis.RecordAuthResponse(e, account.Record(), "passkey", nil)
	}
}

// decodePasskeyBody must use e.BindBody, not a json.Decoder: on the login route
// apis.RecordAuthResponse reads the body again through e.RequestInfo(), and only
// BindBody calls Reread() to rewind it. Decoding by hand turns an
// already-verified sign-in into a 500. Requires Content-Type: application/json.
func decodePasskeyBody(e *core.RequestEvent, sessionID, name *string, credential *json.RawMessage) error {
	var body struct {
		SessionID  string          `json:"session_id"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := e.BindBody(&body); err != nil {
		return writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	if len(bytes.TrimSpace(body.Credential)) == 0 {
		return writeError(e, http.StatusBadRequest, "A passkey response is required.")
	}
	*sessionID = body.SessionID
	*credential = body.Credential
	if name != nil {
		*name = body.Name
	}
	return nil
}

func writePasskeyLookupError(app core.App, e *core.RequestEvent, err error) error {
	if errors.Is(err, passkey.ErrNotFound) {
		return writeError(e, http.StatusNotFound, "Passkey not found.")
	}
	app.Logger().Error("passkey lookup failed", "error", err)
	return writeError(e, http.StatusInternalServerError, "Failed to load the passkey.")
}

// isLastSignInMethod is false in a default install, where password auth is on.
// It exists for the OAuth2-only configuration docs/oauth.md describes, where
// deleting the last passkey would lock the account out entirely.
func isLastSignInMethod(finder externalAuthFinder, userRecord *core.Record) (bool, error) {
	collection := userRecord.Collection()
	if collection.PasswordAuth.Enabled {
		return false, nil
	}
	// An _externalAuths row is not by itself a way in: it survives both turning
	// OAuth2 off and removing the provider, and PocketBase refuses the sign-in
	// either way, so a stale row must not count as a method.
	if !collection.OAuth2.Enabled {
		return true, nil
	}
	externals, err := finder.FindAllExternalAuthsByRecord(userRecord)
	if err != nil {
		return false, err
	}
	for _, external := range externals {
		if _, ok := collection.OAuth2.GetProviderConfig(external.Provider()); ok {
			return false, nil
		}
	}
	return true, nil
}

type externalAuthFinder interface {
	FindAllExternalAuthsByRecord(*core.Record) ([]*core.ExternalAuth, error)
}
