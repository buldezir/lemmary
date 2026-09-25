package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

func settingsRecordForTest(t *testing.T) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection(config.CollectionName)
	collection.Fields.Add(
		&core.TextField{Name: "ocr_provider_id", Max: 15},
		&core.TextField{Name: "ocr_model", Max: 200},
		&core.TextField{Name: "extract_provider_id", Max: 15},
		&core.TextField{Name: "extract_model", Max: 200},
		&core.TextField{Name: "research_provider_id", Max: 15},
		&core.TextField{Name: "research_model", Max: 200},
		&core.TextField{Name: "embedding_provider_id", Max: 15},
		&core.TextField{Name: "embedding_model", Max: 200},
		&core.NumberField{Name: "embedding_dims", OnlyInt: true},
		&core.BoolField{Name: "always_require_review"},
		&core.TextField{Name: "ingest_dir_owner", Max: 15},
		&core.NumberField{Name: "ingest_dir_interval_min", OnlyInt: true},
		&core.BoolField{Name: "ingest_dir_delete_original"},
		&core.TextField{Name: "imap_host"},
		&core.TextField{Name: "imap_security"},
		&core.TextField{Name: "imap_username"},
		&core.TextField{Name: "imap_password"},
		&core.TextField{Name: "imap_folder"},
		&core.TextField{Name: "imap_after_consume"},
		&core.TextField{Name: "imap_move_folder"},
		&core.DateField{Name: "imap_since"},
		&core.SelectField{Name: "imap_skip_types", Values: []string{"pdf", "office", "image", "text"}, MaxSelect: 4},
	)
	record := core.NewRecord(collection)
	record.Id = config.SingletonID
	return record
}

// Changing the model or its length makes the recorded length a lie, and a
// vector index sized from it would silently drop everything.
func TestPatchResetsDimensionsWhenTheBindingChanges(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingModel: new("text-embedding-3-large"),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetInt("embedding_dims"); got != 0 {
		t.Fatalf("embedding_dims = %d, want 0 after a model change", got)
	}
}

func TestPatchKeepsDimensionsWhenTheBindingIsUnchanged(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingModel: new("text-embedding-3-small"),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetInt("embedding_dims"); got != 1536 {
		t.Fatalf("embedding_dims = %d; re-saving the same model must not reset it", got)
	}
}

// Half a binding reads as "the feature is on" and then fails on every document.
func TestPatchRefusesAnEmbeddingProviderWithoutAModel(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_provider_id", "provider1")
	record.Set("embedding_model", "text-embedding-3-small")

	err := applySettingsPatch(nil, record, settingsPatchRequest{EmbeddingModel: new("  ")})
	if err == nil {
		t.Fatal("clearing the model while a provider is bound should be refused")
	}
}

// Clearing both is how an admin turns dense retrieval off.
func TestPatchAllowsClearingTheWholeEmbeddingBinding(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingProviderID: new(""),
		EmbeddingModel:      new(""),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if record.GetString("embedding_model") != "" || record.GetInt("embedding_dims") != 0 {
		t.Fatalf("binding was not cleared: %v / %v", record.Get("embedding_model"), record.Get("embedding_dims"))
	}
}

// The operator owns the AI bill in managed mode: a tenant pointing the embedding
// binding at their own model would spend the operator's key.
func TestTouchesManagedCoversTheEmbeddingBinding(t *testing.T) {
	t.Parallel()

	if !(settingsPatchRequest{EmbeddingProviderID: new("provider1")}).touchesManaged() {
		t.Fatal("embedding_provider_id must count as managed")
	}
	if !(settingsPatchRequest{EmbeddingModel: new("m")}).touchesManaged() {
		t.Fatal("embedding_model must count as managed")
	}
	// The tenant-owned fields still are not.
	if (settingsPatchRequest{WorkerMaxRetries: new(int)}).touchesManaged() {
		t.Fatal("worker_max_retries is tenant-owned")
	}
}

// Research is the one LLM binding that may be empty: empty means the general
// model does that work too. The general one cannot be, since everything else
// runs on it.
func TestPatchResearchBindingMayBeEmptyAndIsManaged(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("extract_provider_id", "provider1")
	record.Set("extract_model", "small-model")

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		ResearchProviderID: new(""),
		ResearchModel:      new(""),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if record.GetString("research_provider_id") != "" || record.GetString("research_model") != "" {
		t.Fatalf("research binding = %v / %v", record.Get("research_provider_id"), record.Get("research_model"))
	}
	// A model with no provider never runs, and Settings would still show it.
	if err := applySettingsPatch(nil, record, settingsPatchRequest{ResearchModel: new("big-model")}); err == nil {
		t.Fatal("a research model without a provider must be refused")
	}
	record.Set("research_model", "")

	if err := applySettingsPatch(nil, record, settingsPatchRequest{ExtractModel: new("")}); err == nil {
		t.Fatal("an empty general model must be refused")
	}
	// A provider with no model would read as bound and quietly run research on
	// the general model. validateProviderID is skipped by a nil app only for an
	// empty id, so the provider is set on the record directly.
	record.Set("research_provider_id", "provider1")
	if err := applySettingsPatch(nil, record, settingsPatchRequest{ResearchModel: new("")}); err == nil {
		t.Fatal("a research provider without a model must be refused")
	}

	if !(settingsPatchRequest{ResearchProviderID: new("provider1")}).touchesManaged() {
		t.Fatal("research_provider_id must count as managed")
	}
	if !(settingsPatchRequest{ResearchModel: new("m")}).touchesManaged() {
		t.Fatal("research_model must count as managed")
	}

	got := settingsResponseFromConfig(config.Config{ResearchProviderID: "provider1", ResearchModel: "big-model"})
	if got.ResearchProviderID != "provider1" || got.ResearchModel != "big-model" {
		t.Fatalf("response = %+v", got)
	}
}

// embedding_dims has no patch field: it is learned from the provider.
func TestSettingsResponseExposesTheEmbeddingBinding(t *testing.T) {
	t.Parallel()
	got := settingsResponseFromConfig(config.Config{
		EmbeddingProviderID: "provider1",
		EmbeddingModel:      "text-embedding-3-small",
		EmbeddingDims:       1536,
	})

	if got.EmbeddingProviderID != "provider1" || got.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("response = %+v", got)
	}
	if got.EmbeddingDims != 1536 {
		t.Fatalf("embedding_dims = %d, want 1536", got.EmbeddingDims)
	}
}

// The three binding messages are derived from the capability predicates, so a
// new SDK cannot leave a sentence naming an old list.
func TestBindingRefusalsNameEverySDKThatCouldServe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		need    providerNeed
		refused string
		serves  []string
		mustSay []string
	}{
		// refused has to be a real SDK this binding turns down: CanOCR answers
		// true for anything it does not know, and ValidSDK gates what a row may
		// hold anyway.
		{"llm", needLLM, aiprovider.SDKGoogleVision, aiprovider.LLMSDKs(),
			[]string{aiprovider.SDKChatGPT}},
		{"embedding", needEmbedding, aiprovider.SDKGoogleVision, aiprovider.EmbeddingSDKs(),
			[]string{aiprovider.SDKLocalEmbeddings}},
		{"ocr", needOCR, aiprovider.SDKLocalEmbeddings, aiprovider.OCRSDKs(),
			[]string{aiprovider.SDKDocling, aiprovider.SDKChatGPT}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := providerServes(aiprovider.Provider{SDK: tc.refused}, tc.need)
			if err == nil {
				t.Fatalf("%s was accepted for this binding", tc.refused)
			}
			for _, sdk := range tc.serves {
				if !strings.Contains(err.Error(), sdk) {
					t.Errorf("message omits %s, which can serve this binding: %v", sdk, err)
				}
			}
			// Named explicitly too, so the derivation cannot quietly stop
			// producing the SDKs these messages used to leave out.
			for _, sdk := range tc.mustSay {
				if !strings.Contains(err.Error(), sdk) {
					t.Errorf("message omits %s: %v", sdk, err)
				}
			}
		})
	}

	// Every SDK that can serve is accepted too, so the lists and the predicates
	// cannot disagree.
	for _, sdk := range aiprovider.OCRSDKs() {
		if err := providerServes(aiprovider.Provider{SDK: sdk}, needOCR); err != nil {
			t.Errorf("OCR refused %s, which OCRSDKs names: %v", sdk, err)
		}
	}
	if err := providerServes(aiprovider.Provider{SDK: aiprovider.SDKLocalEmbeddings}, needOCR); err == nil {
		t.Error("OCR accepted the embeddings-only SDK")
	}
	if err := providerServes(aiprovider.Provider{SDK: aiprovider.SDKChatGPT}, needEmbedding); err == nil {
		t.Error("embeddings accepted chatgpt, whose endpoint has none")
	}
}

func TestPatchTurnsAlwaysRequireReviewOnAndOff(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)

	if err := applySettingsPatch(nil, record, settingsPatchRequest{
		AlwaysRequireReview: new(true),
	}); err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if !record.GetBool("always_require_review") {
		t.Fatal("always_require_review stayed off after a patch turning it on")
	}

	if err := applySettingsPatch(nil, record, settingsPatchRequest{
		AlwaysRequireReview: new(false),
	}); err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if record.GetBool("always_require_review") {
		t.Fatal("always_require_review stayed on after a patch turning it off")
	}
}

// Requiring review buys no API calls, so a managed tenant keeps it: naming a
// managed field fails the whole PATCH with a 403.
func TestAlwaysRequireReviewIsNotAManagedSetting(t *testing.T) {
	t.Parallel()
	if (settingsPatchRequest{AlwaysRequireReview: new(true)}).touchesManaged() {
		t.Fatal("always_require_review counts as managed; a hosted tenant could not set it")
	}
}

func TestPatchIngestDirFields(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)

	for _, bad := range []int{0, 45, 90, 420, 1441} {
		if err := applySettingsPatch(nil, record, settingsPatchRequest{IngestDirIntervalMin: new(bad)}); err == nil {
			t.Fatalf("expected interval %d to be refused: a cron step cannot space it evenly", bad)
		}
	}
	for _, good := range []int{1, 30, 60, 180, 1440} {
		if err := applySettingsPatch(nil, record, settingsPatchRequest{IngestDirIntervalMin: new(good)}); err != nil {
			t.Fatalf("interval %d: %v", good, err)
		}
	}
	err := applySettingsPatch(nil, record, settingsPatchRequest{
		IngestDirOwner:          new("  user00000000001 "),
		IngestDirIntervalMin:    new(15),
		IngestDirDeleteOriginal: new(true),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetString("ingest_dir_owner"); got != "user00000000001" {
		t.Fatalf("ingest_dir_owner = %q", got)
	}
	if got := record.GetInt("ingest_dir_interval_min"); got != 15 {
		t.Fatalf("ingest_dir_interval_min = %d", got)
	}
	if !record.GetBool("ingest_dir_delete_original") {
		t.Fatal("ingest_dir_delete_original not stored")
	}
	if (settingsPatchRequest{IngestDirDeleteOriginal: new(true)}).touchesManaged() {
		t.Fatal("ingest_dir fields are tenant-owned; a hosted tenant must be able to set them")
	}
}

func TestPatchIMAPFields(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		IMAPHost:     new(" imap.example.com "),
		IMAPSecurity: new("starttls"),
		IMAPUsername: new("docs@example.com"),
		IMAPPassword: new("s3cret"),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetString("imap_host"); got != "imap.example.com" {
		t.Fatalf("imap_host = %q", got)
	}
	if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPPassword: new("")}); err != nil {
		t.Fatalf("blank password: %v", err)
	}
	if got := record.GetString("imap_password"); got != "s3cret" {
		t.Fatalf("a blank password must keep the stored one, got %q", got)
	}

	for _, bad := range []settingsPatchRequest{
		{IMAPSecurity: new("none")},
		{IMAPAfterConsume: new("archive")},
		{IMAPAfterConsume: new("move")},
		{IMAPAfterConsume: new("move"), IMAPMoveFolder: new("inbox")},
	} {
		if err := applySettingsPatch(nil, record, bad); err == nil {
			t.Fatalf("expected %+v to be refused", bad)
		}
		record.Set("imap_after_consume", "")
		record.Set("imap_move_folder", "")
	}
	err = applySettingsPatch(nil, record, settingsPatchRequest{
		IMAPAfterConsume: new("move"),
		IMAPMoveFolder:   new("Lemmary/Done"),
	})
	if err != nil {
		t.Fatalf("move with a target: %v", err)
	}

	skip := []string{"image", "text"}
	if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPSkipTypes: &skip}); err != nil {
		t.Fatalf("skip types: %v", err)
	}
	if got := record.GetStringSlice("imap_skip_types"); !slices.Equal(got, skip) {
		t.Fatalf("imap_skip_types = %v", got)
	}
	for _, bad := range [][]string{{"exe"}, {"pdf", "office", "image", "text"}} {
		if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPSkipTypes: &bad}); err == nil {
			t.Fatalf("expected skip types %v to be refused", bad)
		}
	}
	if body, _ := json.Marshal(settingsResponseFromConfig(config.Config{})); !strings.Contains(string(body), `"imap_skip_types":[]`) {
		t.Fatalf("no skip types must read as an empty list: %s", body)
	}

	cfg := config.Config{IMAPPassword: "s3cret"}
	if !settingsResponseFromConfig(cfg).IMAPPasswordSet {
		t.Fatal("imap_password_set should report a stored password")
	}
	body, _ := json.Marshal(settingsResponseFromConfig(cfg))
	if strings.Contains(string(body), "s3cret") {
		t.Fatalf("the password must never be returned: %s", body)
	}
	if (settingsPatchRequest{IMAPHost: new("x")}).touchesManaged() {
		t.Fatal("imap fields are tenant-owned; a hosted tenant must be able to set them")
	}
}

// Pointing at a mailbox imports what arrives from then on; a new password or
// after-import action must not skip the mail that came in meanwhile.
func TestIMAPSinceMovesOnlyWhenTheMailboxChanges(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	since := func() time.Time { return record.GetDateTime("imap_since").Time() }

	if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPUsername: new("docs")}); err != nil {
		t.Fatal(err)
	}
	if !since().IsZero() {
		t.Fatal("imap_since set without a server")
	}
	before := time.Now().Add(-time.Second)
	if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPHost: new("imap.example.com")}); err != nil {
		t.Fatal(err)
	}
	first := since()
	if first.Before(before) {
		t.Fatalf("imap_since = %v, want now", first)
	}

	record.Set("imap_since", first.Add(-time.Hour))
	for _, same := range []settingsPatchRequest{
		{IMAPPassword: new("new")},
		{IMAPAfterConsume: new("delete")},
		{IMAPHost: new("imap.example.com"), IMAPFolder: new("INBOX")},
	} {
		if err := applySettingsPatch(nil, record, same); err != nil {
			t.Fatal(err)
		}
		if !since().Equal(first.Add(-time.Hour)) {
			t.Fatalf("%+v moved imap_since", same)
		}
	}
	if err := applySettingsPatch(nil, record, settingsPatchRequest{IMAPFolder: new("Scans")}); err != nil {
		t.Fatal(err)
	}
	if !since().After(first.Add(-time.Hour)) {
		t.Fatal("a new folder must move imap_since")
	}
}

func TestPatchStoresTrimmedExtractionRules(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)

	rules := "  Treat Rechnung as the document type Invoice.  "
	if err := applySettingsPatch(nil, record, settingsPatchRequest{ExtractionRules: &rules}); err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetString("extraction_rules"); got != "Treat Rechnung as the document type Invoice." {
		t.Fatalf("extraction_rules = %q", got)
	}

	// The field's own Max would refuse this too, but only with a validation
	// error nobody can read.
	tooLong := strings.Repeat("x", maxExtractionRules+1)
	if err := applySettingsPatch(nil, record, settingsPatchRequest{ExtractionRules: &tooLong}); err == nil {
		t.Fatal("expected rules longer than the cap to be refused")
	}
}

// Without the read side, a patch could store rules the Settings page would
// never show back.
func TestSettingsResponseCarriesExtractionRules(t *testing.T) {
	t.Parallel()
	res := settingsResponseFromConfig(config.Config{
		ExtractionRules: "Always tag invoices with the vendor's city.",
	})
	if res.ExtractionRules != "Always tag invoices with the vendor's city." {
		t.Fatalf("ExtractionRules = %q", res.ExtractionRules)
	}
}

// The rules cost nothing but the prompt they ride in, so a managed tenant keeps
// them: naming a managed field fails the whole PATCH with a 403.
func TestExtractionRulesAreNotAManagedSetting(t *testing.T) {
	t.Parallel()
	rules := "Always tag invoices with the vendor's city."
	if (settingsPatchRequest{ExtractionRules: &rules}).touchesManaged() {
		t.Fatal("extraction_rules counts as managed; a hosted tenant could not set it")
	}
}

func TestOneOfReadsAsASentence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sdks []string
		want string
	}{
		{nil, "a configured"},
		{[]string{"openai"}, "an openai"},
		{[]string{"openai", "mistral"}, "an openai or mistral"},
		{[]string{"openai", "mistral", "local"}, "an openai, mistral, or local"},
		{[]string{"docling"}, "a docling"},
	}
	for _, tc := range cases {
		if got := oneOf(tc.sdks); got != tc.want {
			t.Errorf("oneOf(%v) = %q, want %q", tc.sdks, got, tc.want)
		}
	}
}

// Validated before the record is touched, so a value PocketBase would reject
// is refused with a message an admin can read.
func TestBrandingPatchValidatesNameAndAccent(t *testing.T) {
	t.Parallel()

	name, accent, err := brandingPatch(settingsPatchRequest{
		AppName: new("  Archive  "),
		Accent:  new("#6E2620"),
	})
	if err != nil {
		t.Fatalf("valid branding rejected: %v", err)
	}
	if name == nil || *name != "Archive" {
		t.Fatalf("app_name = %v, want trimmed Archive", name)
	}
	if accent == nil || *accent != "#6E2620" {
		t.Fatalf("accent = %v, want #6E2620", accent)
	}

	// Absent stays absent: the stored value is kept.
	if name, accent, err = brandingPatch(settingsPatchRequest{}); err != nil || name != nil || accent != nil {
		t.Fatalf("empty patch touched branding: %v %v %v", name, accent, err)
	}

	// Empty clears the accent back to the built-in one.
	if _, accent, err = brandingPatch(settingsPatchRequest{Accent: new(" ")}); err != nil || accent == nil || *accent != "" {
		t.Fatalf("blank accent = %v (%v), want cleared", accent, err)
	}

	if _, _, err = brandingPatch(settingsPatchRequest{AppName: new("   ")}); err == nil {
		t.Fatal("empty app_name must be refused: PocketBase requires one")
	}
	for _, bad := range []string{"6e2620", "#6e262", "#ggmmbb", "rebeccapurple"} {
		if _, _, err = brandingPatch(settingsPatchRequest{Accent: new(bad)}); err == nil {
			t.Fatalf("accent %q must be refused", bad)
		}
	}
}

func TestPatchSettingsSavesBrandingWithTheRest(t *testing.T) {
	app := bootQueueApp(t)
	rt := &config.Runtime{}

	rec := callManaged(t, handlePatchSettings(app, rt), http.MethodPatch, "",
		`{"worker_max_retries":7,"app_name":"Archive","accent":"#6e2620"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	record, err := config.FindSettingsRecord(app, rt.Env())
	if err != nil {
		t.Fatal(err)
	}
	if got := record.GetInt("worker_max_retries"); got != 7 {
		t.Fatalf("worker_max_retries = %d, want 7", got)
	}
	if meta := app.Settings().Meta; meta.AppName != "Archive" || meta.AccentColor != "#6e2620" {
		t.Fatalf("live branding = %q %q, want it applied once the request returns", meta.AppName, meta.AccentColor)
	}
}

// A branding save that fails must take the settings half down with it: the
// client is told the PATCH failed.
func TestPatchSettingsRollsBackWhenBrandingFails(t *testing.T) {
	app := bootQueueApp(t)
	rt := &config.Runtime{}
	before, err := config.FindSettingsRecord(app, rt.Env())
	if err != nil {
		t.Fatal(err)
	}
	nameBefore := app.Settings().Meta.AppName
	app.OnModelUpdate(app.Settings().TableName()).BindFunc(func(*core.ModelEvent) error {
		return errors.New("settings refused")
	})

	rec := callManaged(t, handlePatchSettings(app, rt), http.MethodPatch, "",
		`{"worker_max_retries":7,"app_name":"Archive"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Failed to save the application name or accent color.") {
		t.Fatalf("patch = %d %s, want the branding 400", rec.Code, rec.Body)
	}
	after, err := config.FindSettingsRecord(app, rt.Env())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.GetInt("worker_max_retries"), before.GetInt("worker_max_retries"); got != want {
		t.Fatalf("worker_max_retries = %d, want %d: the settings half was committed", got, want)
	}
	if got := app.Settings().Meta.AppName; got != nameBefore {
		t.Fatalf("live app name = %q, want %q after a failed save", got, nameBefore)
	}
}

// Branding is tenant-owned: a managed instance sets the models, not the name.
func TestBrandingIsNotAManagedSetting(t *testing.T) {
	t.Parallel()
	if (settingsPatchRequest{AppName: new("Archive"), Accent: new("#000000")}).touchesManaged() {
		t.Fatal("app_name and accent are tenant-owned")
	}
}
