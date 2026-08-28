// Package limits bounds how much one instance may hold.
//
// The six allowances here are what a hosted plan is made of: how many documents
// an instance stores, how many pages those add up to, how many bytes they occupy,
// how large a single file may be, how many pages that file may have, and how many
// accounts exist besides the admin. They are read from the environment and
// nowhere else -- an orchestrator can only express a plan as the environment of
// the container it creates, and an admin editing the Settings page must not be
// able to raise their own plan. That is the same reasoning UPLOAD_MAX_MB was
// added under, and the reason none of these joins app_settings or the
// env_applied mechanism.
//
// Every limit is unlimited by default, so an install that sets nothing behaves
// exactly as it did before this package existed.
package limits

import (
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
)

// Limit is one allowance: either unset, which bounds nothing, or a number.
//
// A struct rather than a bare int64 with a magic value, because both 0 and -1
// are wanted for real things. Zero is a limit a plan genuinely sells --
// LIMIT_ADDITIONAL_USERS=0 is "a single account, no extras" -- so 0 cannot double
// as the unlimited sentinel; and a negative number is a typo, not an allowance.
// The zero Limit is the unlimited one, so the zero Limits bounds nothing and a
// test can build the unlimited case without naming six fields.
type Limit struct {
	value int64
	set   bool
}

// Of returns a Limit bounding at value.
func Of(value int64) Limit {
	return Limit{value: value, set: true}
}

// Unlimited returns the Limit that bounds nothing. Same as the zero value; named
// so a call site reads as a decision rather than an omission.
func Unlimited() Limit {
	return Limit{}
}

// IsUnlimited reports whether this limit bounds anything.
func (l Limit) IsUnlimited() bool { return !l.set }

// Value is the bound. Meaningless when IsUnlimited.
func (l Limit) Value() int64 { return l.value }

// Exceeded reports whether want would cross the limit.
func (l Limit) Exceeded(want int64) bool {
	return l.set && want > l.value
}

// Remaining is how much headroom is left given what is already used, floored at
// zero so an instance that is already over its limit reports 0 rather than a
// negative. Meaningless when IsUnlimited.
func (l Limit) Remaining(used int64) int64 {
	if !l.set {
		return 0
	}
	if used >= l.value {
		return 0
	}
	return l.value - used
}

// Limits is one instance's allowance.
type Limits struct {
	// Documents caps how many documents the instance stores.
	Documents Limit
	// DocumentPages caps the sum of every stored document's page count.
	DocumentPages Limit
	// StorageBytes caps the sum of every stored document's size.
	StorageBytes Limit
	// FileBytes caps one document's file. It can only lower the effective cap:
	// the documents.file field carries its own 20 MB MaxSize, which PocketBase
	// enforces in the field validator, so a larger value here has no effect.
	FileBytes Limit
	// FilePages caps the page count of one document.
	FilePages Limit
	// AdditionalUsers caps accounts other than the paired admin.
	AdditionalUsers Limit
}

// Any reports whether anything is bounded at all, so callers can skip work --
// and so the UI renders nothing on an install that sets no limits.
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

// EnvKeys lists every variable this package reads, in the order the docs list
// them.
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
// use alongside them.
//
// Called once at process start, like pdfsplit.MaxPDFBytes: a limit is a property
// of the container an orchestrator created, and an orchestrator changes it by
// recreating the container rather than by reaching into a running one.
//
// The second return exists because of what an unusable value falls back to. A
// limit nobody can read becomes unlimited, which is a working instance and a
// wrong plan -- the one failure mode here that is invisible from the outside. So
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
	// Built into a local first, and returned in a separate statement. Go does not
	// order a plain variable read against the function calls in the same
	// expression, so returning the struct literal and misconfigured together
	// reads the slice header before read() has finished appending to it.
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

// envLimit parses one limit. Unset means unlimited; so does a value this cannot
// use.
//
// Falling back to unlimited on a malformed value follows the convention the rest
// of this codebase reads environment under -- a typo in an orchestrator's
// environment must not take a customer's instance down, and the default is a
// working configuration. For a limit that cuts the other way from usual: a typo
// grants more room rather than less. That is still the direction to fail in,
// because the alternative is locking an owner out of their own archive over a
// stray character, and the warning is what makes it diagnosable.
//
// An explicit 0 is honoured, not treated as unset: it is how a plan says "none".
//
// The second return is false only when a value was set and could not be used --
// not for an unset variable, which is a deliberate "unlimited" rather than a
// mistake.
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
