package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/netguard"
	"lemmary/backend/internal/rules"
	"lemmary/backend/internal/strutil"
)

// RunRulesStep runs the document owner's rules. It enriches a document rather
// than producing one, so whatever goes wrong in an action -- a model that does
// not answer, a webhook that is down, a tag deleted since the rule was saved --
// fails it softly, after everything else it did is saved.
type RunRulesStep struct {
	Extractor ai.Extractor
	// Nil means netguard's client; tests reach httptest on loopback with their own.
	HTTPClient *http.Client
}

func (s *RunRulesStep) Name() string { return models.StepRunRules }

// rulesTrigger reads the trigger off the job: one that applies extracted
// metadata is a processed document, a rules-only job is an edit.
func rulesTrigger(job *core.Record) string {
	if steps, err := parseSteps(job); err == nil && slices.Contains(steps, models.StepApplyMetadata) {
		return rules.TriggerProcessed
	}
	return rules.TriggerEdited
}

func (s *RunRulesStep) ShouldSkip(state *StepState) (bool, error) {
	if state.Document.GetString("duplicate_of") != "" {
		return true, nil
	}
	list, err := rules.Load(state.App, state.Document.GetString("user"), rulesTrigger(state.Job))
	return len(list) == 0, err
}

type pendingWebhook struct {
	rule   rules.Rule
	action rules.Action
}

type rulesRun struct {
	state     *StepState
	extractor ai.Extractor
	cat       rules.Catalog
	doc       *rules.Document
	changed   map[string]bool
	review    bool
	hooks     []pendingWebhook
	problems  []error
	overwrite bool
}

func (s *RunRulesStep) Run(ctx context.Context, state *StepState) error {
	userID := state.Document.GetString("user")
	trigger := rulesTrigger(state.Job)
	list, err := rules.Load(state.App, userID, trigger)
	if err != nil {
		return err
	}
	cat, err := rules.LoadCatalog(state.App, userID)
	if err != nil {
		return err
	}
	values, err := models.LoadDocumentFieldValues(state.App, state.Document.Id)
	if err != nil {
		return err
	}
	run := &rulesRun{
		state:     state,
		extractor: s.Extractor,
		cat:       cat,
		changed:   map[string]bool{},
		overwrite: state.Job.GetBool(models.JobOverwriteCustomFields),
		doc: &rules.Document{
			Title:      state.Document.GetString("title"),
			Text:       state.Document.GetString("ocr_text"),
			Confidence: state.Document.GetFloat("confidence"),
			Tags:       state.Document.GetStringSlice("tags"),
			Values:     values,
		},
	}

	var fired []string
	for _, rule := range list {
		matched, err := rule.Localize(cat).Matches(run.doc, cat.Fields, run.asker(ctx))
		if err != nil {
			run.fail(rule, "AI question: %v", err)
		}
		if !matched {
			continue
		}
		fired = append(fired, rule.Name)
		for _, action := range rule.Actions {
			run.apply(ctx, rule, action)
		}
	}
	state.StepDetail = strings.Join(fired, ", ")
	state.Logger.Info("rules run", "trigger", trigger, "rules", len(list), "matched", len(fired))

	if err := run.save(); err != nil {
		return err
	}
	client := s.HTTPClient
	if client == nil && len(run.hooks) > 0 {
		client = netguard.Client(rules.WebhookTimeout)
	}
	for _, hook := range run.hooks {
		run.deliver(ctx, client, trigger, hook)
	}
	if len(run.problems) > 0 {
		return fmt.Errorf("%w: %w", ErrStepSoft, errors.Join(run.problems...))
	}
	return nil
}

// asker puts a rule's question to the extraction model, with the document as
// the rules have left it so far: its metadata as a webhook would carry it, then
// its text.
func (r *rulesRun) asker(ctx context.Context) rules.Asker {
	if r.extractor == nil {
		return nil
	}
	return func(question string) (bool, error) {
		metadata, err := json.MarshalIndent(r.webhookDocument(), "", "  ")
		if err != nil {
			return false, err
		}
		aiCtx, cancel := context.WithTimeout(ctx, r.state.Cfg.OpenAITimeout)
		defer cancel()
		return r.extractor.AnswerYesNo(aiCtx, "Metadata:\n"+string(metadata)+"\n\nText:\n"+r.doc.Text, question)
	}
}

func (r *rulesRun) fail(rule rules.Rule, format string, args ...any) {
	r.problems = append(r.problems, fmt.Errorf("rule %q: %s", rule.Name, fmt.Sprintf(format, args...)))
}

func (r *rulesRun) apply(ctx context.Context, rule rules.Rule, action rules.Action) {
	switch action.Type {
	case rules.ActionAddTag, rules.ActionRemoveTag:
		r.applyTag(rule, action)
	case rules.ActionSetField:
		r.applySetField(rule, action)
	case rules.ActionRequireReview:
		r.review = true
	case rules.ActionAIExtract:
		r.applyAIExtract(ctx, rule, action)
	case rules.ActionWebhook:
		// After the save, so every webhook sees the document as stored.
		r.hooks = append(r.hooks, pendingWebhook{rule: rule, action: action})
	default:
		r.fail(rule, "unknown action %q", action.Type)
	}
}

func (r *rulesRun) applyTag(rule rules.Rule, action rules.Action) {
	id, ok := r.tagID(rule, action)
	if !ok {
		return
	}
	has := slices.Contains(r.doc.Tags, id)
	if action.Type == rules.ActionAddTag && !has {
		r.doc.Tags = append(r.doc.Tags, id)
		r.changed["tags"] = true
	}
	if action.Type == rules.ActionRemoveTag && has {
		r.doc.Tags = slices.DeleteFunc(slices.Clone(r.doc.Tags), func(t string) bool { return t == id })
		r.changed["tags"] = true
	}
}

// tagID is the owner's tag an action names. A global rule names it, and adding
// a tag the owner lacks creates it in their list: the admin asked for it on
// every document, and a rule that silently does nothing for some users would
// be worse. Removing one they lack is nothing to do.
func (r *rulesRun) tagID(rule rules.Rule, action rules.Action) (string, bool) {
	value, _ := action.Value.(string)
	if !rule.Global {
		if _, ok := r.cat.Tags[value]; !ok {
			r.fail(rule, "its tag no longer exists")
			return "", false
		}
		return value, true
	}
	if id := r.cat.IDByName("", value); id != "" {
		return id, true
	}
	if action.Type == rules.ActionRemoveTag {
		return "", false
	}
	id, _, err := EnsureTag(r.state.App, r.state.Document.GetString("user"), value)
	if err != nil {
		r.fail(rule, "tag %q: %v", value, err)
		return "", false
	}
	r.cat.Tags[id] = value
	return id, true
}

func (r *rulesRun) applySetField(rule rules.Rule, action rules.Action) {
	field, ok := r.cat.Fields[action.Field]
	if !ok {
		r.fail(rule, "its field no longer exists")
		return
	}
	raw := action.Value
	if rule.Global && field.Type == models.CustomFieldOption {
		// Named, like a global rule's tags, and created the way extraction
		// creates a type or correspondent it has not seen.
		name, _ := raw.(string)
		id, _, err := EnsureOption(r.state.App, field.ID, r.state.Document.GetString("user"), name)
		if err != nil {
			r.fail(rule, "%s %q: %v", field.Name, name, err)
			return
		}
		r.cat.Options[id] = rules.Option{Field: field.ID, Name: name}
		raw = id
	}
	value, ok := r.fieldValue(field, raw)
	if !ok {
		r.fail(rule, "its value for %s no longer exists", field.Name)
		return
	}
	r.set(field, value)
}

// fieldValue is a stored value as the document's values hold it: a rule's
// value is already the stored form, an option or choice by id, so this checks
// the id still exists and attaches its name.
func (r *rulesRun) fieldValue(field models.CustomField, raw any) (models.FieldValue, bool) {
	v := models.FieldValue{Type: field.Type}
	s, _ := raw.(string)
	switch field.Type {
	case models.CustomFieldNumber:
		n, ok := raw.(float64)
		v.Number = n
		return v, ok
	case models.CustomFieldDate:
		v.Date = s
		return v, s != ""
	case models.CustomFieldOption:
		option, ok := r.cat.Options[s]
		v.Option, v.OptionName = s, option.Name
		return v, ok && option.Field == field.ID
	case models.CustomFieldChoice:
		i := slices.IndexFunc(field.Choices, func(c models.FieldChoice) bool { return c.ID == s })
		if i < 0 {
			return v, false
		}
		v.Choice, v.ChoiceName = s, field.Choices[i].Name
		return v, true
	}
	v.Text = s
	return v, s != ""
}

func (r *rulesRun) set(field models.CustomField, value models.FieldValue) {
	if r.doc.Values == nil {
		r.doc.Values = models.FieldValues{}
	}
	if current, ok := r.doc.Values[field.ID]; ok && current == value {
		return
	}
	r.doc.Values[field.ID] = value
	r.changed[field.ID] = true
}

// applyAIExtract fills only empty fields unless the job overwrites, as
// extraction does, so a value its owner typed survives the rules too.
func (r *rulesRun) applyAIExtract(ctx context.Context, rule rules.Rule, action rules.Action) {
	if r.extractor == nil {
		r.fail(rule, "no AI extractor is configured")
		return
	}
	if strings.TrimSpace(r.doc.Text) == "" {
		r.fail(rule, "the document has no text to read")
		return
	}
	var fields []models.CustomField
	for _, id := range action.Fields {
		field, ok := r.cat.Fields[id]
		if !ok || field.Type == models.CustomFieldOption {
			continue
		}
		if _, filled := r.doc.Values[id]; filled && !r.overwrite {
			continue
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return
	}
	aiCtx, cancel := context.WithTimeout(ctx, r.state.Cfg.OpenAITimeout)
	defer cancel()
	answers, err := r.extractor.ExtractFields(aiCtx, r.doc.Text, action.Prompt, fields)
	if err != nil {
		r.fail(rule, "AI extraction: %v", err)
		return
	}
	for _, field := range fields {
		coerced, ok := field.ValueIn(answers)
		if !ok {
			continue
		}
		if value, ok := r.fieldValue(field, coerced); ok {
			r.set(field, value)
		}
	}
}

// save writes only what the rules changed onto a fresh copy of the document:
// the job's copy is as old as the job, and the owner may have edited it while
// a model or a webhook was answering.
func (r *rulesRun) save() error {
	var writes []models.FieldWrite
	for id := range r.changed {
		field, ok := r.cat.Fields[id]
		if !ok {
			continue
		}
		writes = append(writes, models.FieldWrite{Field: field, Value: storedValue(r.doc.Values[id])})
	}
	if len(writes) == 0 && !r.changed["tags"] && !r.review {
		return nil
	}
	return r.state.App.RunInTransaction(func(txApp core.App) error {
		document, err := txApp.FindRecordById("documents", r.state.Document.Id)
		if err != nil {
			return err
		}
		if r.changed["tags"] {
			tags := retag(document.GetStringSlice("tags"), r.state.Document.GetStringSlice("tags"), r.doc.Tags)
			document.Set("tags", tags)
			r.state.Document.Set("tags", tags)
		}
		if r.review {
			document.Set("processing_status", models.DocStatusNeedsReview)
			r.state.Document.Set("processing_status", models.DocStatusNeedsReview)
		}
		if err := models.SaveFieldValues(txApp, document, writes); err != nil {
			return err
		}
		return txApp.Save(document.IgnoreUnchangedFields(true))
	})
}

// retag applies what the rules did to the tags, from before to after, onto
// current, so a tag the owner added or removed meanwhile stays that way.
func retag(current, before, after []string) []string {
	out := slices.DeleteFunc(slices.Clone(current), func(id string) bool {
		return slices.Contains(before, id) && !slices.Contains(after, id)
	})
	for _, id := range after {
		if !slices.Contains(before, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// storedValue is what SaveFieldValue takes for a value: the id for an option
// or a choice.
func storedValue(v models.FieldValue) any {
	switch v.Type {
	case models.CustomFieldNumber:
		return v.Number
	case models.CustomFieldDate:
		return v.Date
	case models.CustomFieldOption:
		return v.Option
	case models.CustomFieldChoice:
		return v.Choice
	}
	return v.Text
}

func (r *rulesRun) deliver(ctx context.Context, client *http.Client, trigger string, hook pendingWebhook) {
	body, err := json.Marshal(rules.WebhookPayload{
		Event:    rules.WebhookEvent,
		Trigger:  trigger,
		Rule:     rules.WebhookRule{ID: hook.rule.ID, Name: hook.rule.Name},
		Document: r.webhookDocument(),
	})
	if err != nil {
		r.fail(hook.rule, "webhook: %v", err)
		return
	}
	if err := rules.Deliver(ctx, client, hook.action.URL, hook.action.Secret, body); err != nil {
		r.fail(hook.rule, "%v", err)
		return
	}
	r.state.Logger.Info("webhook delivered", "rule", strutil.TruncateRunes(hook.rule.Name, 40))
}

// finalStatus is the status the job will leave the document in, not the
// processing it is in while the rules run after an edit.
func (r *rulesRun) finalStatus() string {
	if r.review {
		return models.DocStatusNeedsReview
	}
	if back := r.state.Job.GetString(models.JobReturnStatus); back != "" {
		return back
	}
	return r.state.Document.GetString("processing_status")
}

func (r *rulesRun) webhookDocument() rules.WebhookDocument {
	document := r.state.Document
	out := rules.WebhookDocument{
		ID:               document.Id,
		Title:            document.GetString("title"),
		Summary:          document.GetString("summary"),
		DocumentType:     r.doc.Values.OptionName(models.DocumentTypeFieldID),
		Correspondent:    r.doc.Values.OptionName(models.CorrespondentFieldID),
		Tags:             []string{},
		Fields:           map[string]any{},
		ProcessingStatus: r.finalStatus(),
	}
	if date := document.GetDateTime("document_date"); !date.IsZero() {
		out.DocumentDate = date.Time().Format(time.DateOnly)
	}
	for _, id := range r.doc.Tags {
		if name, ok := r.cat.Tags[id]; ok {
			out.Tags = append(out.Tags, name)
		}
	}
	for id, value := range r.doc.Values {
		if field, ok := r.cat.Fields[id]; ok && field.Type != models.CustomFieldOption {
			out.Fields[field.Name] = value.Value()
		}
	}
	return out
}
