package mail

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// CronExprFromEnv returns the mail sync cron expression.
func CronExprFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MAIL_CRON_EXPR")); v != "" {
		return v
	}
	return "0 */6 * * *"
}

// RegisterCron registers the periodic mail sync cron job.
func RegisterCron(app core.App, svc *Service) {
	expr := CronExprFromEnv()
	app.Cron().MustAdd("mail_sync", expr, func() {
		if err := RunCronSync(app, svc); err != nil {
			app.Logger().Error("mail cron error", slog.Any("error", err))
		}
	})
	app.Logger().Info("mail sync cron registered", "cron", expr)
}

// RunCronSync enqueues scans for enabled accounts with cron_enabled.
func RunCronSync(app core.App, svc *Service) error {
	if !GoogleOAuthConfigured(app) {
		return nil
	}

	accounts, err := app.FindRecordsByFilter(
		"mail_accounts",
		"enabled = true && cron_enabled = true",
		"-created",
		500,
		0,
		nil,
	)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, account := range accounts {
		lookback := int(account.GetFloat("cron_lookback_days"))
		if lookback <= 0 {
			lookback = 7
		}
		dateFrom := now.AddDate(0, 0, -lookback).Format("2006-01-02")
		if ts := account.GetDateTime("last_synced_at"); !ts.IsZero() {
			// Re-scan from last sync day (inclusive) to catch late arrivals.
			dateFrom = ts.Time().UTC().Format("2006-01-02")
		}
		dateTo := now.Format("2006-01-02")
		mode := account.GetString("triage_mode")
		if mode != ModeDeep {
			mode = ModeSimple
		}

		scan, err := CreateScan(app, account.GetString("user"), account.Id, TriggerCron, mode, dateFrom, dateTo)
		if err != nil {
			app.Logger().Warn("mail cron create scan", "account", account.Id, slog.Any("error", err))
			continue
		}
		svc.StartScanAsync(scan.Id)
	}
	return nil
}

// CreateScan inserts a pending mail_scans record.
func CreateScan(app core.App, userID, accountID, trigger, mode, dateFrom, dateTo string) (*core.Record, error) {
	if _, err := BuildDateQuery(dateFrom, dateTo); err != nil {
		return nil, err
	}
	if mode != ModeDeep {
		mode = ModeSimple
	}
	if trigger != TriggerCron {
		trigger = TriggerManual
	}

	collection, err := app.FindCollectionByNameOrId("mail_scans")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("account", accountID)
	record.Set("trigger", trigger)
	record.Set("mode", mode)
	record.Set("date_from", dateFrom)
	record.Set("date_to", dateTo)
	record.Set("status", ScanPending)
	record.Set("progress_json", map[string]any{})
	if err := app.Save(record); err != nil {
		return nil, fmt.Errorf("create scan: %w", err)
	}
	return record, nil
}