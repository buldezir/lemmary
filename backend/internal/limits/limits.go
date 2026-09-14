// Package limits bounds how much one instance may hold.
//
// The six allowances are what a hosted plan is made of. They are read from the
// environment and nowhere else: an orchestrator can only express a plan as the
// environment of the container it creates, and an admin editing the Settings
// page must not be able to raise their own plan. Every limit is unlimited by
// default.
//
// MaxOCRPages is neither a plan nor read from the environment: it is what this
// can extract rather than what a plan sells, since the providers hand back a
// document's whole text in one string that has to fit the column. It lives here
// because Register's documents hooks are already the one place an upload is
// measured, and measuring twice would spool the same file to disk twice.
package limits

import (
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
)

// Limit is a struct rather than a bare int64 with a magic value, because both 0
// and -1 are wanted for real things: LIMIT_ADDITIONAL_USERS=0 is an allowance a
// plan genuinely sells, and a negative number is a typo. The zero Limit is the
// unlimited one, so the zero Limits bounds nothing.
type Limit struct {
	value int64
	set   bool
}

func Of(value int64) Limit {
	return Limit{value: value, set: true}
}

// Unlimited is the zero value, named so a call site reads as a decision rather
// than an omission.
func Unlimited() Limit {
	return Limit{}
}

func (l Limit) IsUnlimited() bool { return !l.set }

// Value is meaningless when IsUnlimited.
func (l Limit) Value() int64 { return l.value }

func (l Limit) Exceeded(want int64) bool {
	return l.set && want > l.value
}

// Remaining is floored at zero, so an instance already over its limit reports 0
// rather than a negative. Meaningless when IsUnlimited.
func (l Limit) Remaining(used int64) int64 {
	if !l.set {
		return 0
	}
	if used >= l.value {
		return 0
	}
	return l.value - used
}

type Limits struct {
	Documents     Limit
	DocumentPages Limit
	StorageBytes  Limit
	// FileBytes can only lower the effective cap: the documents.file field carries
	// its own 20 MB MaxSize, which PocketBase enforces in the field validator.
	FileBytes Limit
	// FilePages can only lower the effective cap: MaxOCRPages bounds every install.
	FilePages       Limit
	AdditionalUsers Limit
}

// Any reports whether anything is bounded at all, so the UI renders nothing on
// an install that sets no limits.
func (l Limits) Any() bool {
	for _, limit := range []Limit{
		l.Documents, l.DocumentPages, l.StorageBytes,
		l.FileBytes, l.FilePages, l.AdditionalUsers,
	} {
		if !limit.IsUnlimited() {
			return true
		}
	}
	return false
}

// Env var names, exported so the docs, the tests and an orchestrator's
// allowlist all read the same strings.
const (
	EnvDocuments       = "LIMIT_DOCUMENTS"
	EnvDocumentPages   = "LIMIT_DOCUMENT_PAGES"
	EnvStorageBytes    = "LIMIT_STORAGE_BYTES"
	EnvFileBytes       = "LIMIT_FILE_BYTES"
	EnvFilePages       = "LIMIT_FILE_PAGES"
	EnvAdditionalUsers = "LIMIT_ADDITIONAL_USERS"
)

func EnvKeys() []string {
	return []string{
		EnvDocuments,
		EnvDocumentPages,
		EnvStorageBytes,
		EnvFileBytes,
		EnvFilePages,
		EnvAdditionalUsers,
	}
}

// FromEnv reads the limits, and returns the names of any variables it could not
// use. Called once at process start: a limit is a property of the container an
// orchestrator created, changed by recreating it.
//
// The bad names travel with the limits because an unusable value falls back to
// unlimited, which is a working instance and a wrong plan, the one failure mode
// here invisible from the outside.
// the bad names travel with the limits, to be logged loudly and shown to an
// admin, rather than only appearing once in a boot log nobody reads.
func FromEnv(logger *slog.Logger) (Limits, []string) {
	var misconfigured []string
	read := func(key string) Limit {
		limit, ok := envLimit(logger, key)
		if !ok {
			misconfigured = append(misconfigured, key)
		}
		return limit
	}
	// Built into a local and returned separately: Go does not order a plain variable
	// read against the function calls in the same expression, so a struct literal
	// returning misconfigured too would read the slice header before read() has
	// finished appending to it.
	lim := Limits{
		Documents:       read(EnvDocuments),
		DocumentPages:   read(EnvDocumentPages),
		StorageBytes:    read(EnvStorageBytes),
		FileBytes:       read(EnvFileBytes),
		FilePages:       read(EnvFilePages),
		AdditionalUsers: read(EnvAdditionalUsers),
	}
	return lim, misconfigured
}

// envLimit falls back to unlimited on a malformed value, as the rest of this
// codebase reads environment: a typo must not take a customer's instance down.
// For a limit that fails in the generous direction, but locking an owner out of
// their own archive over a stray character is worse, and the warning makes it
// diagnosable. An explicit 0 is honoured: it is how a plan says "none".
//
// The second return is false only when a value was set and could not be used.
func envLimit(logger *slog.Logger, key string) (Limit, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return Unlimited(), true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		reject(logger, "instance limit ignored: not a whole number", key, raw)
		return Unlimited(), false
	}
	if value < 0 {
		reject(logger, "instance limit ignored: negative", key, raw)
		return Unlimited(), false
	}
	if value == math.MaxInt64 {
		// Keeps the +1 in the headroom arithmetic from overflowing.
		return Of(math.MaxInt64 - 1), true
	}
	return Of(value), true
}

// reject logs at ERROR, not WARN: the instance keeps running, so this is the
// only signal that a plan is not being enforced.
func reject(logger *slog.Logger, msg, key, raw string) {
	if logger == nil {
		return
	}
	// The value is safe to log: a limit is a number, not a credential.
	logger.Error(msg, "env", key, "value", raw, "effect", "unlimited")
}
