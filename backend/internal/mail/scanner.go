package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"paperless-go/backend/internal/config"
)

// Progress tracks scan counters.
type Progress struct {
	Listed     int `json:"listed"`
	Fetched    int `json:"fetched"`
	Triaged    int `json:"triaged"`
	Imported   int `json:"imported"`
	Skipped    int `json:"skipped"`
	Duplicates int `json:"duplicates"`
	Errors     int `json:"errors"`
}

// Service runs mail scans.
type Service struct {
	app core.App
	rt  *config.Runtime

	scanMu sync.Mutex
}

func NewService(app core.App, rt *config.Runtime) *Service {
	return &Service{app: app, rt: rt}
}

// StartScanAsync marks the scan running and processes it in a background goroutine.
func (s *Service) StartScanAsync(scanID string) {
	go func() {
		if err := s.RunScan(scanID); err != nil {
			s.app.Logger().Error("mail scan failed", "scan_id", scanID, slog.Any("error", err))
		}
	}()
}

// RunScan executes one mail_scans record end-to-end.
func (s *Service) RunScan(scanID string) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	scan, err := s.app.FindRecordById("mail_scans", scanID)
	if err != nil {
		return err
	}
	account, err := s.app.FindRecordById("mail_accounts", scan.GetString("account"))
	if err != nil {
		return failScan(s.app, scan, fmt.Errorf("account: %w", err))
	}
	if !account.GetBool("enabled") {
		return failScan(s.app, scan, fmt.Errorf("mail account is disabled"))
	}

	mode := scan.GetString("mode")
	if mode != ModeDeep {
		mode = ModeSimple
	}
	dateFrom := scan.GetString("date_from")
	dateTo := scan.GetString("date_to")
	query, err := BuildDateQuery(dateFrom, dateTo)
	if err != nil {
		return failScan(s.app, scan, err)
	}

	scan.Set("status", ScanRunning)
	scan.Set("error", "")
	_ = saveProgress(s.app, scan, Progress{})

	client, err := defaultClientFactory(s.app, account.GetString("refresh_token"))
	if err != nil {
		return failScan(s.app, scan, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ids, err := client.ListMessageIDs(ctx, query, defaultMaxMessages)
	if err != nil {
		return failScan(s.app, scan, err)
	}

	progress := Progress{Listed: len(ids)}
	_ = saveProgress(s.app, scan, progress)

	snap := s.rt.Snapshot()
	triager := NewAITriager(snap.Cfg.OpenAIAPIKey, snap.Cfg.OpenAIModel, snap.Cfg.OpenAIBaseURL)

	batchSize := defaultSimpleBatch
	if mode == ModeDeep {
		batchSize = defaultDeepBatch
	}

	userID := scan.GetString("user")
	accountID := account.Id

	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batchIDs := ids[i:end]

		messages := make([]*Message, 0, len(batchIDs))
		for _, id := range batchIDs {
			msg, err := client.GetMessage(ctx, id, mode)
			if err != nil {
				progress.Errors++
				s.app.Logger().Warn("mail get message", "id", id, slog.Any("error", err))
				continue
			}
			if len(msg.Attachments) == 0 {
				progress.Skipped++
				continue
			}
			messages = append(messages, msg)
			progress.Fetched++
		}
		_ = saveProgress(s.app, scan, progress)

		if len(messages) == 0 {
			continue
		}

		decisions, err := triager.Triage(ctx, mode, messages)
		if err != nil {
			return failScan(s.app, scan, err)
		}
		progress.Triaged += len(messages)

		byID := map[string]*Message{}
		for _, m := range messages {
			byID[m.ID] = m
		}

		for _, d := range decisions {
			msg := byID[d.MessageID]
			if msg == nil {
				continue
			}
			wanted := map[string]struct{}{}
			for _, name := range d.Filenames {
				wanted[strings.ToLower(name)] = struct{}{}
			}
			for _, att := range msg.Attachments {
				if _, ok := wanted[strings.ToLower(att.Filename)]; !ok {
					continue
				}
				if !IsAllowedImportFilename(att.Filename) {
					progress.Skipped++
					continue
				}
				dup, err := AlreadyImported(s.app, accountID, msg.ID, att.ID)
				if err != nil {
					progress.Errors++
					continue
				}
				if dup {
					progress.Duplicates++
					continue
				}
				data, err := client.DownloadAttachment(ctx, msg.ID, att.ID)
				if err != nil {
					progress.Errors++
					s.app.Logger().Warn("mail download", "message", msg.ID, "file", att.Filename, slog.Any("error", err))
					continue
				}
				doc, err := ImportAttachment(s.app, userID, att.Filename, data)
				if err != nil {
					progress.Errors++
					s.app.Logger().Warn("mail import", "file", att.Filename, slog.Any("error", err))
					continue
				}
				if err := RecordImport(s.app, userID, accountID, msg.ID, att.ID, att.Filename, doc.Id); err != nil {
					progress.Errors++
					continue
				}
				progress.Imported++
			}
		}
		_ = saveProgress(s.app, scan, progress)
	}

	now, _ := types.ParseDateTime(time.Now().UTC())
	account.Set("last_synced_at", now)
	_ = s.app.Save(account)

	scan.Set("status", ScanCompleted)
	scan.Set("error", "")
	return saveProgress(s.app, scan, progress)
}

func failScan(app core.App, scan *core.Record, err error) error {
	scan.Set("status", ScanFailed)
	scan.Set("error", err.Error())
	_ = app.Save(scan)
	return err
}

func saveProgress(app core.App, scan *core.Record, p Progress) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return err
	}
	scan.Set("progress_json", asMap)
	return app.Save(scan)
}

// AccountDTO is a safe API representation of a mail account.
type AccountDTO struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	Enabled          bool   `json:"enabled"`
	CronEnabled      bool   `json:"cron_enabled"`
	CronLookbackDays int    `json:"cron_lookback_days"`
	TriageMode       string `json:"triage_mode"`
	LastSyncedAt     string `json:"last_synced_at,omitempty"`
	Created          string `json:"created"`
	Updated          string `json:"updated"`
}

func AccountFromRecord(r *core.Record) AccountDTO {
	lookback := int(r.GetFloat("cron_lookback_days"))
	if lookback <= 0 {
		lookback = 7
	}
	dto := AccountDTO{
		ID:               r.Id,
		Email:            r.GetString("email"),
		Enabled:          r.GetBool("enabled"),
		CronEnabled:      r.GetBool("cron_enabled"),
		CronLookbackDays: lookback,
		TriageMode:       r.GetString("triage_mode"),
		Created:          r.GetString("created"),
		Updated:          r.GetString("updated"),
	}
	if ts := r.GetDateTime("last_synced_at"); !ts.IsZero() {
		dto.LastSyncedAt = ts.Time().UTC().Format(time.RFC3339)
	}
	return dto
}

// ScanDTO is a safe API representation of a scan job.
type ScanDTO struct {
	ID        string   `json:"id"`
	AccountID string   `json:"account_id"`
	Trigger   string   `json:"trigger"`
	Mode      string   `json:"mode"`
	DateFrom  string   `json:"date_from"`
	DateTo    string   `json:"date_to"`
	Status    string   `json:"status"`
	Progress  Progress `json:"progress"`
	Error     string   `json:"error,omitempty"`
	Created   string   `json:"created"`
	Updated   string   `json:"updated"`
}

func ScanFromRecord(r *core.Record) ScanDTO {
	dto := ScanDTO{
		ID:        r.Id,
		AccountID: r.GetString("account"),
		Trigger:   r.GetString("trigger"),
		Mode:      r.GetString("mode"),
		DateFrom:  r.GetString("date_from"),
		DateTo:    r.GetString("date_to"),
		Status:    r.GetString("status"),
		Error:     r.GetString("error"),
		Created:   r.GetString("created"),
		Updated:   r.GetString("updated"),
	}
	if raw := r.Get("progress_json"); raw != nil {
		b, err := json.Marshal(raw)
		if err == nil {
			_ = json.Unmarshal(b, &dto.Progress)
		}
	}
	return dto
}
