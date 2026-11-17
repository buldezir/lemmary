package remoteimport

import (
	"errors"

	"lemmary/backend/internal/i18n"
)

// userFacing drops the request and dial context around a refusal, which is all
// the user needs to read.
func userFacing(err error) error {
	if e, ok := errors.AsType[*i18n.Error](err); ok {
		return e
	}
	return err
}
