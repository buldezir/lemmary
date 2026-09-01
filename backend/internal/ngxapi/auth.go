package ngxapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/bcrypt"
)

// dummyPasswordHash is compared when no user matches the identity, so response
// timing does not reveal whether a username exists. Cost matches PocketBase's
// default password hashing cost.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("ngxapi timing equalizer"), bcrypt.DefaultCost)
	if err != nil {
		return nil
	}
	return hash
})

type tokenRequest struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

func handleToken(e *core.RequestEvent) error {
	if e.Request.Method != http.MethodPost {
		return handleTokenMethodNotAllowed(e)
	}
	if err := checkAPIVersion(e); err != nil {
		return err
	}
	var req tokenRequest
	if err := e.BindBody(&req); err != nil {
		return badRequest(e, "Invalid request body.")
	}

	identity := strings.TrimSpace(req.Username)
	if identity == "" {
		return badRequest(e, "Username is required.")
	}
	if req.Password == "" {
		return badRequest(e, "Password is required.")
	}

	record, err := authenticateWithPassword(e.App, identity, req.Password)
	if err != nil {
		return unauthorized(e, "Unable to log in with provided credentials.")
	}

	token, err := record.NewAuthToken()
	if err != nil {
		return internalError(e, err)
	}

	return writeJSON(e, http.StatusOK, map[string]string{"token": token})
}

func handleTokenMethodNotAllowed(e *core.RequestEvent) error {
	return methodNotAllowed(e, "POST, OPTIONS")
}

func requireAuth(e *core.RequestEvent) error {
	if e.Auth != nil {
		return nil
	}

	header := e.Request.Header.Get("Authorization")
	if header != "" {
		lower := strings.ToLower(header)
		var token string
		switch {
		case strings.HasPrefix(lower, "token "):
			token = strings.TrimSpace(header[6:])
		case strings.HasPrefix(lower, "bearer "):
			token = strings.TrimSpace(header[7:])
		default:
			token = strings.TrimSpace(header)
		}

		if token != "" {
			record, err := e.App.FindAuthRecordByToken(token, core.TokenTypeAuth)
			if err == nil && record != nil {
				e.Auth = record
				return nil
			}
		}
	}

	if username, password, ok := e.Request.BasicAuth(); ok {
		record, err := authenticateWithPassword(e.App, username, password)
		if err == nil {
			e.Auth = record
			return nil
		}
	}

	return unauthorized(e, "Authentication credentials were not provided.")
}

func authenticateWithPassword(app core.App, identity, password string) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, err
	}
	// The collection's auth policy, honoured here as well as on PocketBase's
	// own auth routes.
	//
	// This flow is hand-rolled because the Paperless-compatible API predates
	// PocketBase's and has to answer in its own shape, which means every policy
	// PocketBase enforces has to be enforced again here or it is not enforced at
	// all -- the records keep their hashes, and POST /api/token and Basic auth
	// go on accepting them. Each check below is a door an operator believes they
	// closed.
	if err := checkCollectionAuthPolicy(collection); err != nil {
		return nil, err
	}

	var record *core.Record
	for _, field := range collection.PasswordAuth.IdentityFields {
		candidate, findErr := findUserByField(app, collection, field, identity)
		if findErr != nil {
			if errors.Is(findErr, sql.ErrNoRows) {
				continue
			}
			return nil, findErr
		}
		record = candidate
		break
	}

	if record == nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
		return nil, errors.New("invalid credentials")
	}
	if !record.ValidatePassword(password) {
		return nil, errors.New("invalid credentials")
	}

	return record, nil
}

// checkCollectionAuthPolicy refuses a password-only sign-in the collection's
// settings say is not enough on its own.
//
// Password auth disabled is the plain case: it is how an operator moves an
// install to OAuth or passkeys only.
//
// MFA is the one that matters more, because it fails in the more dangerous
// direction. With it on, PocketBase's own routes answer a correct password with
// an mfaId and demand a second factor from a different method; this endpoint
// would hand out a full auth token for the password alone, so enabling MFA would
// secure the web UI and leave every Paperless-compatible client -- and anything
// that can reach /api/token -- as an unguarded way in. Under encryption at rest
// the stakes are higher still: a password accepted here is also, through
// enrollment, a key that unwraps the archive.
//
// Refusing is the honest answer rather than a silent downgrade. There is no
// second factor to collect over this API, and it is better for a client to stop
// working visibly than for the operator to believe MFA covers the instance.
func checkCollectionAuthPolicy(collection *core.Collection) error {
	if !collection.PasswordAuth.Enabled {
		return errors.New("password authentication is disabled")
	}
	if collection.MFA.Enabled {
		return errors.New("multi-factor authentication is required, which this API cannot collect")
	}
	return nil
}

func findUserByField(app core.App, collection *core.Collection, field, value string) (*core.Record, error) {
	record := &core.Record{}
	err := app.RecordQuery(collection).
		AndWhere(dbx.HashExp{field: value}).
		Limit(1).
		One(record)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func bindAuth(handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := checkAPIVersion(e); err != nil {
			return err
		}
		if err := requireAuth(e); err != nil {
			return err
		}
		return handler(e)
	}
}

func normalizePaperlessAuthHeader(e *core.RequestEvent) error {
	header := e.Request.Header.Get("Authorization")
	if len(header) > 6 && strings.EqualFold(header[:6], "Token ") {
		e.Request.Header.Set("Authorization", strings.TrimSpace(header[6:]))
	}
	return e.Next()
}
