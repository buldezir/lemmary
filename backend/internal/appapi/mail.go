package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/mail"
)

type mailAPI struct {
	app core.App
	svc *mail.Service
}

func registerMailRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App, rt *config.Runtime) {
	svc := mail.NewService(app, rt)
	mail.RegisterCron(app, svc)
	api := &mailAPI{app: app, svc: svc}

	g.GET("/mail/status", bindAuth(api.handleStatus))
	g.GET("/mail/accounts", bindAuth(api.handleListAccounts))
	g.POST("/mail/accounts", bindAuth(api.handleCreateAccount))
	g.PATCH("/mail/accounts/{id}", bindAuth(api.handlePatchAccount))
	g.DELETE("/mail/accounts/{id}", bindAuth(api.handleDeleteAccount))
	g.POST("/mail/accounts/{id}/scans", bindAuth(api.handleCreateScan))
	g.GET("/mail/scans", bindAuth(api.handleListScans))
	g.GET("/mail/scans/{id}", bindAuth(api.handleGetScan))
}

func (a *mailAPI) handleStatus(e *core.RequestEvent) error {
	return writeJSON(e, http.StatusOK, map[string]any{
		"google_oauth_configured": mail.GoogleOAuthConfigured(a.app),
	})
}

type createAccountRequest struct {
	RefreshToken string `json:"refresh_token"`
	Email        string `json:"email"`
}

func (a *mailAPI) handleCreateAccount(e *core.RequestEvent) error {
	var req createAccountRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	token := strings.TrimSpace(req.RefreshToken)
	if email == "" || token == "" {
		return writeError(e, http.StatusBadRequest, "email and refresh_token are required.")
	}
	authEmail := strings.ToLower(strings.TrimSpace(e.Auth.GetString("email")))
	if authEmail != "" && email != authEmail {
		return writeError(e, http.StatusBadRequest, "Gmail address must match the signed-in user email.")
	}
	if !mail.GoogleOAuthConfigured(a.app) {
		return writeError(e, http.StatusBadRequest, "Google OAuth2 is not configured on the users collection.")
	}

	existing, err := a.app.FindRecordsByFilter(
		"mail_accounts",
		"user = {:user} && email = {:email}",
		"",
		1,
		0,
		map[string]any{"user": e.Auth.Id, "email": email},
	)
	if err != nil {
		return writeError(e, http.StatusInternalServerError, err.Error())
	}

	var record *core.Record
	if len(existing) > 0 {
		record = existing[0]
		record.Set("refresh_token", token)
		record.Set("enabled", true)
	} else {
		collection, err := a.app.FindCollectionByNameOrId("mail_accounts")
		if err != nil {
			return writeError(e, http.StatusInternalServerError, err.Error())
		}
		record = core.NewRecord(collection)
		record.Set("user", e.Auth.Id)
		record.Set("email", email)
		record.Set("refresh_token", token)
		record.Set("enabled", true)
		record.Set("cron_enabled", false)
		record.Set("cron_lookback_days", 7)
		record.Set("triage_mode", mail.ModeSimple)
	}

	if err := a.app.Save(record); err != nil {
		return writeError(e, http.StatusBadRequest, err.Error())
	}
	return writeJSON(e, http.StatusOK, mail.AccountFromRecord(record))
}

func (a *mailAPI) handleListAccounts(e *core.RequestEvent) error {
	records, err := a.app.FindRecordsByFilter(
		"mail_accounts",
		"user = {:user}",
		"-created",
		100,
		0,
		map[string]any{"user": e.Auth.Id},
	)
	if err != nil {
		return writeError(e, http.StatusInternalServerError, err.Error())
	}
	out := make([]mail.AccountDTO, 0, len(records))
	for _, r := range records {
		out = append(out, mail.AccountFromRecord(r))
	}
	return writeJSON(e, http.StatusOK, map[string]any{"items": out})
}

type patchAccountRequest struct {
	Enabled          *bool   `json:"enabled"`
	CronEnabled      *bool   `json:"cron_enabled"`
	CronLookbackDays *int    `json:"cron_lookback_days"`
	TriageMode       *string `json:"triage_mode"`
}

func (a *mailAPI) findOwnedAccount(userID, id string) (*core.Record, error) {
	record, err := a.app.FindRecordById("mail_accounts", id)
	if err != nil {
		return nil, err
	}
	if record.GetString("user") != userID {
		return nil, errAccountNotFound
	}
	return record, nil
}

var errAccountNotFound = errors.New("account not found")

func (a *mailAPI) handlePatchAccount(e *core.RequestEvent) error {
	record, err := a.findOwnedAccount(e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return writeError(e, http.StatusNotFound, "Account not found.")
	}
	var req patchAccountRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	if req.Enabled != nil {
		record.Set("enabled", *req.Enabled)
	}
	if req.CronEnabled != nil {
		record.Set("cron_enabled", *req.CronEnabled)
	}
	if req.CronLookbackDays != nil {
		days := *req.CronLookbackDays
		if days < 1 {
			days = 1
		}
		if days > 90 {
			days = 90
		}
		record.Set("cron_lookback_days", days)
	}
	if req.TriageMode != nil {
		mode := strings.TrimSpace(*req.TriageMode)
		if mode != mail.ModeDeep {
			mode = mail.ModeSimple
		}
		record.Set("triage_mode", mode)
	}
	if err := a.app.Save(record); err != nil {
		return writeError(e, http.StatusBadRequest, err.Error())
	}
	return writeJSON(e, http.StatusOK, mail.AccountFromRecord(record))
}

func (a *mailAPI) handleDeleteAccount(e *core.RequestEvent) error {
	record, err := a.findOwnedAccount(e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return writeError(e, http.StatusNotFound, "Account not found.")
	}
	if err := a.app.Delete(record); err != nil {
		return writeError(e, http.StatusInternalServerError, err.Error())
	}
	e.Response.WriteHeader(http.StatusNoContent)
	return nil
}

type createScanRequest struct {
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
	Mode     string `json:"mode"`
}

func (a *mailAPI) handleCreateScan(e *core.RequestEvent) error {
	account, err := a.findOwnedAccount(e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return writeError(e, http.StatusNotFound, "Account not found.")
	}
	if !account.GetBool("enabled") {
		return writeError(e, http.StatusBadRequest, "Mail account is disabled.")
	}
	var req createScanRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	dateFrom := strings.TrimSpace(req.DateFrom)
	dateTo := strings.TrimSpace(req.DateTo)
	if dateFrom == "" || dateTo == "" {
		return writeError(e, http.StatusBadRequest, "date_from and date_to are required (YYYY-MM-DD).")
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = account.GetString("triage_mode")
	}
	scan, err := mail.CreateScan(a.app, e.Auth.Id, account.Id, mail.TriggerManual, mode, dateFrom, dateTo)
	if err != nil {
		return writeError(e, http.StatusBadRequest, err.Error())
	}
	a.svc.StartScanAsync(scan.Id)
	return writeJSON(e, http.StatusAccepted, mail.ScanFromRecord(scan))
}

func (a *mailAPI) handleListScans(e *core.RequestEvent) error {
	records, err := a.app.FindRecordsByFilter(
		"mail_scans",
		"user = {:user}",
		"-created",
		50,
		0,
		map[string]any{"user": e.Auth.Id},
	)
	if err != nil {
		return writeError(e, http.StatusInternalServerError, err.Error())
	}
	out := make([]mail.ScanDTO, 0, len(records))
	for _, r := range records {
		out = append(out, mail.ScanFromRecord(r))
	}
	return writeJSON(e, http.StatusOK, map[string]any{"items": out})
}

func (a *mailAPI) handleGetScan(e *core.RequestEvent) error {
	record, err := a.app.FindRecordById("mail_scans", e.Request.PathValue("id"))
	if err != nil || record.GetString("user") != e.Auth.Id {
		return writeError(e, http.StatusNotFound, "Scan not found.")
	}
	return writeJSON(e, http.StatusOK, mail.ScanFromRecord(record))
}
