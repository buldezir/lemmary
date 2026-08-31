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
	// The collection's own switch, honoured here as well as on PocketBase's
	// auth routes. Turning password auth off is how an operator moves an
	// install to OAuth or passkeys only; records keep whatever hash they had,
	// so without this check every one of those passwords stays a working
	// credential through POST /api/token and Basic auth -- a door the operator
	// believes they closed.
	if !collection.PasswordAuth.Enabled {
		return nil, errors.New("password authentication is disabled")
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
