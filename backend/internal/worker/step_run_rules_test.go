package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

type fieldsExtractor struct {
	stubExtractor
	answers map[string]any
	asked   []string
	prompt  string
	// questions holds what AnswerYesNo was asked and the document it saw.
	questions []string
	document  string
}

func (f *fieldsExtractor) AnswerYesNo(_ context.Context, document, question string) (bool, error) {
	f.questions = append(f.questions, question)
	f.document = document
	return strings.Contains(question, "reminder"), nil
}

func (f *fieldsExtractor) ExtractFields(_ context.Context, _ string, instructions string, fields []models.CustomField) (map[string]any, error) {
	f.prompt = instructions
	for _, field := range fields {
		f.asked = append(f.asked, field.Name)
	}
	return f.answers, nil
}

var _ ai.Extractor = (*fieldsExtractor)(nil)

func saveTestField(t *testing.T, app core.App, name, fieldType string, choices ...string) models.CustomField {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	record.Set("type", fieldType)
	if err := app.Save(record); err != nil {
		t.Fatalf("save field %s: %v", name, err)
	}
	if len(choices) > 0 {
		var want []models.FieldChoice
		for _, c := range choices {
			want = append(want, models.FieldChoice{Name: c})
		}
		if err := models.SyncFieldChoices(app, record.Id, want); err != nil {
			t.Fatal(err)
		}
	}
	fields, err := models.LoadCustomFields(app)
	if err != nil {
		t.Fatal(err)
	}
	return fields[slices.IndexFunc(fields, func(f models.CustomField) bool { return f.ID == record.Id })]
}

func saveTestRule(t *testing.T, app core.App, userID, name string, enabled bool, triggers []string, conditions []rules.Condition, actions []rules.Action) {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := app.CountRecords(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(coll)
	record.Set("user", userID)
	record.Set("position", existing)
	record.Set("name", name)
	record.Set("enabled", enabled)
	record.Set("triggers", triggers)
	record.Set("conditions", conditions)
	record.Set("actions", actions)
	if err := app.Save(record); err != nil {
		t.Fatalf("save rule %s: %v", name, err)
	}
}

func rulesTestDocument(t *testing.T, app core.App, owner, documentType string) *core.Record {
	t.Helper()
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	doc := core.NewRecord(documents)
	doc.Set("user", owner)
	doc.Set("title", "Invoice R-1")
	doc.Set("ocr_text", "Invoice R-1, total 1.200,00 EUR")
	doc.Set("processing_status", models.DocStatusCompleted)
	file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc.Set("file", file)
	SkipCreateJob(doc)
	if err := app.Save(doc); err != nil {
		t.Fatalf("save document: %v", err)
	}
	typeID, _, err := EnsureOption(app, models.DocumentTypeFieldID, owner, documentType)
	if err != nil {
		t.Fatal(err)
	}
	if err := models.SaveFieldValue(app, doc, models.DocumentTypeField, typeID); err != nil {
		t.Fatal(err)
	}
	return doc
}

func rulesTestJob(t *testing.T, app core.App, doc *core.Record, steps []string) *core.Record {
	t.Helper()
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}
	job := core.NewRecord(jobs)
	job.Set("document", doc.Id)
	job.Set("status", models.JobStatusRunning)
	job.Set("steps", steps)
	return job
}

// One pass in the rules' order: the second rule reads the amount the first
// one's AI action filled, the webhook goes out after the save with the
// document as stored, and a disabled or edit-only rule never runs.
func TestRunRulesAppliesMatchingRulesInOrder(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	toPay := createTag(t, app, "to pay", owner)
	big := createTag(t, app, "big", owner)
	amount := saveTestField(t, app, "Amount", models.CustomFieldNumber)
	status := saveTestField(t, app, "Status", models.CustomFieldChoice, "Paid", "Open")
	doc := rulesTestDocument(t, app, owner, "Invoice")
	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	invoiceType := values.OptionID(models.DocumentTypeFieldID)

	var payload rules.WebhookPayload
	var signature string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		signature = r.Header.Get(rules.SignatureHeader)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("webhook body: %v", err)
		}
	}))
	t.Cleanup(hook.Close)

	processed := []string{rules.TriggerProcessed}
	saveTestRule(t, app, owner, "Invoices", true, processed,
		[]rules.Condition{{Subject: models.DocumentTypeFieldID, Op: rules.OpIs, Value: invoiceType}},
		[]rules.Action{
			{Type: rules.ActionAIExtract, Prompt: "The amount is the gross total.", Fields: []string{amount.ID, status.ID}},
			{Type: rules.ActionAddTag, Value: toPay},
			{Type: rules.ActionWebhook, URL: hook.URL, Secret: "s3cret"},
		})
	saveTestRule(t, app, owner, "Big", true, processed,
		[]rules.Condition{{Subject: amount.ID, Op: rules.OpGreater, Value: 1000.0}},
		[]rules.Action{{Type: rules.ActionRequireReview}, {Type: rules.ActionAddTag, Value: big}})
	saveTestRule(t, app, owner, "Off", false, processed, nil,
		[]rules.Action{{Type: rules.ActionRemoveTag, Value: toPay}})
	saveTestRule(t, app, owner, "Edits only", true, []string{rules.TriggerEdited}, nil,
		[]rules.Action{{Type: rules.ActionRemoveTag, Value: toPay}})

	extractor := &fieldsExtractor{answers: map[string]any{"Amount": "1200", "Status": "open"}}
	state := &StepState{
		App:      app,
		Cfg:      config.Config{OpenAITimeout: 5e9},
		Job:      rulesTestJob(t, app, doc, []string{models.StepApplyMetadata, models.StepRunRules}),
		Document: doc,
		Logger:   slog.Default(),
	}
	step := &RunRulesStep{Extractor: extractor, HTTPClient: hook.Client()}
	if skip, err := step.ShouldSkip(state); err != nil || skip {
		t.Fatalf("ShouldSkip = %v, %v", skip, err)
	}
	if err := step.Run(context.Background(), state); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.StepDetail != "Invoices, Big" {
		t.Fatalf("detail = %q", state.StepDetail)
	}
	if extractor.prompt != "The amount is the gross total." || !slices.Equal(extractor.asked, []string{"Amount", "Status"}) {
		t.Fatalf("AI asked %v with %q", extractor.asked, extractor.prompt)
	}

	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetStringSlice("tags"); !slices.Equal(got, []string{toPay, big}) {
		t.Fatalf("tags = %v", got)
	}
	if got := stored.GetString("processing_status"); got != models.DocStatusNeedsReview {
		t.Fatalf("status = %q", got)
	}
	values, err = models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if values[amount.ID].Number != 1200 || values[status.ID].ChoiceName != "Open" {
		t.Fatalf("values = %#v", values)
	}

	if payload.Rule.Name != "Invoices" || payload.Trigger != rules.TriggerProcessed || payload.Event != rules.WebhookEvent {
		t.Fatalf("payload = %+v", payload)
	}
	got := payload.Document
	if got.ID != doc.Id || got.DocumentType != "Invoice" || got.Fields["Amount"] != 1200.0 || got.Fields["Status"] != "Open" ||
		!slices.Equal(got.Tags, []string{"to pay", "big"}) || got.ProcessingStatus != models.DocStatusNeedsReview {
		t.Fatalf("webhook document = %+v", got)
	}
	if !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("signature = %q", signature)
	}
}

// An admin's global rule runs before the owner's own, matches the owner's tags
// and types by name, and creates the tag and type it sets when the owner has
// none of that name; another user's rules never run.
func TestRunRulesRunsGlobalRulesFirstByName(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	other := createTagUser(t, app, "other@example.com")
	mine := createTag(t, app, "mine", owner)
	doc := rulesTestDocument(t, app, owner, "Invoice")

	processed := []string{rules.TriggerProcessed}
	saveTestRule(t, app, owner, "Own", true, processed,
		[]rules.Condition{{Subject: rules.SubjectTag, Op: rules.OpIsNot, Value: mine}},
		[]rules.Action{{Type: rules.ActionAddTag, Value: mine}})
	saveTestRule(t, app, "", "Global", true, processed,
		[]rules.Condition{{Subject: models.DocumentTypeFieldID, Op: rules.OpIs, Value: "invoice"}},
		[]rules.Action{
			{Type: rules.ActionAddTag, Value: "Bookkeeping"},
			{Type: rules.ActionRemoveTag, Value: "never had it"},
			{Type: rules.ActionSetField, Field: models.CorrespondentFieldID, Value: "Tax office"},
		})
	saveTestRule(t, app, other, "Someone else's", true, processed, nil,
		[]rules.Action{{Type: rules.ActionRequireReview}})

	state := &StepState{
		App:      app,
		Job:      rulesTestJob(t, app, doc, models.ExtractionPipelineSteps),
		Document: doc,
		Logger:   slog.Default(),
	}
	if err := (&RunRulesStep{}).Run(context.Background(), state); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.StepDetail != "Global, Own" {
		t.Fatalf("detail = %q", state.StepDetail)
	}
	bookkeeping, err := findTagByName(app, owner, "Bookkeeping")
	if err != nil || bookkeeping == "" {
		t.Fatalf("the global rule's tag was not created for the owner: %q %v", bookkeeping, err)
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetStringSlice("tags"); !slices.Equal(got, []string{bookkeeping, mine}) {
		t.Fatalf("tags = %v", got)
	}
	if got := stored.GetString("processing_status"); got != models.DocStatusCompleted {
		t.Fatalf("another user's rule ran: status %q", got)
	}
	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := values.OptionName(models.CorrespondentFieldID); got != "Tax office" {
		t.Fatalf("correspondent = %q", got)
	}
}

// A rule's AI question sees the document's metadata as the rules above left it
// and its text, and decides whether the rule runs.
func TestRunRulesAsksTheAIQuestion(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	toPay := createTag(t, app, "to pay", owner)
	reminder := createTag(t, app, "reminder", owner)
	doc := rulesTestDocument(t, app, owner, "Invoice")

	processed := []string{rules.TriggerProcessed}
	saveTestRule(t, app, owner, "Tag", true, processed, nil,
		[]rules.Action{{Type: rules.ActionAddTag, Value: toPay}})
	saveTestRule(t, app, owner, "Reminders", true, processed,
		[]rules.Condition{{Subject: rules.SubjectAI, Op: rules.OpIs, Value: "Is this a payment reminder?"}},
		[]rules.Action{{Type: rules.ActionAddTag, Value: reminder}})
	saveTestRule(t, app, owner, "Not paid", true, processed,
		[]rules.Condition{{Subject: rules.SubjectAI, Op: rules.OpIs, Value: "Is it paid?"}},
		[]rules.Action{{Type: rules.ActionRequireReview}})

	extractor := &fieldsExtractor{}
	state := &StepState{
		App:      app,
		Cfg:      config.Config{OpenAITimeout: 5e9},
		Job:      rulesTestJob(t, app, doc, models.ExtractionPipelineSteps),
		Document: doc,
		Logger:   slog.Default(),
	}
	if err := (&RunRulesStep{Extractor: extractor}).Run(context.Background(), state); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.StepDetail != "Tag, Reminders" {
		t.Fatalf("detail = %q", state.StepDetail)
	}
	if len(extractor.questions) != 2 {
		t.Fatalf("questions = %v", extractor.questions)
	}
	for _, want := range []string{`"document_type": "Invoice"`, `"to pay"`, "Invoice R-1, total"} {
		if !strings.Contains(extractor.document, want) {
			t.Fatalf("the question's document lacks %q:\n%s", want, extractor.document)
		}
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetStringSlice("tags"); !slices.Equal(got, []string{toPay, reminder}) {
		t.Fatalf("tags = %v", got)
	}
	if got := stored.GetString("processing_status"); got != models.DocStatusCompleted {
		t.Fatalf("a rule whose question was answered no ran: %q", got)
	}
}

// What an action could not do fails the step softly, after the rest is saved.
func TestRunRulesFailsSoftlyAfterSavingTheRest(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	toPay := createTag(t, app, "to pay", owner)
	doc := rulesTestDocument(t, app, owner, "Invoice")

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(hook.Close)
	saveTestRule(t, app, owner, "Everything", true, []string{rules.TriggerProcessed}, nil, []rules.Action{
		{Type: rules.ActionWebhook, URL: hook.URL},
		{Type: rules.ActionAddTag, Value: "tdeletedsince00"},
		{Type: rules.ActionAddTag, Value: toPay},
	})

	state := &StepState{
		App:      app,
		Job:      rulesTestJob(t, app, doc, models.ExtractionPipelineSteps),
		Document: doc,
		Logger:   slog.Default(),
	}
	err := (&RunRulesStep{HTTPClient: hook.Client()}).Run(context.Background(), state)
	if !errors.Is(err, ErrStepSoft) || !strings.Contains(err.Error(), "answered 502") || !strings.Contains(err.Error(), "tag no longer exists") {
		t.Fatalf("err = %v", err)
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetStringSlice("tags"); !slices.Equal(got, []string{toPay}) {
		t.Fatalf("tags = %v", got)
	}
}

// An edit queues the edited rules once, in a job that hands the document back
// in the status it had: one edited in the Inbox stays there.
func TestEnqueueEditedRulesReturnsTheDocumentAsItWas(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	doc := rulesTestDocument(t, app, owner, "Invoice")
	doc.Set("processing_status", models.DocStatusNeedsReview)
	if err := app.Save(doc); err != nil {
		t.Fatal(err)
	}

	if err := EnqueueEditedRules(app, doc); err != nil {
		t.Fatal(err)
	}
	if n, _ := app.CountRecords("processing_jobs"); n != 0 {
		t.Fatalf("queued %d jobs for an owner without edited rules", n)
	}

	saveTestRule(t, app, owner, "Edits", true, []string{rules.TriggerEdited}, nil,
		[]rules.Action{{Type: rules.ActionRequireReview}})
	for range 2 {
		if err := EnqueueEditedRules(app, doc); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := app.FindAllRecords("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want one", len(jobs))
	}
	job := jobs[0]
	if got := job.GetString(models.JobReturnStatus); got != models.DocStatusNeedsReview {
		t.Fatalf("return_status = %q", got)
	}
	if rulesTrigger(job) != rules.TriggerEdited {
		t.Fatal("a rules-only job is an edit")
	}

	doc.Set("processing_status", models.DocStatusProcessing)
	if err := app.Save(doc); err != nil {
		t.Fatal(err)
	}
	edited, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	edited.Set("title", "Renamed while the job ran")
	if err := app.Save(edited); err != nil {
		t.Fatal(err)
	}
	if err := finalizeDocumentWithoutApply(app, job, doc, models.EditedRulesSteps); err != nil {
		t.Fatal(err)
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetString("processing_status"); got != models.DocStatusNeedsReview {
		t.Fatalf("status after the job = %q", got)
	}
	if got := stored.GetString("title"); got != "Renamed while the job ran" {
		t.Fatalf("the job put back its own title: %q", got)
	}
}

// The job's copy is as old as the job: what the owner changed meanwhile
// survives the rules' save, and the webhook reports the status the job will
// hand the document back in, not processing.
func TestRunRulesKeepsEditsMadeWhileItRan(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	toPay := createTag(t, app, "to pay", owner)
	urgent := createTag(t, app, "urgent", owner)
	doc := rulesTestDocument(t, app, owner, "Invoice")
	doc.Set("processing_status", models.DocStatusProcessing)
	if err := app.Save(doc); err != nil {
		t.Fatal(err)
	}

	var status string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload rules.WebhookPayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		status = payload.Document.ProcessingStatus
	}))
	t.Cleanup(hook.Close)
	saveTestRule(t, app, owner, "Edits", true, []string{rules.TriggerEdited}, nil, []rules.Action{
		{Type: rules.ActionAddTag, Value: toPay},
		{Type: rules.ActionWebhook, URL: hook.URL},
	})

	job := rulesTestJob(t, app, doc, models.EditedRulesSteps)
	job.Set(models.JobReturnStatus, models.DocStatusCompleted)
	state := &StepState{App: app, Job: job, Document: doc, Logger: slog.Default()}

	edited, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	edited.Set("title", "Renamed while the job ran")
	edited.Set("tags", []string{urgent})
	if err := app.Save(edited); err != nil {
		t.Fatal(err)
	}

	if err := (&RunRulesStep{HTTPClient: hook.Client()}).Run(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetString("title"); got != "Renamed while the job ran" {
		t.Fatalf("title = %q", got)
	}
	if got := stored.GetStringSlice("tags"); !slices.Equal(got, []string{urgent, toPay}) {
		t.Fatalf("tags = %v", got)
	}
	if status != models.DocStatusCompleted {
		t.Fatalf("webhook processing_status = %q", status)
	}
}
