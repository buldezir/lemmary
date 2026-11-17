// Package rules is the if-then automation a user sets up for their own
// documents: when a document is processed or edited, every enabled rule whose
// conditions all hold runs its actions, in the rules' order. One pass: an action
// that changes the document is seen by the rules after it, but never makes a
// rule run twice.
package rules

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

const Collection = "rules"

// Triggers. Processed runs after AI extraction applied its metadata, a
// reprocess included; edited after the owner changed a document's tags or
// fields.
const (
	TriggerProcessed = "processed"
	TriggerEdited    = "edited"
)

// Subjects a condition reads, besides a field id.
const (
	SubjectTitle      = "title"
	SubjectText       = "text"
	SubjectTag        = "tag"
	SubjectConfidence = "confidence"
)

const (
	OpIs          = "is"
	OpIsNot       = "is_not"
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpMatches     = "matches"
	OpGreater     = "gt"
	OpLess        = "lt"
	OpIsSet       = "is_set"
	OpIsNotSet    = "is_not_set"
)

const (
	ActionAddTag        = "add_tag"
	ActionRemoveTag     = "remove_tag"
	ActionSetField      = "set_field"
	ActionRequireReview = "require_review"
	ActionAIExtract     = "ai_extract"
	ActionWebhook       = "webhook"
)

// Kinds group subjects by the operators and values they take.
const (
	kindText   = "text"
	kindNumber = "number"
	kindDate   = "date"
	kindOption = "option"
	kindChoice = "choice"
	kindTag    = "tag"
)

var opsByKind = map[string][]string{
	kindText:   {OpContains, OpNotContains, OpMatches, OpIsSet, OpIsNotSet},
	kindNumber: {OpIs, OpGreater, OpLess, OpIsSet, OpIsNotSet},
	kindDate:   {OpIs, OpGreater, OpLess, OpIsSet, OpIsNotSet},
	kindOption: {OpIs, OpIsNot, OpIsSet, OpIsNotSet},
	kindChoice: {OpIs, OpIsNot, OpIsSet, OpIsNotSet},
	kindTag:    {OpIs, OpIsNot},
}

type Condition struct {
	// A field id, or one of the Subject constants.
	Subject string `json:"subject"`
	Op      string `json:"op"`
	// A string for text, date, option (its id), choice (its id) and tag (its
	// id); a number for a number. Absent for is_set and is_not_set.
	Value any `json:"value,omitempty"`
}

// Action carries the parameters of every type; each type reads its own.
type Action struct {
	Type string `json:"type"`
	// add_tag, remove_tag: the tag id. set_field: the value, typed as a
	// condition's is.
	Value any `json:"value,omitempty"`
	// set_field: the field id.
	Field string `json:"field,omitempty"`
	// ai_extract: the instructions and the field ids to fill.
	Prompt string   `json:"prompt,omitempty"`
	Fields []string `json:"fields,omitempty"`
	// webhook: where to POST, and the key to sign the body with, if any.
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"`
}

type Rule struct {
	ID   string
	Name string
	// Global is an admin's rule, stored without a user, that runs on every
	// user's documents. Tags and options belong to each user, so a global rule
	// names them rather than pointing at one user's ids; see Localize.
	Global     bool
	Triggers   []string
	Conditions []Condition
	Actions    []Action
}

func FromRecord(record *core.Record) (Rule, error) {
	rule := Rule{
		ID:       record.Id,
		Name:     record.GetString("name"),
		Global:   record.GetString("user") == "",
		Triggers: record.GetStringSlice("triggers"),
	}
	if err := record.UnmarshalJSONField("conditions", &rule.Conditions); err != nil {
		return Rule{}, fmt.Errorf("rule %s conditions: %w", record.Id, err)
	}
	if err := record.UnmarshalJSONField("actions", &rule.Actions); err != nil {
		return Rule{}, fmt.Errorf("rule %s actions: %w", record.Id, err)
	}
	return rule, nil
}

// Load is the enabled rules for trigger that apply to the owner's documents,
// in the order they run: the global ones first, then the owner's own, so an
// owner's rule sees what the admin's did.
func Load(app core.App, userID, trigger string) ([]Rule, error) {
	records, err := app.FindRecordsByFilter(Collection, "(user = {:user} || user = '') && enabled = true",
		"user,position,created,id", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return nil, err
	}
	var out []Rule
	for _, record := range records {
		if !slices.Contains(record.GetStringSlice("triggers"), trigger) {
			continue
		}
		rule, err := FromRecord(record)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// Document is what conditions read and actions change.
type Document struct {
	Title      string
	Text       string
	Confidence float64
	Tags       []string
	Values     models.FieldValues
}

// Matches is true when every condition holds; a rule without any always does.
func (r Rule) Matches(d *Document, fields map[string]models.CustomField) bool {
	for _, c := range r.Conditions {
		if !c.holds(d, fields) {
			return false
		}
	}
	return true
}

func subjectKind(subject string, fields map[string]models.CustomField) (string, bool) {
	switch subject {
	case SubjectTitle, SubjectText:
		return kindText, true
	case SubjectTag:
		return kindTag, true
	case SubjectConfidence:
		return kindNumber, true
	}
	field, ok := fields[subject]
	if !ok {
		return "", false
	}
	switch field.Type {
	case models.CustomFieldNumber:
		return kindNumber, true
	case models.CustomFieldDate:
		return kindDate, true
	case models.CustomFieldOption:
		return kindOption, true
	case models.CustomFieldChoice:
		return kindChoice, true
	}
	return kindText, true
}

func (c Condition) holds(d *Document, fields map[string]models.CustomField) bool {
	kind, ok := subjectKind(c.Subject, fields)
	if !ok {
		return false
	}
	if kind == kindTag {
		has := slices.Contains(d.Tags, stringValue(c.Value))
		return has == (c.Op == OpIs)
	}
	value, set := d.subjectValue(c.Subject, kind)
	switch c.Op {
	case OpIsSet:
		return set
	case OpIsNotSet:
		return !set
	}
	switch kind {
	case kindText:
		return textHolds(c, value.(string))
	case kindNumber:
		want, _ := c.Value.(float64)
		return set && compare(value.(float64), want, c.Op)
	case kindDate:
		return set && compare(strings.Compare(value.(string), stringValue(c.Value)), 0, c.Op)
	}
	// Option and choice: is_not holds for a document without a value too.
	same := set && value.(string) == stringValue(c.Value)
	return same == (c.Op == OpIs)
}

// subjectValue is the subject's value in the type its kind compares: a string
// for text, date, option and choice (the ids), a float64 for a number.
func (d *Document) subjectValue(subject, kind string) (any, bool) {
	switch subject {
	case SubjectTitle:
		return d.Title, strings.TrimSpace(d.Title) != ""
	case SubjectText:
		return d.Text, strings.TrimSpace(d.Text) != ""
	case SubjectConfidence:
		// Zero is a document no model rated.
		return d.Confidence, d.Confidence > 0
	}
	v, set := d.Values[subject]
	switch kind {
	case kindNumber:
		return v.Number, set
	case kindDate:
		return v.Date, set
	case kindOption:
		return v.Option, set
	case kindChoice:
		return v.Choice, set
	}
	return v.Text, set
}

func textHolds(c Condition, value string) bool {
	switch c.Op {
	case OpMatches:
		re, err := regexp.Compile(stringValue(c.Value))
		return err == nil && re.MatchString(value)
	case OpNotContains:
		return !strings.Contains(strings.ToLower(value), strings.ToLower(stringValue(c.Value)))
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(stringValue(c.Value)))
}

func compare[T int | float64](got, want T, op string) bool {
	switch op {
	case OpGreater:
		return got > want
	case OpLess:
		return got < want
	}
	return got == want
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
