package appapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/strutil"
)

type settingsResponse struct {
	OCRProviderID string `json:"ocr_provider_id"`
	OCRModel      string `json:"ocr_model"`
	// The general model: everything a language model does here but the Deep
	// Research reasoning loop.
	ExtractProviderID string `json:"extract_provider_id"`
	ExtractModel      string `json:"extract_model"`
	// Empty means Deep Research runs on the general model.
	ResearchProviderID  string `json:"research_provider_id"`
	ResearchModel       string `json:"research_model"`
	EmbeddingProviderID string `json:"embedding_provider_id"`
	EmbeddingModel      string `json:"embedding_model"`
	// Empty means no web call is served: Ask AI offers no web tools at all, and
	// research declares the schemas but refuses every call. No model: a
	// web-search API takes none.
	WebSearchProviderID string `json:"websearch_provider_id"`
	// EmbeddingDims is read-only, recorded from the first real response: a
	// number that disagreed with the model would build an index that silently
	// drops every vector.
	EmbeddingDims                 int     `json:"embedding_dims"`
	OCRTimeoutSec                 int     `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      string  `json:"processing_result_language"`
	DeepSearchLanguages           string  `json:"deep_search_languages"`
	OpenAITimeoutSec              int     `json:"openai_timeout_sec"`
	WorkerTimeoutSec              int     `json:"worker_timeout_sec"`
	WorkerMaxRetries              int     `json:"worker_max_retries"`
	ExtractionPromptVersion       string  `json:"extraction_prompt_version"`
	ExtractionRules               string  `json:"extraction_rules"`
	NearDuplicateDetectionEnabled bool    `json:"near_duplicate_detection_enabled"`
	NearDuplicateThreshold        float64 `json:"near_duplicate_threshold"`
	AlwaysRequireReview           bool    `json:"always_require_review"`
	// The consume folder. Owner empty means the first admin's paired account.
	IngestDirOwner          string `json:"ingest_dir_owner"`
	IngestDirIntervalMin    int    `json:"ingest_dir_interval_min"`
	IngestDirDeleteOriginal bool   `json:"ingest_dir_delete_original"`
	// The IMAP mailbox, sharing the owner and interval above. The password is
	// write-only: a GET reports only whether one is stored.
	IMAPHost         string   `json:"imap_host"`
	IMAPSecurity     string   `json:"imap_security"`
	IMAPUsername     string   `json:"imap_username"`
	IMAPPasswordSet  bool     `json:"imap_password_set"`
	IMAPFolder       string   `json:"imap_folder"`
	IMAPAfterConsume string   `json:"imap_after_consume"`
	IMAPMoveFolder   string   `json:"imap_move_folder"`
	IMAPSkipTypes    []string `json:"imap_skip_types"`
	// Read-only: set whenever the mailbox changes; older mail is not scanned.
	IMAPSince string `json:"imap_since"`
	// Branding lives in PocketBase's own settings, not the app_settings record:
	// the name is what passkeys, emails and backups are stamped with.
	AppName string `json:"app_name"`
	Accent  string `json:"accent"`
}

type settingsPatchRequest struct {
	OCRProviderID                 *string   `json:"ocr_provider_id"`
	OCRModel                      *string   `json:"ocr_model"`
	ExtractProviderID             *string   `json:"extract_provider_id"`
	ExtractModel                  *string   `json:"extract_model"`
	ResearchProviderID            *string   `json:"research_provider_id"`
	ResearchModel                 *string   `json:"research_model"`
	EmbeddingProviderID           *string   `json:"embedding_provider_id"`
	EmbeddingModel                *string   `json:"embedding_model"`
	WebSearchProviderID           *string   `json:"websearch_provider_id"`
	OCRTimeoutSec                 *int      `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      *string   `json:"processing_result_language"`
	DeepSearchLanguages           *string   `json:"deep_search_languages"`
	OpenAITimeoutSec              *int      `json:"openai_timeout_sec"`
	WorkerTimeoutSec              *int      `json:"worker_timeout_sec"`
	WorkerMaxRetries              *int      `json:"worker_max_retries"`
	ExtractionPromptVersion       *string   `json:"extraction_prompt_version"`
	ExtractionRules               *string   `json:"extraction_rules"`
	NearDuplicateDetectionEnabled *bool     `json:"near_duplicate_detection_enabled"`
	NearDuplicateThreshold        *float64  `json:"near_duplicate_threshold"`
	AlwaysRequireReview           *bool     `json:"always_require_review"`
	IngestDirOwner                *string   `json:"ingest_dir_owner"`
	IngestDirIntervalMin          *int      `json:"ingest_dir_interval_min"`
	IngestDirDeleteOriginal       *bool     `json:"ingest_dir_delete_original"`
	IMAPHost                      *string   `json:"imap_host"`
	IMAPSecurity                  *string   `json:"imap_security"`
	IMAPUsername                  *string   `json:"imap_username"`
	IMAPPassword                  *string   `json:"imap_password"`
	IMAPFolder                    *string   `json:"imap_folder"`
	IMAPAfterConsume              *string   `json:"imap_after_consume"`
	IMAPMoveFolder                *string   `json:"imap_move_folder"`
	IMAPSkipTypes                 *[]string `json:"imap_skip_types"`
	AppName                       *string   `json:"app_name"`
	Accent                        *string   `json:"accent"`
}

// touchesManaged is true for the same fields ApplyManaged rewrites. Timeouts,
// retries, languages, the prompt version and always_require_review are
// tenant-owned; see AIEnv.
func (r settingsPatchRequest) touchesManaged() bool {
	return r.OCRProviderID != nil ||
		r.OCRModel != nil ||
		r.ExtractProviderID != nil ||
		r.ExtractModel != nil ||
		r.ResearchProviderID != nil ||
		r.ResearchModel != nil ||
		r.EmbeddingProviderID != nil ||
		r.EmbeddingModel != nil ||
		r.WebSearchProviderID != nil ||
		r.NearDuplicateDetectionEnabled != nil ||
		r.NearDuplicateThreshold != nil
}

func handleGetSettings(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := config.EnsureDefaults(app, rt.Env()); err != nil {
			app.Logger().Warn("ensure settings before GET failed", "error", err)
		}
		// No reload here: the runtime is rebuilt by the app_settings/ai_providers
		// record hooks, so reads stay cheap and quiet.
		return writeJSON(e, http.StatusOK, settingsResponseFor(app, rt.Snapshot().Cfg))
	}
}

func handlePatchSettings(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req settingsPatchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if rt.Managed() && req.touchesManaged() {
			return writeError(e, http.StatusForbidden, managedMessage)
		}
		// Checked before the record is touched, so a name PocketBase would reject
		// reads as a validation error rather than a failed save.
		appName, accent, err := brandingPatch(req)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		// Load + patch + save in one transaction: settings is a singleton saved
		// whole, so two concurrent PATCHes would silently revert each other.
		// Branding is saved in it too, so a failure there rolls the rest back.
		var patchErr, brandingErr error
		err = app.RunInTransaction(func(txApp core.App) error {
			record, err := config.FindSettingsRecord(txApp, rt.Env())
			if err != nil {
				return err
			}
			if err := applySettingsPatch(txApp, record, req); err != nil {
				patchErr = err
				return err
			}
			if err := txApp.Save(record); err != nil {
				return err
			}
			brandingErr = saveBranding(txApp, appName, accent)
			return brandingErr
		})
		switch {
		case patchErr != nil:
			return writeError(e, http.StatusBadRequest, patchErr.Error())
		case brandingErr != nil:
			app.Logger().Error("save branding failed", slog.Any("error", brandingErr))
			return writeError(e, http.StatusBadRequest, "Failed to save the application name or accent color.")
		case err != nil:
			app.Logger().Error("save settings failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to save settings.")
		}

		// The saves above fire their after-success hooks on commit: those reload
		// the runtime and PocketBase's own settings.
		return writeJSON(e, http.StatusOK, settingsResponseFor(app, rt.Snapshot().Cfg))
	}
}

// handleGetEmbeddingStats is separate from GET /settings because it scans two
// tables, and every visit to the Settings page would otherwise pay for it.
func handleGetEmbeddingStats(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		stats, err := loadEmbeddingStats(app, rt.Snapshot().Cfg)
		if err != nil {
			app.Logger().Error("embedding stats failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to load embedding statistics.")
		}
		return writeJSON(e, http.StatusOK, stats)
	}
}

// A nil pointer back means "not in this request", so the stored value is kept.
func brandingPatch(req settingsPatchRequest) (appName *string, accent *string, err error) {
	if req.AppName != nil {
		name := strings.TrimSpace(*req.AppName)
		// PocketBase requires a name, so an empty one would fail the whole
		// save with a validation error nobody can read.
		if name == "" || len([]rune(name)) > 255 {
			return nil, nil, errInvalid("app_name must be 1-255 characters")
		}
		appName = &name
	}
	if req.Accent != nil {
		// Empty clears it and falls back to the built-in accent; anything else
		// is #rrggbb, which is all PocketBase stores.
		value := strings.TrimSpace(*req.Accent)
		if value != "" && !hexColor.MatchString(value) {
			return nil, nil, errInvalid("accent must be a hex color like #6e2620")
		}
		accent = &value
	}
	return appName, accent, nil
}

// saveBranding saves a clone: the live settings must not change before the
// transaction commits, and PocketBase reloads them from the row once it does.
func saveBranding(app core.App, appName, accent *string) error {
	if appName == nil && accent == nil {
		return nil
	}
	settings, err := app.Settings().Clone()
	if err != nil {
		return err
	}
	if appName != nil {
		settings.Meta.AppName = *appName
	}
	if accent != nil {
		settings.Meta.AccentColor = *accent
	}
	return app.Save(settings)
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxExtractionRules matches the field's Max in the migration that added it.
const maxExtractionRules = 4000

func settingsResponseFor(app core.App, cfg config.Config) settingsResponse {
	res := settingsResponseFromConfig(cfg)
	res.AppName = resolvedAppName(app)
	res.Accent = resolvedAccent(app)
	return res
}

func settingsResponseFromConfig(cfg config.Config) settingsResponse {
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = config.DefaultNearDuplicateThreshold
	}
	return settingsResponse{
		OCRProviderID:                 cfg.OCRProviderID,
		OCRModel:                      cfg.OCRModel,
		ExtractProviderID:             cfg.ExtractProviderID,
		ExtractModel:                  cfg.ExtractModel,
		ResearchProviderID:            cfg.ResearchProviderID,
		ResearchModel:                 cfg.ResearchModel,
		EmbeddingProviderID:           cfg.EmbeddingProviderID,
		EmbeddingModel:                cfg.EmbeddingModel,
		EmbeddingDims:                 cfg.EmbeddingDims,
		WebSearchProviderID:           cfg.WebSearchProviderID,
		OCRTimeoutSec:                 int(cfg.OCRTimeout.Seconds()),
		ProcessingResultLanguage:      cfg.ProcessingResultLanguage,
		DeepSearchLanguages:           cfg.DeepSearchLanguages,
		OpenAITimeoutSec:              int(cfg.OpenAITimeout.Seconds()),
		WorkerTimeoutSec:              int(cfg.WorkerTimeout.Seconds()),
		WorkerMaxRetries:              cfg.WorkerMaxRetries,
		ExtractionPromptVersion:       cfg.ExtractionPromptVer,
		ExtractionRules:               cfg.ExtractionRules,
		NearDuplicateDetectionEnabled: cfg.NearDuplicateDetectionEnabled,
		NearDuplicateThreshold:        threshold,
		AlwaysRequireReview:           cfg.AlwaysRequireReview,
		IngestDirOwner:                cfg.IngestDirOwner,
		IngestDirIntervalMin:          cfg.IngestDirIntervalMin,
		IngestDirDeleteOriginal:       cfg.IngestDirDeleteOriginal,
		IMAPHost:                      cfg.IMAPHost,
		IMAPSecurity:                  cfg.IMAPSecurity,
		IMAPUsername:                  cfg.IMAPUsername,
		IMAPPasswordSet:               cfg.IMAPPassword != "",
		IMAPFolder:                    cfg.IMAPFolder,
		IMAPAfterConsume:              cfg.IMAPAfterConsume,
		IMAPMoveFolder:                cfg.IMAPMoveFolder,
		IMAPSkipTypes:                 append([]string{}, cfg.IMAPSkipTypes...),
		IMAPSince:                     formatSince(cfg.IMAPSince),
	}
}

func applySettingsPatch(app core.App, record *core.Record, req settingsPatchRequest) error {
	// Read before the write so a change can be detected: switching model or
	// endpoint invalidates the recorded vector length and every stored vector.
	embeddingBefore := embeddingBinding(record)
	if err := applyBindingsPatch(app, record, req); err != nil {
		return err
	}
	if err := applyProcessingPatch(record, req); err != nil {
		return err
	}
	if err := applyIngestDirPatch(app, record, req); err != nil {
		return err
	}
	if err := applyIMAPPatch(record, req); err != nil {
		return err
	}
	if req.NearDuplicateThreshold != nil {
		if *req.NearDuplicateThreshold <= 0 || *req.NearDuplicateThreshold > 1 {
			return errInvalid("near_duplicate_threshold must be between 0 and 1")
		}
		record.Set("near_duplicate_threshold", *req.NearDuplicateThreshold)
	}
	if err := validateBindings(app, record); err != nil {
		return err
	}
	if embeddingBinding(record) != embeddingBefore {
		record.Set("embedding_dims", 0)
	}
	return nil
}

func embeddingBinding(record *core.Record) string {
	return strings.TrimSpace(record.GetString("embedding_provider_id")) + "|" +
		strings.TrimSpace(record.GetString("embedding_model"))
}

func applyBindingsPatch(app core.App, record *core.Record, req settingsPatchRequest) error {
	if err := patchSettingsProviderID(app, record, "ocr_provider_id", req.OCRProviderID, needOCR); err != nil {
		return err
	}
	patchSettingsText(record, "ocr_model", req.OCRModel)
	if err := patchSettingsProviderID(app, record, "extract_provider_id", req.ExtractProviderID, needLLM); err != nil {
		return err
	}
	patchSettingsText(record, "extract_model", req.ExtractModel)
	if err := patchSettingsProviderID(app, record, "research_provider_id", req.ResearchProviderID, needLLM); err != nil {
		return err
	}
	patchSettingsText(record, "research_model", req.ResearchModel)
	if err := patchSettingsProviderID(app, record, "embedding_provider_id", req.EmbeddingProviderID, needEmbedding); err != nil {
		return err
	}
	patchSettingsText(record, "embedding_model", req.EmbeddingModel)
	return patchSettingsProviderID(app, record, "websearch_provider_id", req.WebSearchProviderID, needWebSearch)
}

func applyProcessingPatch(record *core.Record, req settingsPatchRequest) error {
	if err := patchSettingsPositive(record, "ocr_timeout_sec", req.OCRTimeoutSec); err != nil {
		return err
	}
	if req.ProcessingResultLanguage != nil {
		record.Set("processing_result_language", strings.ToLower(strings.TrimSpace(*req.ProcessingResultLanguage)))
	}
	if req.DeepSearchLanguages != nil {
		record.Set("deep_search_languages", config.NormalizeLanguageList(*req.DeepSearchLanguages))
	}
	if err := patchSettingsPositive(record, "openai_timeout_sec", req.OpenAITimeoutSec); err != nil {
		return err
	}
	if err := patchSettingsPositive(record, "worker_timeout_sec", req.WorkerTimeoutSec); err != nil {
		return err
	}
	if req.WorkerMaxRetries != nil {
		if *req.WorkerMaxRetries < 0 {
			return errInvalid("worker_max_retries must be >= 0")
		}
		record.Set("worker_max_retries", *req.WorkerMaxRetries)
	}
	patchSettingsText(record, "extraction_prompt_version", req.ExtractionPromptVersion)
	if req.ExtractionRules != nil {
		rules := strings.TrimSpace(*req.ExtractionRules)
		// Checked here rather than left to the field's Max so the 400 carries a
		// message an admin can read.
		if len([]rune(rules)) > maxExtractionRules {
			return errInvalid(fmt.Sprintf("extraction_rules must be at most %d characters", maxExtractionRules))
		}
		record.Set("extraction_rules", rules)
	}
	if req.NearDuplicateDetectionEnabled != nil {
		record.Set("near_duplicate_detection_enabled", *req.NearDuplicateDetectionEnabled)
	}
	if req.AlwaysRequireReview != nil {
		record.Set("always_require_review", *req.AlwaysRequireReview)
	}
	return nil
}

func applyIngestDirPatch(app core.App, record *core.Record, req settingsPatchRequest) error {
	if req.IngestDirOwner != nil {
		id := strings.TrimSpace(*req.IngestDirOwner)
		if id != "" && app != nil {
			if _, err := app.FindRecordById("users", id); err != nil {
				return errInvalid("ingest_dir_owner is not a known user")
			}
		}
		record.Set("ingest_dir_owner", id)
	}
	if req.IngestDirIntervalMin != nil {
		if !config.ValidIngestInterval(*req.IngestDirIntervalMin) {
			return errInvalid("ingest_dir_interval_min must divide an hour (1-30 minutes) or a day (1-24 hours)")
		}
		record.Set("ingest_dir_interval_min", *req.IngestDirIntervalMin)
	}
	if req.IngestDirDeleteOriginal != nil {
		record.Set("ingest_dir_delete_original", *req.IngestDirDeleteOriginal)
	}
	return nil
}

func patchSettingsProviderID(app core.App, record *core.Record, field string, value *string, need providerNeed) error {
	if value == nil {
		return nil
	}
	id := strings.TrimSpace(*value)
	if err := validateProviderID(app, id, need); err != nil {
		return err
	}
	record.Set(field, id)
	return nil
}

func patchSettingsText(record *core.Record, field string, value *string) {
	if value != nil {
		record.Set(field, strings.TrimSpace(*value))
	}
}

func patchSettingsPositive(record *core.Record, field string, value *int) error {
	if value == nil {
		return nil
	}
	if *value <= 0 {
		return errInvalid(field + " must be positive")
	}
	record.Set(field, *value)
	return nil
}

func validateBindings(app core.App, record *core.Record) error {
	ocrID := strings.TrimSpace(record.GetString("ocr_provider_id"))
	if ocrID != "" {
		p, err := aiprovider.FindByID(app, ocrID)
		if err != nil || p == nil {
			return errInvalid("ocr_provider_id is not a valid provider")
		}
		if aiprovider.RequiresOCRModel(p.SDK) && strings.TrimSpace(record.GetString("ocr_model")) == "" {
			return errInvalid("ocr_model is required for this OCR provider")
		}
	}
	if extractID := strings.TrimSpace(record.GetString("extract_provider_id")); extractID != "" {
		if strings.TrimSpace(record.GetString("extract_model")) == "" {
			return errInvalid("extract_model is required")
		}
	}
	// Both halves or neither: half a binding would read as bound in Settings and
	// quietly run research on the general model.
	researchID := strings.TrimSpace(record.GetString("research_provider_id"))
	researchModel := strings.TrimSpace(record.GetString("research_model"))
	if researchID != "" && researchModel == "" {
		return errInvalid("research_model is required when a research provider is set")
	}
	if researchID == "" && researchModel != "" {
		return errInvalid("research_provider_id is required when a research model is set")
	}

	embeddingID := strings.TrimSpace(record.GetString("embedding_provider_id"))
	embeddingModel := strings.TrimSpace(record.GetString("embedding_model"))
	if embeddingID != "" && embeddingModel == "" {
		// Half a binding is worse than none: the feature would read as on and
		// every document would fail its embed step.
		return errInvalid("embedding_model is required when an embedding provider is set")
	}
	return nil
}

// providerNeed is what a binding requires of the provider assigned to it.
type providerNeed int

const (
	// needOCR admits google_vision and every SDK that can send a file to a
	// model, but not local, which has no way to read a document.
	needOCR providerNeed = iota
	needLLM
	needEmbedding
	needWebSearch
)

func validateProviderID(app core.App, id string, need providerNeed) error {
	if id == "" {
		return nil
	}
	p, err := aiprovider.FindByID(app, id)
	if err != nil || p == nil {
		return errInvalid("unknown provider")
	}
	return providerServes(*p, need)
}

// The three SDK lists are derived, not written out: spelled by hand the
// messages went stale twice, and an admin who believed one would not have tried
// the provider that works.
func providerServes(p aiprovider.Provider, need providerNeed) error {
	switch need {
	case needLLM:
		if !aiprovider.IsLLM(p.SDK) {
			return errInvalid("extraction and research require " + oneOf(aiprovider.LLMSDKs()) + " provider")
		}
	case needEmbedding:
		if !aiprovider.CanEmbed(p.SDK) {
			return errInvalid("embeddings require " + oneOf(aiprovider.EmbeddingSDKs()) + " provider")
		}
	case needOCR:
		if !aiprovider.CanOCR(p.SDK) {
			return errInvalid("OCR requires " + oneOf(aiprovider.OCRSDKs()) + " provider")
		}
	case needWebSearch:
		if !aiprovider.CanWebSearch(p.SDK) {
			return errInvalid("web search requires " + oneOf(aiprovider.WebSearchSDKs()) + " provider")
		}
	}
	return nil
}

// oneOf renders a list of SDK names as "an openai, openrouter, or mistral".
func oneOf(sdks []string) string {
	article := "a"
	if len(sdks) > 0 && strings.ContainsRune("aeiou", rune(sdks[0][0])) {
		article = "an"
	}
	switch len(sdks) {
	case 0:
		return "a configured"
	case 1:
		return article + " " + sdks[0]
	case 2:
		return article + " " + sdks[0] + " or " + sdks[1]
	default:
		return article + " " + strings.Join(sdks[:len(sdks)-1], ", ") + ", or " + sdks[len(sdks)-1]
	}
}

type settingsError string

func (e settingsError) Error() string { return string(e) }

func errInvalid(msg string) error { return settingsError(msg) }

func formatSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// imapMailbox is what identifies the mailbox: pointing at another one moves
// imap_since, a new password or after-import action does not.
func imapMailbox(record *core.Record) string {
	return record.GetString("imap_host") + "|" + record.GetString("imap_username") + "|" +
		strutil.FirstNonEmpty(record.GetString("imap_folder"), config.DefaultIMAPFolder)
}

func applyIMAPPatch(record *core.Record, req settingsPatchRequest) error {
	before := imapMailbox(record)
	for field, value := range map[string]*string{
		"imap_host":        req.IMAPHost,
		"imap_username":    req.IMAPUsername,
		"imap_folder":      req.IMAPFolder,
		"imap_move_folder": req.IMAPMoveFolder,
	} {
		if value != nil {
			record.Set(field, strings.TrimSpace(*value))
		}
	}
	// Blank keeps the stored password, as the provider api_key does.
	if req.IMAPPassword != nil && *req.IMAPPassword != "" {
		record.Set("imap_password", *req.IMAPPassword)
	}
	if req.IMAPSecurity != nil {
		switch v := strings.TrimSpace(*req.IMAPSecurity); v {
		case config.IMAPSecurityTLS, config.IMAPSecuritySTARTTLS:
			record.Set("imap_security", v)
		default:
			return errInvalid("imap_security must be tls or starttls")
		}
	}
	if req.IMAPAfterConsume != nil {
		switch v := strings.TrimSpace(*req.IMAPAfterConsume); v {
		case config.IMAPKeep, config.IMAPDelete, config.IMAPMove:
			record.Set("imap_after_consume", v)
		default:
			return errInvalid("imap_after_consume must be keep, delete or move")
		}
	}
	if req.IMAPSkipTypes != nil {
		skip := map[string]bool{}
		for _, v := range *req.IMAPSkipTypes {
			if _, ok := config.IMAPFileTypes[v]; !ok {
				return errInvalid("imap_skip_types must be pdf, office, image or text")
			}
			skip[v] = true
		}
		if len(skip) == len(config.IMAPFileTypes) {
			return errInvalid("imap_skip_types cannot skip every file type")
		}
		record.Set("imap_skip_types", *req.IMAPSkipTypes)
	}
	if record.GetString("imap_after_consume") == config.IMAPMove {
		folder := strutil.FirstNonEmpty(record.GetString("imap_folder"), config.DefaultIMAPFolder)
		switch target := record.GetString("imap_move_folder"); {
		case target == "":
			return errInvalid("imap_move_folder is required to move consumed messages")
		case strings.EqualFold(target, folder):
			return errInvalid("imap_move_folder must differ from imap_folder")
		}
	}
	if record.GetString("imap_host") != "" && imapMailbox(record) != before {
		record.Set("imap_since", time.Now().UTC())
	}
	return nil
}
