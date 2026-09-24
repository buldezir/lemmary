package config

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/strutil"
)

const (
	CollectionName = "app_settings"
	SingletonID    = "appsettings0001" // must be 15 chars (PocketBase default id rules)
)

type Config struct {
	OCRProviderID string
	OCRModel      string
	// ExtractProviderID/ExtractModel is the general model: extraction, split
	// detection, Ask AI, AI search, and Deep Research's bulk per-document reads.
	ExtractProviderID string
	ExtractModel      string

	// ResearchProviderID/ResearchModel bind the model that drives the Deep
	// Research reasoning loop: a few expensive calls where everything else is
	// many cheap ones. Unset falls back to the general binding.
	ResearchProviderID string
	ResearchModel      string

	// EmbeddingProviderID/EmbeddingModel bind the retrieval embedding model. Unset
	// means dense retrieval is off and the archive is searched by keywords alone,
	// which is a working state, not a broken one.
	EmbeddingProviderID string
	EmbeddingModel      string
	// EmbeddingDims is learned from the provider's first response rather than
	// configured. It is what sizes the vector index before a single embedding call,
	// and what makes a model switch detectable.
	EmbeddingDims int

	// WebSearchProviderID binds the provider backing the web_search and
	// web_fetch tools. Unset means no web call is served, which is the pre-flag
	// behaviour. No model: a web-search API has none.
	WebSearchProviderID string

	OCRProvider       *aiprovider.Provider
	ExtractProvider   *aiprovider.Provider
	EmbeddingProvider *aiprovider.Provider
	WebSearchProvider *aiprovider.Provider

	OCRTimeout               time.Duration
	ProcessingResultLanguage string
	DeepSearchLanguages      string
	OpenAITimeout            time.Duration
	WorkerCronExpr           string
	WorkerTimeout            time.Duration
	WorkerMaxRetries         int
	ExtractionPromptVer      string
	// ExtractionRules is appended to the built-in extraction prompt, for the house
	// conventions a fixed prompt cannot know.
	ExtractionRules               string
	NearDuplicateDetectionEnabled bool
	NearDuplicateThreshold        float64
	// AlwaysRequireReview finishes every extracted document on needs_review, so
	// nothing reaches completed except by a person saying so.
	AlwaysRequireReview bool
	// The consume folder (INGEST_DIR). Owner is a users id; empty means the
	// first admin's paired account.
	IngestDirOwner          string
	IngestDirIntervalMin    int
	IngestDirDeleteOriginal bool
	// The IMAP mailbox (INGEST_IMAP_ENABLED). It shares the owner and interval
	// above; Host empty means off.
	IMAPHost         string
	IMAPSecurity     string
	IMAPUsername     string
	IMAPPassword     string
	IMAPFolder       string
	IMAPAfterConsume string
	IMAPMoveFolder   string
	// IMAPSince is when the mailbox was last pointed somewhere new; mail
	// received before it is left to a Management backfill. Zero reads all.
	IMAPSince time.Time
}

const DefaultNearDuplicateThreshold = 0.92

const (
	DefaultIngestDirIntervalMin = 5
	MaxIngestDirIntervalMin     = 24 * 60
)

// ValidIngestInterval is what a cron step can space evenly: minutes that
// divide an hour, hours that divide a day.
func ValidIngestInterval(minutes int) bool {
	if minutes < 1 || minutes > MaxIngestDirIntervalMin {
		return false
	}
	if minutes < 60 {
		return 60%minutes == 0
	}
	return minutes%60 == 0 && 24%(minutes/60) == 0
}

// IngestCronExpr renders an interval ValidIngestInterval accepted: every N
// minutes under an hour, every N/60 hours under a day, once a day at 1440.
func IngestCronExpr(minutes int) string {
	switch {
	case minutes <= 1:
		return "* * * * *"
	case minutes < 60:
		return fmt.Sprintf("*/%d * * * *", minutes)
	case minutes < MaxIngestDirIntervalMin:
		return fmt.Sprintf("0 */%d * * *", minutes/60)
	default:
		return "0 0 * * *"
	}
}

const EnvIngestIMAP = "INGEST_IMAP_ENABLED"

func IngestIMAPEnabledFromEnv() bool {
	return getEnvBool(EnvIngestIMAP, false)
}

const (
	IMAPSecurityTLS      = "tls"
	IMAPSecuritySTARTTLS = "starttls"

	IMAPKeep   = "keep"
	IMAPDelete = "delete"
	IMAPMove   = "move"

	DefaultIMAPFolder = "INBOX"
)

func WorkerCronFromEnv() string {
	return getEnv("WORKER_CRON_EXPR", "* * * * *")
}

// DefaultWorkerConcurrency is one, so an absent flag keeps the behaviour this
// codebase has always had. There is no idle time to win back by raising it:
// drainPending already picks the next job the instant the current one ends.
// What it buys is overlap, and overlap is only free when the work is somebody
// else's HTTP server waiting, not a local OCR sidecar spending these CPUs.
//
// Two things behave differently above 1, both in .env.example: near-duplicate
// detection assumes documents are fingerprinted in creation order, and the OCR
// deadline is per call.
const DefaultWorkerConcurrency = 1

// WorkerConcurrencyFromEnv falls back to the default below 1, rather than
// stopping the worker.
func WorkerConcurrencyFromEnv() int {
	return envIntDefault("WORKER_CONCURRENCY", DefaultWorkerConcurrency, 1)
}

const DefaultStagingMaxBytes int64 = 1 << 30 // 1 GiB

// minStagingMaxBytes keeps a typo from setting a limit no upload can meet.
const minStagingMaxBytes int64 = 1 << 20 // 1 MiB

// StagingMaxBytesFromEnv is the largest archive an import may stage on disk.
// An importer discards the previous staged upload first, so one account can
// occupy roughly this much while deciding whether to confirm.
func StagingMaxBytesFromEnv() int64 {
	return envInt64Default("IMPORT_STAGING_MAX_BYTES", DefaultStagingMaxBytes, minStagingMaxBytes)
}

// The schema is owned by migrations/; this only errors clearly if they have
// not run.
func findSettingsCollection(app core.App) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil, fmt.Errorf("%s collection is missing; run migrations: %w", CollectionName, err)
	}
	return collection, nil
}

// EnsureDefaults seeds the app_settings singleton from the environment when it
// is missing. After that first boot the Settings page owns the values;
// ApplyManaged then takes back the operator-owned parts in managed mode.
func EnsureDefaults(app core.App, env AIEnv) error {
	if _, err := aiprovider.EnsureCollection(app); err != nil {
		return err
	}

	if record, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return bindProviders(app, record)
	}

	collection, err := findSettingsCollection(app)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Id = SingletonID
	record.MarkAsNew()
	applyConfigToRecord(record, env.Defaults())
	if err := aiprovider.Apply(app, record, env.Providers); err != nil {
		return err
	}
	if err := app.Save(record); err != nil {
		// A concurrent caller can seed the singleton between the find and the save, and
		// the fixed ID then collides. Treat that as success.
		if existing, findErr := app.FindRecordById(CollectionName, SingletonID); findErr == nil {
			return bindProviders(app, existing)
		}
		return fmt.Errorf("seed %s: %w", CollectionName, err)
	}
	app.Logger().Info("seeded app_settings singleton from env defaults")
	return nil
}

// ApplyManaged unconditionally rewrites the operator-owned settings (providers,
// task bindings, duplicate detection) from the environment. Timeouts and
// language settings are not here; see AIEnv.
func ApplyManaged(app core.App, env AIEnv) error {
	settings, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return fmt.Errorf("load %s: %w", CollectionName, err)
	}
	if err := aiprovider.Apply(app, settings, env.Providers); err != nil {
		return err
	}
	settings.Set("near_duplicate_detection_enabled", env.NearDuplicateEnabled)
	settings.Set("near_duplicate_threshold", env.NearDuplicateThreshold)
	if err := app.Save(settings); err != nil {
		return fmt.Errorf("save %s: %w", CollectionName, err)
	}
	// The variable names are safe to log; the values are not.
	app.Logger().Info("applied managed AI configuration from the environment",
		"llm_sdk", env.Providers.LLM.SDK, "ocr_sdk", env.Providers.OCRSDK())
	return nil
}

func bindProviders(app core.App, record *core.Record) error {
	before := record.GetString("ocr_provider_id") + "|" + record.GetString("extract_provider_id")
	if err := aiprovider.MigrateLegacySettings(app, record); err != nil {
		return err
	}
	after := record.GetString("ocr_provider_id") + "|" + record.GetString("extract_provider_id")
	if before == after {
		return nil
	}
	return app.Save(record)
}

// Load reads runtime settings from the DB singleton. WorkerCronExpr is always
// from env.
func Load(app core.App) (Config, error) {
	record, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return Config{}, fmt.Errorf("load %s: %w", CollectionName, err)
	}
	return configFromRecord(app, record)
}

func FindSettingsRecord(app core.App, env AIEnv) (*core.Record, error) {
	if err := EnsureDefaults(app, env); err != nil {
		return nil, err
	}
	return app.FindRecordById(CollectionName, SingletonID)
}

func configFromRecord(app core.App, record *core.Record) (Config, error) {
	ocrTimeoutSec := int(record.GetFloat("ocr_timeout_sec"))
	if ocrTimeoutSec <= 0 {
		ocrTimeoutSec = 40
	}
	ingestInterval := int(record.GetFloat("ingest_dir_interval_min"))
	if ingestInterval <= 0 {
		ingestInterval = DefaultIngestDirIntervalMin
	}
	openAITimeoutSec := int(record.GetFloat("openai_timeout_sec"))
	if openAITimeoutSec <= 0 {
		openAITimeoutSec = 60
	}
	workerTimeoutSec := int(record.GetFloat("worker_timeout_sec"))
	if workerTimeoutSec <= 0 {
		workerTimeoutSec = 300
	}

	threshold := record.GetFloat("near_duplicate_threshold")
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultNearDuplicateThreshold
	}

	cfg := Config{
		OCRProviderID:                 strings.TrimSpace(record.GetString("ocr_provider_id")),
		OCRModel:                      strings.TrimSpace(record.GetString("ocr_model")),
		ExtractProviderID:             strings.TrimSpace(record.GetString("extract_provider_id")),
		ExtractModel:                  strings.TrimSpace(record.GetString("extract_model")),
		ResearchProviderID:            strings.TrimSpace(record.GetString("research_provider_id")),
		ResearchModel:                 strings.TrimSpace(record.GetString("research_model")),
		EmbeddingProviderID:           strings.TrimSpace(record.GetString("embedding_provider_id")),
		EmbeddingModel:                strings.TrimSpace(record.GetString("embedding_model")),
		EmbeddingDims:                 max(int(record.GetFloat("embedding_dims")), 0),
		WebSearchProviderID:           strings.TrimSpace(record.GetString("websearch_provider_id")),
		OCRTimeout:                    time.Duration(ocrTimeoutSec) * time.Second,
		ProcessingResultLanguage:      strings.ToLower(strings.TrimSpace(record.GetString("processing_result_language"))),
		DeepSearchLanguages:           NormalizeLanguageList(record.GetString("deep_search_languages")),
		OpenAITimeout:                 time.Duration(openAITimeoutSec) * time.Second,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 time.Duration(workerTimeoutSec) * time.Second,
		WorkerMaxRetries:              max(int(record.GetFloat("worker_max_retries")), 0),
		ExtractionPromptVer:           strutil.FirstNonEmpty(record.GetString("extraction_prompt_version"), "v1"),
		ExtractionRules:               strings.TrimSpace(record.GetString("extraction_rules")),
		NearDuplicateDetectionEnabled: record.GetBool("near_duplicate_detection_enabled"),
		NearDuplicateThreshold:        threshold,
		AlwaysRequireReview:           record.GetBool("always_require_review"),
		IngestDirOwner:                strings.TrimSpace(record.GetString("ingest_dir_owner")),
		IngestDirIntervalMin:          ingestInterval,
		IngestDirDeleteOriginal:       record.GetBool("ingest_dir_delete_original"),
		IMAPHost:                      strings.TrimSpace(record.GetString("imap_host")),
		IMAPSecurity:                  strutil.FirstNonEmpty(record.GetString("imap_security"), IMAPSecurityTLS),
		IMAPUsername:                  strings.TrimSpace(record.GetString("imap_username")),
		IMAPPassword:                  record.GetString("imap_password"),
		IMAPFolder:                    strutil.FirstNonEmpty(record.GetString("imap_folder"), DefaultIMAPFolder),
		IMAPAfterConsume:              strutil.FirstNonEmpty(record.GetString("imap_after_consume"), IMAPKeep),
		IMAPMoveFolder:                strings.TrimSpace(record.GetString("imap_move_folder")),
		IMAPSince:                     record.GetDateTime("imap_since").Time(),
	}

	if err := resolveProviders(app, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ResearchBinding is the pair the Deep Research loop runs on: the research
// binding when both halves are set, else the general one. Resolved on the way
// out rather than into the stored fields, so Settings shows an empty research
// binding as empty. Embeddings have no such fallback: an embedding endpoint
// cannot be guessed from a language model, and a wrong guess would spend money
// on the whole archive before failing. Unset means off.
func (c Config) ResearchBinding() (providerID, model string) {
	if c.ResearchProviderID != "" && c.ResearchModel != "" {
		return c.ResearchProviderID, c.ResearchModel
	}
	return c.ExtractProviderID, c.ExtractModel
}

func resolveProviders(app core.App, cfg *Config) error {
	for _, binding := range []struct {
		id     string
		target **aiprovider.Provider
	}{
		{cfg.OCRProviderID, &cfg.OCRProvider},
		{cfg.ExtractProviderID, &cfg.ExtractProvider},
		{cfg.EmbeddingProviderID, &cfg.EmbeddingProvider},
		{cfg.WebSearchProviderID, &cfg.WebSearchProvider},
	} {
		provider, err := lookupProvider(app, binding.id)
		if err != nil {
			return err
		}
		*binding.target = provider
	}
	return nil
}

func lookupProvider(app core.App, id string) (*aiprovider.Provider, error) {
	p, err := aiprovider.FindByID(app, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup provider %s: %w", id, err)
	}
	return p, nil
}

func applyConfigToRecord(record *core.Record, cfg Config) {
	record.Set("ocr_provider_id", cfg.OCRProviderID)
	record.Set("ocr_model", cfg.OCRModel)
	record.Set("extract_provider_id", cfg.ExtractProviderID)
	record.Set("extract_model", cfg.ExtractModel)
	record.Set("research_provider_id", cfg.ResearchProviderID)
	record.Set("research_model", cfg.ResearchModel)
	record.Set("embedding_provider_id", cfg.EmbeddingProviderID)
	record.Set("embedding_model", cfg.EmbeddingModel)
	record.Set("embedding_dims", max(cfg.EmbeddingDims, 0))
	record.Set("websearch_provider_id", cfg.WebSearchProviderID)
	record.Set("ocr_timeout_sec", int(cfg.OCRTimeout.Seconds()))
	record.Set("processing_result_language", cfg.ProcessingResultLanguage)
	record.Set("deep_search_languages", cfg.DeepSearchLanguages)
	record.Set("openai_timeout_sec", int(cfg.OpenAITimeout.Seconds()))
	record.Set("worker_timeout_sec", int(cfg.WorkerTimeout.Seconds()))
	record.Set("worker_max_retries", cfg.WorkerMaxRetries)
	record.Set("extraction_prompt_version", cfg.ExtractionPromptVer)
	record.Set("extraction_rules", cfg.ExtractionRules)
	record.Set("near_duplicate_detection_enabled", cfg.NearDuplicateDetectionEnabled)
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultNearDuplicateThreshold
	}
	record.Set("near_duplicate_threshold", threshold)
	record.Set("always_require_review", cfg.AlwaysRequireReview)
	record.Set("ingest_dir_owner", cfg.IngestDirOwner)
	interval := cfg.IngestDirIntervalMin
	if interval <= 0 {
		interval = DefaultIngestDirIntervalMin
	}
	record.Set("ingest_dir_interval_min", interval)
	record.Set("ingest_dir_delete_original", cfg.IngestDirDeleteOriginal)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envIntDefault falls back when unset, malformed, or below min: a typo like
// "6O" must not become a zero-second HTTP timeout.
func envIntDefault(key string, fallback, min int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < min {
		return fallback
	}
	return parsed
}

func envInt64Default(key string, fallback, min int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil || parsed < min {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil || parsed <= 0 || parsed > 1 {
		return fallback
	}
	return parsed
}

// NormalizeLanguageList cleans a comma-separated ISO 639-1 list (e.g. "de, en, uk").
func NormalizeLanguageList(raw string) string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		code := strings.ToLower(strings.TrimSpace(part))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return strings.Join(out, ",")
}

// HasLLM is what the setup wizard and the readiness check ask before declaring
// an install finished. Configured rather than APIKey != "": the chatgpt SDK
// holds a token instead of a key, so the bare test would leave a signed-in
// instance stuck on the wizard with a working provider in front of it.
func HasLLM(cfg Config) bool {
	p := cfg.ExtractProvider
	return p != nil && p.Configured() && aiprovider.IsLLM(p.SDK)
}

// HasEmbedding is false for half a configuration (no credential, or no model),
// because treating it as on makes every document fail its embed step instead
// of skipping it. CanEmbed rather than IsLLM and Configured rather than a bare
// key test: the local SDK embeds without chatting and authenticates to nobody.
func HasEmbedding(cfg Config) bool {
	p := cfg.EmbeddingProvider
	return p != nil && aiprovider.CanEmbed(p.SDK) && p.Configured() &&
		strings.TrimSpace(cfg.EmbeddingModel) != ""
}

// HasWebSearch reports whether the web_search and web_fetch tools can be
// offered at all. No model term, unlike HasEmbedding: a web-search API takes
// none.
func HasWebSearch(cfg Config) bool {
	p := cfg.WebSearchProvider
	return p != nil && p.Configured() && aiprovider.CanWebSearch(p.SDK)
}

var recordEmbeddingDimsMu sync.Mutex

// RecordEmbeddingDims is called from the pipeline, not from Settings, because
// nobody can know the number before the first request: an admin types a model
// name and the provider decides how long its vectors are. Writing it back
// saves the settings record, which reloads the runtime.
func RecordEmbeddingDims(app core.App, dims int) error {
	if dims <= 0 {
		return nil
	}
	// ponytail: one process-wide lock. On the first embed after a model is
	// bound, every concurrent pipeline reads 0 and every one of them saves the
	// settings record -- and each save reloads the runtime, rebuilding the OCR,
	// AI, embedder and chat clients the other pipelines are mid-call on. Under
	// the lock the re-read below means only the first writer saves. It is two
	// queries on a path that already spent a round trip embedding, so a
	// finer-grained scheme would buy nothing.
	recordEmbeddingDimsMu.Lock()
	defer recordEmbeddingDimsMu.Unlock()

	record, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return fmt.Errorf("load %s: %w", CollectionName, err)
	}
	if int(record.GetFloat("embedding_dims")) == dims {
		return nil
	}
	record.Set("embedding_dims", dims)
	if err := app.Save(record); err != nil {
		return fmt.Errorf("save embedding_dims: %w", err)
	}
	app.Logger().Info("recorded embedding dimensions", "dims", dims)
	return nil
}

func HasOCR(cfg Config) bool {
	p := cfg.OCRProvider
	// Configured, not APIKey != "": a hosted provider needs a credential and a
	// local sidecar needs an address, so asking for a key unconditionally reports
	// a working docling container as unconfigured.
	if p == nil || !p.Configured() {
		return false
	}
	if aiprovider.RequiresOCRModel(p.SDK) && strings.TrimSpace(cfg.OCRModel) == "" {
		return false
	}
	return true
}
