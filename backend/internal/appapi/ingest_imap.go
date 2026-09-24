package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/imapimport"
)

type imapScanRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// scanWindow turns two inclusive YYYY-MM-DD days into the [from, to) pair
// IMAP's SINCE and BEFORE take.
func scanWindow(req imapScanRequest) (from, to time.Time, err error) {
	from, err = time.Parse(time.DateOnly, req.From)
	if err != nil {
		return from, to, errors.New("from must be a date (YYYY-MM-DD)")
	}
	last, err := time.Parse(time.DateOnly, req.To)
	if err != nil {
		return from, to, errors.New("to must be a date (YYYY-MM-DD)")
	}
	if last.Before(from) {
		return from, to, errors.New("to must not be before from")
	}
	return from, last.AddDate(0, 0, 1), nil
}

// A backfill can run far longer than a request should stay open, so the POST
// starts it and the Management page polls the GET, as the embedding sweep does.
func handleStartIMAPBackfill(rt *config.Runtime, scanner *imapimport.Scanner) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req imapScanRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid JSON body.")
		}
		from, to, err := scanWindow(req)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		switch err := scanner.StartBackfill(rt.Snapshot().Cfg, from, to); {
		case errors.Is(err, imapimport.ErrBusy):
			return writeError(e, http.StatusConflict, "A mailbox scan is already running. Try again shortly.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		return writeJSON(e, http.StatusAccepted, scanner.BackfillStatus())
	}
}

func handleGetIMAPBackfill(scanner *imapimport.Scanner) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return writeJSON(e, http.StatusOK, scanner.BackfillStatus())
	}
}
