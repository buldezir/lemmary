package appapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/passkey"
)

// passkeyChallenges holds the in-flight WebAuthn ceremonies for this process.
// One store for both registration and login: the handles are opaque and
// single-use, and a registration handle is worthless to the login endpoint
// because the session data it carries is checked against the ceremony type by
// go-webauthn.
var passkeyChallenges = passkey.NewChallengeStore()

// errLastSignInMethod aborts the delete transaction when the credential turns out
// to be the account's only remaining way in.
var errLastSignInMethod = errors.New("passkey is the last sign-in method")

// passkeyMaxBodyBytes caps the attestation/assertion payload. Real ones run to a
// few kilobytes; PocketBase's route default is 32MB, which is absurd for a JSON
// envelope on an endpoint anyone can reach.
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

// pocketBaseDefaultAppName is what PocketBase seeds Meta.AppName with on a fresh
// install. Treated as "not set yet" for passkeys specifically: this string is
// baked into the platform passkey manager at creation time and cannot be changed
// afterwards, so an install whose admin has not renamed the app yet would leave
// "Acme" in somebody's iCloud Keychain permanently. The header can show the
// placeholder harmlessly; a credential cannot.
const pocketBaseDefaultAppName = "Acme"

// passkeyDisplayName is the relying-party name shown in the OS credential picker.
func passkeyDisplayName(app core.App) string {
	name := strings.TrimSpace(app.Settings().Meta.AppName)
	if name == "" || name == pocketBaseDefaultAppName {
		return defaultAppName
	}
	return name
}

// webauthnFor builds the relying-party config for this request, mapping the
// "this address cannot carry a passkey" cases to a 4xx with an explanation rather
// than a 500.
func webauthnFor(app core.App, e *core.RequestEvent) (*webauthn.WebAuthn, error) {
	w, err := passkey.NewForRequest(e.Request, passkeyDisplayName(app))
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
// button: the request's address can carry a ceremony, and at least one credential
// exists to offer. Never errors — a failure to answer is answered as "no", since
// the fallback is the password form that was always there.
func passkeyLoginAvailable(app core.App, e *core.RequestEvent) bool {
	if !passkey.Available(e.Request) {
		return false
	}
	total, err := app.CountRecords(passkey.CollectionName)
	if err != nil {
		// Most likely the migration has not applied — RunAppMigrations only warns
		// and continues, so the collection can legitimately be absent at runtime.
		return false
	}
	return total > 0
}

// passkeyAccount resolves the users record the caller's passkeys belong to.
//
// resolveOwnerUserID is reused rather than rejecting superuser sessions: an admin
// who signed in through the PocketBase superuser path has no users record of its
// own, and handleExportDocuments already established that such a session acts on
// its paired users account. Enrolling a passkey against _superusers would produce
// a credential that could only ever mint a superuser token, which is not a
// session this app's document ownership can use.
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

// maxExclusions caps the excludeCredentials list. Some CTAP2 security keys error
// out on a long list, which would turn "you have a lot of passkeys" into "you can
// no longer add one" — a worse failure than the one exclusions prevent. The list
// is only an optimisation: without an entry the duplicate is caught server-side
// by the unique index on credential_id instead of by the browser, so trimming it
// costs a clearer error message and nothing else. Newest first, because those are
// the authenticators the account holder is most likely to still be using.
const maxExclusions = 20

func exclusionList(credentials []webauthn.Credential) []protocol.CredentialDescriptor {
	if len(credentials) > maxExclusions {
		credentials = credentials[:maxExclusions]
	}
	return webauthn.Credentials(credentials).CredentialDescriptors()
}

// writeChallengeError renders a challenge-store failure. A full store is load
// shedding, not a fault, so it answers 429 and says the attempt is worth
// repeating.
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
			// Discoverable credentials are what make the usernameless button
			// possible: the authenticator has to be able to name the account
			// without the app telling it who is signing in.
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
			// Enrolling the same authenticator twice becomes the browser's own
			// InvalidStateError instead of a second row that behaves identically
			// to the first.
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

		// The library's FinishRegistration wants an *http.Request whose body is
		// the bare PublicKeyCredential, but this endpoint's body also carries the
		// session handle and the label. Parse-then-validate is the same code path
		// FinishRegistration takes internally, so nothing is skipped.
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

		// Count, guard and delete inside one transaction, for the same reason
		// handlePostSetupAdmin does: two concurrent deletes of two different
		// credentials would otherwise both read a total of two, both pass the
		// guard, and both delete — leaving a passkey-only account with no way in.
		// PocketBase serializes writes through a single connection, so the second
		// transaction's count sees the first one's delete.
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
		// Discoverable login: no user is named, so nothing here reveals whether
		// any account or credential exists. That is the point of the usernameless
		// flow — an unauthenticated caller learns only that the server can issue
		// a challenge.
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

		// The record the assertion resolved to, captured by the handler below so
		// the sign counter can be written back to the right row afterwards.
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
			// The user handle is the record id this app wrote at registration, so
			// a mismatch means the assertion belongs to some other deployment's
			// credential that happens to collide on raw ID.
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

		// The signature counter failed to advance when it should have, which the
		// spec calls out as a possible cloned authenticator. Refuse the session.
		//
		// This does *not* catch the synced-passkey case, which was the worry:
		// go-webauthn's Authenticator.UpdateCounter sets CloneWarning only when
		// `authDataCount <= SignCount && (authDataCount != 0 || SignCount != 0)`,
		// so an authenticator that reports zero forever — iCloud Keychain, Google
		// Password Manager — leaves the flag clear and signs in normally. The flag
		// is set only when a real counter went backwards or stalled, so there is no
		// population of legitimate authenticators to protect by ignoring it, and
		// logging it while issuing the token anyway would make the check
		// decorative.
		if credential.Authenticator.CloneWarning {
			app.Logger().Warn("passkey sign counter did not advance; refusing the session",
				"record", matched.Id)
			return writeError(e, http.StatusUnauthorized, "That passkey was not accepted.")
		}

		// A counter that never moves is the classic WebAuthn storage bug: it turns
		// the clone detection above into a no-op. So the advanced counter is
		// written back on every login — but a failure here does not fail the
		// sign-in. The credential has already been verified; refusing the session
		// over a transient write would lock someone out of their account to protect
		// bookkeeping.
		if err := passkey.TouchCredential(app, matched, credential); err != nil {
			app.Logger().Error("passkey counter write-back failed", "record", matched.Id, "error", err)
		}

		// PocketBase's own auth response, so the token, the auth hooks and the
		// _authOrigins bookkeeping behave exactly as they do for a password login
		// and the SPA can adopt the session with no special case.
		return apis.RecordAuthResponse(e, account.Record(), "passkey", nil)
	}
}

// decodePasskeyBody reads the shared {session_id, name?, credential} envelope.
//
// e.BindBody rather than a json.Decoder over e.Request.Body, and on the login
// route that is load-bearing rather than a style choice. PocketBase wraps every
// request body in a rereadable reader, and apis.RecordAuthResponse reads the body
// again through e.RequestInfo() when it evaluates the collection's auth rule.
// Only BindBody rewinds the reader afterwards — it calls Reread() explicitly,
// precisely because a single json.Decode is not guaranteed to reach EOF and
// trigger the reset. Decoding by hand here would leave the body consumed and turn
// an already-verified sign-in into a 500.
//
// Consequence: the request must declare Content-Type: application/json, which is
// what both apiFetch and the auth module's own fetches send.
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

// isLastSignInMethod reports whether passkeys are the account's only remaining
// way in, which is the one case where removing the final one has to be refused.
//
// In a default install password auth is enabled, so this is false and every
// passkey can be deleted freely. It exists for the OAuth2-only configuration
// docs/oauth.md describes, where turning identity/password off and then deleting
// the last passkey would lock the account out of the app entirely.
func isLastSignInMethod(finder externalAuthFinder, userRecord *core.Record) (bool, error) {
	collection := userRecord.Collection()
	if collection.PasswordAuth.Enabled {
		return false, nil
	}
	// An _externalAuths row is not by itself a way in. The row survives both
	// turning OAuth2 off and removing that provider from the collection, and
	// PocketBase refuses the sign-in in either case — so counting a stale row as a
	// method is exactly how an account ends up with its last passkey deleted and
	// nothing that works.
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

// externalAuthFinder is the slice of core.App this check needs, narrowed so the
// rule can be tested without standing up an app.
type externalAuthFinder interface {
	FindAllExternalAuthsByRecord(*core.Record) ([]*core.ExternalAuth, error)
}
