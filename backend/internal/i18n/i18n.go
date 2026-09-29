// Package i18n translates the server's own user-facing messages into the
// language a request asks for. The catalog is keyed by the exact English text, so
// English is the message itself and a missing entry falls back to it.
package i18n

import (
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/text/language"
)

const cookieName = "lemmary_lang"

var (
	langs   = []string{"en", "de", "ru"}
	matcher = language.NewMatcher([]language.Tag{language.English, language.German, language.Russian})
)

// FromRequest returns "en", "de" or "ru": the cookie when it names one of them,
// else the best Accept-Language match, else English.
func FromRequest(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		switch c.Value {
		case "en", "de", "ru":
			return c.Value
		}
	}
	tags, _, _ := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	_, i, _ := matcher.Match(tags...)
	return langs[i]
}

// T returns msg in lang, or msg itself when the catalog has no entry.
func T(lang, msg string) string {
	if s, ok := catalog[msg][lang]; ok {
		return s
	}
	return msg
}

// Error is a user-facing error built from a catalog key: Error() is the English,
// and Of renders it in another language from the same arguments.
type Error struct {
	Format string
	Args   []any
}

func Errorf(format string, args ...any) *Error { return &Error{Format: format, Args: args} }

func (e *Error) Error() string { return fmt.Sprintf(e.Format, e.Args...) }

// Of is err as a user reads it in lang. An *Error anywhere in the chain is
// translated from its format; any other error is looked up by its text.
func Of(lang string, err error) string {
	if e, ok := errors.AsType[*Error](err); ok && lang != "en" {
		return fmt.Sprintf(T(lang, e.Format), e.Args...)
	}
	return T(lang, err.Error())
}
