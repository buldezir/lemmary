package rules

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/netguard"
)

const (
	MaxConditions = 20
	MaxActions    = 20
	// Bounds a condition's text, a pattern and a webhook's URL and secret.
	maxValueRunes = 500
	// The AI instructions are a prompt, so they get room for a paragraph or two.
	MaxPromptRunes = 4000
)

// Catalog is what a rule's ids may point at: every field, and the owner's
// tags and options, each with its name.
type Catalog struct {
	Fields  map[string]models.CustomField
	Tags    map[string]string
	Options map[string]Option
	// named is set while validating a global rule, which names tags and
	// options instead of pointing at one user's.
	named bool
}

// noMatch is an id no tag or option has. A global rule naming a tag its owner
// lacks asks about nothing: "includes" fails and "does not include" holds.
const noMatch = "\x00none"

// IDByName is the owner's tag (field "") or option of field with that name:
// the name ignoring case, else the models.NameKey match extraction and a
// global set_field resolve names by, or "" when there is none.
func (cat Catalog) IDByName(field, name string) string {
	names := cat.Tags
	if field != "" {
		names = map[string]string{}
		for id, option := range cat.Options {
			if option.Field == field {
				names[id] = option.Name
			}
		}
	}
	name = strings.TrimSpace(name)
	key := models.NameKey(name)
	var keyed []string
	for id, candidate := range names {
		if strings.EqualFold(strings.TrimSpace(candidate), name) {
			return id
		}
		if key != "" && models.NameKey(candidate) == key {
			keyed = append(keyed, id)
		}
	}
	if len(keyed) == 0 {
		return ""
	}
	slices.Sort(keyed)
	return keyed[0]
}

// Localize points a global rule's conditions at the owner's tags and options by
// name. Its actions keep their names: the worker resolves them as it runs, and
// creates what the owner lacks.
func (r Rule) Localize(cat Catalog) Rule {
	if !r.Global {
		return r
	}
	conditions := make([]Condition, len(r.Conditions))
	for i, c := range r.Conditions {
		conditions[i] = c
		kind, _ := subjectKind(c.Subject, cat.Fields)
		if (kind != kindTag && kind != kindOption) || !needsValue(c.Op) {
			continue
		}
		field := c.Subject
		if kind == kindTag {
			field = ""
		}
		if id := cat.IDByName(field, stringValue(c.Value)); id != "" {
			conditions[i].Value = id
		} else {
			conditions[i].Value = noMatch
		}
	}
	r.Conditions = conditions
	return r
}

func needsValue(op string) bool {
	return op != OpIsSet && op != OpIsNotSet
}

type Option struct {
	Field string
	Name  string
}

func LoadCatalog(app core.App, userID string) (Catalog, error) {
	defined, err := models.LoadCustomFields(app)
	if err != nil {
		return Catalog{}, err
	}
	cat := Catalog{
		Fields:  map[string]models.CustomField{},
		Tags:    map[string]string{},
		Options: map[string]Option{},
	}
	for _, field := range append(defined, models.PredefinedCustomFields...) {
		cat.Fields[field.ID] = field
	}
	tags, err := app.FindAllRecords("tags", dbx.HashExp{"user": userID})
	if err != nil {
		return Catalog{}, err
	}
	for _, tag := range tags {
		cat.Tags[tag.Id] = tag.GetString("name")
	}
	options, err := app.FindAllRecords(models.CustomFieldOptionsCollection, dbx.HashExp{"user": userID})
	if err != nil {
		return Catalog{}, err
	}
	for _, option := range options {
		cat.Options[option.Id] = Option{Field: option.GetString("field"), Name: option.GetString("name")}
	}
	return cat, nil
}

// Validate checks a rule as its owner saved it, so the worker never meets a
// condition it cannot read or an action it cannot run. Ids are checked against
// the catalog now; one deleted later is the worker's to skip.
func Validate(r Rule, cat Catalog) error {
	cat.named = r.Global
	if len(r.Actions) == 0 {
		return i18n.Errorf("A rule needs at least one action.")
	}
	if len(r.Conditions) > MaxConditions || len(r.Actions) > MaxActions {
		return i18n.Errorf("A rule has at most %d conditions and %d actions.", MaxConditions, MaxActions)
	}
	for _, c := range r.Conditions {
		if err := validateCondition(c, cat); err != nil {
			return err
		}
	}
	for _, a := range r.Actions {
		if err := validateAction(a, cat); err != nil {
			return err
		}
	}
	return nil
}

func validateCondition(c Condition, cat Catalog) error {
	kind, ok := subjectKind(c.Subject, cat.Fields)
	if !ok {
		return i18n.Errorf("A condition refers to a field, tag or value that does not exist.")
	}
	if !slices.Contains(opsByKind[kind], c.Op) {
		return i18n.Errorf("A condition's comparison does not fit its field.")
	}
	if !needsValue(c.Op) {
		return nil
	}
	if c.Op == OpMatches {
		if _, err := regexp.Compile(stringValue(c.Value)); err != nil {
			return i18n.Errorf("The pattern %q is not a valid regular expression.", stringValue(c.Value))
		}
	}
	if share, ok := c.Value.(float64); c.Subject == SubjectConfidence && ok && (share < 0 || share > 1) {
		return i18n.Errorf("A condition's value does not fit its field.")
	}
	return validateValue(kind, c.Subject, c.Value, cat, false)
}

// validateValue checks a condition's value, or with forAction a set_field
// action's, for the subject's kind.
func validateValue(kind, subject string, value any, cat Catalog, forAction bool) error {
	invalid := i18n.Errorf("A condition's value does not fit its field.")
	missing := i18n.Errorf("A condition refers to a field, tag or value that does not exist.")
	if forAction {
		invalid = i18n.Errorf("An action's value does not fit its field.")
		missing = i18n.Errorf("An action refers to a field, tag or value that does not exist.")
	}
	s, isString := value.(string)
	if cat.named && (kind == kindOption || kind == kindTag) {
		kind = kindText
	}
	switch kind {
	case kindNumber:
		if _, ok := value.(float64); !ok {
			return invalid
		}
	case kindDate:
		if date, ok := models.NormalizeDocumentDate(s); !isString || s == "" || !ok || date != s {
			return invalid
		}
	case kindOption:
		if option, ok := cat.Options[s]; !ok || option.Field != subject {
			return missing
		}
	case kindChoice:
		if !slices.ContainsFunc(cat.Fields[subject].Choices, func(c models.FieldChoice) bool { return c.ID == s }) {
			return missing
		}
	case kindTag:
		if _, ok := cat.Tags[s]; !ok {
			return missing
		}
	default:
		if !isString || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > maxValueRunes {
			return invalid
		}
	}
	return nil
}

func validateAction(a Action, cat Catalog) error {
	switch a.Type {
	case ActionAddTag, ActionRemoveTag:
		return validateValue(kindTag, "", a.Value, cat, true)
	case ActionSetField:
		field, ok := cat.Fields[a.Field]
		if !ok {
			return i18n.Errorf("An action refers to a field, tag or value that does not exist.")
		}
		kind, _ := subjectKind(field.ID, cat.Fields)
		return validateValue(kind, field.ID, a.Value, cat, true)
	case ActionRequireReview:
		return nil
	case ActionAIExtract:
		return validateAIExtract(a, cat)
	case ActionWebhook:
		return validateWebhook(a)
	}
	return i18n.Errorf("An action has an unknown type.")
}

func validateAIExtract(a Action, cat Catalog) error {
	if len(a.Fields) == 0 {
		return i18n.Errorf("An AI action needs at least one field to fill.")
	}
	if utf8.RuneCountInString(a.Prompt) > MaxPromptRunes {
		return i18n.Errorf("An AI action's instructions are limited to %d characters.", MaxPromptRunes)
	}
	for _, id := range a.Fields {
		// The predefined fields are extraction's own answer, not one an
		// instruction can be scoped to.
		if _, ok := cat.Fields[id]; !ok || models.IsPredefinedField(id) {
			return i18n.Errorf("An action refers to a field, tag or value that does not exist.")
		}
	}
	return nil
}

func validateWebhook(a Action) error {
	raw := strings.TrimSpace(a.URL)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") || utf8.RuneCountInString(raw) > maxValueRunes {
		return i18n.Errorf("A webhook needs an http or https URL.")
	}
	if utf8.RuneCountInString(a.Secret) > maxValueRunes {
		return i18n.Errorf("A webhook's secret is limited to %d characters.", maxValueRunes)
	}
	if err := netguard.CheckURL(raw); err != nil {
		if refusal, ok := errors.AsType[*i18n.Error](err); ok {
			return refusal
		}
		return i18n.Errorf("A webhook needs an http or https URL.")
	}
	return nil
}
