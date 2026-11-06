// Package apitoken issues the credentials integrations keep: an agent's MCP
// config, a Paperless-ngx app. A token is random, stored only as its hash, and
// opens the routes of its one scope. PocketBase does not take it for a session,
// so every other route sees an anonymous request.
package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	CollectionName = "api_tokens"

	ScopeMCP       = "mcp"
	ScopePaperless = "paperless"

	prefix     = "lmy_"
	maxNameLen = 100
	touchEvery = time.Hour
	scopeKey   = "apiTokenScope"
)

var ErrNotFound = errors.New("api token not found")

type Info struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	LastUsed string `json:"last_used"`
}

func ValidScope(scope string) bool {
	return scope == ScopeMCP || scope == ScopePaperless
}

// Mint returns the token itself, which is never stored and cannot be shown again.
func Mint(app core.App, userID, scope, name string) (string, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return "", err
	}
	token := prefix + rand.Text()
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > maxNameLen {
		name = string(runes[:maxNameLen])
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("scope", scope)
	record.Set("name", name)
	record.Set("hash", hash(token))
	if err := app.Save(record); err != nil {
		return "", err
	}
	return token, nil
}

// FromHeader returns the API token an Authorization header carries, under any
// scheme or none, or "" when it carries something else.
func FromHeader(header string) string {
	header = strings.TrimSpace(header)
	if i := strings.IndexByte(header, ' '); i >= 0 {
		header = strings.TrimSpace(header[i+1:])
	}
	if strings.HasPrefix(header, prefix) {
		return header
	}
	return ""
}

// Authenticate signs the request in as the owner of the token it carries, when
// that token exists and was minted for scope.
func Authenticate(e *core.RequestEvent, scope string) bool {
	token := FromHeader(e.Request.Header.Get("Authorization"))
	if token == "" {
		return false
	}
	record, err := e.App.FindFirstRecordByData(CollectionName, "hash", hash(token))
	if err != nil || record.GetString("scope") != scope {
		return false
	}
	user, err := e.App.FindRecordById("users", record.GetString("user"))
	if err != nil {
		return false
	}
	touch(e.App, record)
	e.Auth = user
	e.Set(scopeKey, scope)
	return true
}

// Scope is the scope of the token the request signed in with, "" for a session.
func Scope(e *core.RequestEvent) string {
	scope, _ := e.Get(scopeKey).(string)
	return scope
}

func List(app core.App, userID, scope string) ([]Info, error) {
	records, err := app.FindRecordsByFilter(CollectionName, "user = {:user} && scope = {:scope}", "-created", 0, 0,
		dbx.Params{"user": userID, "scope": scope})
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(records))
	for _, record := range records {
		info := Info{ID: record.Id, Name: record.GetString("name"), Created: record.GetDateTime("created").String()}
		if used := record.GetDateTime("last_used"); !used.IsZero() {
			info.LastUsed = used.String()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func Revoke(app core.App, userID, id string) error {
	record, err := app.FindRecordById(CollectionName, id)
	if err != nil || record.GetString("user") != userID {
		return ErrNotFound
	}
	return app.Delete(record)
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func touch(app core.App, record *core.Record) {
	if time.Since(record.GetDateTime("last_used").Time()) < touchEvery {
		return
	}
	record.Set("last_used", types.NowDateTime())
	if err := app.Save(record); err != nil {
		app.Logger().Warn("api token: last_used not saved", "error", err)
	}
}
