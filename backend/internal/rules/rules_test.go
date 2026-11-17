package rules

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lemmary/backend/internal/models"
)

var testFields = map[string]models.CustomField{
	"famount":                   {ID: "famount", Name: "Amount", Type: models.CustomFieldNumber},
	"fdue":                      {ID: "fdue", Name: "Due date", Type: models.CustomFieldDate},
	"fnumber":                   {ID: "fnumber", Name: "Invoice number", Type: models.CustomFieldText},
	"fstatus":                   {ID: "fstatus", Name: "Status", Type: models.CustomFieldChoice, Choices: []models.FieldChoice{{ID: "cpaid", Name: "Paid"}}},
	models.DocumentTypeFieldID:  models.DocumentTypeField,
	models.CorrespondentFieldID: models.CorrespondentField,
}

func testCatalog() Catalog {
	return Catalog{
		Fields: testFields,
		Tags:   map[string]string{"tpay": "to pay"},
		Options: map[string]Option{
			"oinvoice": {Field: models.DocumentTypeFieldID, Name: "Invoice"},
			"oacme":    {Field: models.CorrespondentFieldID, Name: "ACME"},
		},
	}
}

func TestConditionsHold(t *testing.T) {
	t.Parallel()
	doc := &Document{
		Title:      "Invoice R-1 from ACME",
		Text:       "Total 129,90 EUR\nIBAN DE89",
		Confidence: 0.4,
		Tags:       []string{"tpay"},
		Values: models.FieldValues{
			"famount":                  {Type: models.CustomFieldNumber, Number: 129.9},
			"fdue":                     {Type: models.CustomFieldDate, Date: "2026-10-31"},
			"fstatus":                  {Type: models.CustomFieldChoice, Choice: "cpaid"},
			models.DocumentTypeFieldID: {Type: models.CustomFieldOption, Option: "oinvoice"},
		},
	}
	cases := []struct {
		c    Condition
		want bool
	}{
		{Condition{Subject: SubjectTitle, Op: OpContains, Value: "acme"}, true},
		{Condition{Subject: SubjectTitle, Op: OpNotContains, Value: "acme"}, false},
		{Condition{Subject: SubjectText, Op: OpMatches, Value: `IBAN DE\d+`}, true},
		{Condition{Subject: SubjectText, Op: OpMatches, Value: `^IBAN`}, false},
		{Condition{Subject: SubjectConfidence, Op: OpLess, Value: 0.5}, true},
		{Condition{Subject: SubjectTag, Op: OpIs, Value: "tpay"}, true},
		{Condition{Subject: SubjectTag, Op: OpIsNot, Value: "tpay"}, false},
		{Condition{Subject: "famount", Op: OpGreater, Value: 100.0}, true},
		{Condition{Subject: "famount", Op: OpLess, Value: 100.0}, false},
		{Condition{Subject: "famount", Op: OpIs, Value: 129.9}, true},
		{Condition{Subject: "fdue", Op: OpLess, Value: "2026-11-01"}, true},
		{Condition{Subject: "fdue", Op: OpGreater, Value: "2026-11-01"}, false},
		{Condition{Subject: "fnumber", Op: OpIsNotSet}, true},
		{Condition{Subject: "fnumber", Op: OpContains, Value: "R"}, false},
		{Condition{Subject: "fstatus", Op: OpIs, Value: "cpaid"}, true},
		{Condition{Subject: models.DocumentTypeFieldID, Op: OpIs, Value: "oinvoice"}, true},
		{Condition{Subject: models.DocumentTypeFieldID, Op: OpIsNot, Value: "oinvoice"}, false},
		// No correspondent at all is not ACME, and not set.
		{Condition{Subject: models.CorrespondentFieldID, Op: OpIsNot, Value: "oacme"}, true},
		{Condition{Subject: models.CorrespondentFieldID, Op: OpIsSet}, false},
		// A field deleted since never matches.
		{Condition{Subject: "fgone", Op: OpIsNotSet}, false},
	}
	for _, tc := range cases {
		if got := tc.c.holds(doc, testFields); got != tc.want {
			t.Errorf("%+v holds = %v, want %v", tc.c, got, tc.want)
		}
	}

	rule := Rule{Conditions: []Condition{cases[0].c, cases[1].c}}
	if ok, _ := rule.Matches(doc, testFields, nil); ok {
		t.Fatal("a rule matches only when every condition holds")
	}
	if ok, _ := (Rule{}).Matches(doc, testFields, nil); !ok {
		t.Fatal("a rule without conditions always matches")
	}
}

// An AI question costs a request, so it is asked only once every other
// condition holds, wherever it sits; is wants yes and is_not wants no.
func TestAIQuestionsAreAskedLast(t *testing.T) {
	t.Parallel()
	doc := &Document{Title: "Reminder from ACME"}
	var asked []string
	ask := func(question string) (bool, error) {
		asked = append(asked, question)
		return question == "Is this a final reminder?", nil
	}
	title := func(word string) Condition { return Condition{Subject: SubjectTitle, Op: OpContains, Value: word} }
	question := func(op, q string) Condition { return Condition{Subject: SubjectAI, Op: op, Value: q} }

	rule := Rule{Conditions: []Condition{question(OpIs, "Is this a final reminder?"), title("invoice")}}
	if ok, err := rule.Matches(doc, testFields, ask); ok || err != nil || len(asked) != 0 {
		t.Fatalf("matched %v, err %v, asked %v: the cheap condition failing must spare the question", ok, err, asked)
	}

	rule = Rule{Conditions: []Condition{question(OpIs, "Is this a final reminder?"), question(OpIsNot, "Is it paid?"), title("acme")}}
	if ok, err := rule.Matches(doc, testFields, ask); !ok || err != nil {
		t.Fatalf("matched %v, err %v", ok, err)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %v", asked)
	}

	failing := func(string) (bool, error) { return false, errors.New("model down") }
	if ok, err := rule.Matches(doc, testFields, failing); ok || err == nil {
		t.Fatalf("an unanswered question matched: %v %v", ok, err)
	}
	if ok, err := rule.Matches(doc, testFields, nil); ok || err == nil {
		t.Fatalf("a question without an AI matched: %v %v", ok, err)
	}

	cat := testCatalog()
	if err := Validate(Rule{Conditions: []Condition{question(OpIs, "")}, Actions: []Action{{Type: ActionRequireReview}}}, cat); err == nil {
		t.Fatal("an empty question was accepted")
	}
	if err := Validate(Rule{Conditions: []Condition{question(OpContains, "x")}, Actions: []Action{{Type: ActionRequireReview}}}, cat); err == nil {
		t.Fatal("a text comparison on a question was accepted")
	}
}

func TestValidate(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")
	cat := testCatalog()
	tag := Action{Type: ActionAddTag, Value: "tpay"}
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"valid", Rule{
			Conditions: []Condition{
				{Subject: models.DocumentTypeFieldID, Op: OpIs, Value: "oinvoice"},
				{Subject: "famount", Op: OpGreater, Value: 100.0},
				{Subject: "fdue", Op: OpIsSet},
				{Subject: SubjectText, Op: OpMatches, Value: `(?i)mahnung`},
			},
			Actions: []Action{
				tag,
				{Type: ActionSetField, Field: "fstatus", Value: "cpaid"},
				{Type: ActionSetField, Field: models.CorrespondentFieldID, Value: "oacme"},
				{Type: ActionRequireReview},
				{Type: ActionAIExtract, Prompt: "The amount is the gross total.", Fields: []string{"famount", "fdue"}},
				{Type: ActionWebhook, URL: "https://hooks.example.com/x", Secret: "s"},
			},
		}, ""},
		{"no action", Rule{}, "at least one action"},
		{"unknown subject", Rule{Conditions: []Condition{{Subject: "fgone", Op: OpIsSet}}, Actions: []Action{tag}}, "does not exist"},
		{"op for another kind", Rule{Conditions: []Condition{{Subject: "famount", Op: OpContains, Value: "1"}}, Actions: []Action{tag}}, "comparison"},
		{"string for a number", Rule{Conditions: []Condition{{Subject: "famount", Op: OpGreater, Value: "100"}}, Actions: []Action{tag}}, "value does not fit"},
		{"confidence past 1", Rule{Conditions: []Condition{{Subject: SubjectConfidence, Op: OpLess, Value: 5.0}}, Actions: []Action{tag}}, "value does not fit"},
		{"partial date", Rule{Conditions: []Condition{{Subject: "fdue", Op: OpLess, Value: "2026-10"}}, Actions: []Action{tag}}, "value does not fit"},
		{"bad regexp", Rule{Conditions: []Condition{{Subject: SubjectText, Op: OpMatches, Value: "(("}}, Actions: []Action{tag}}, "regular expression"},
		{"someone else's option", Rule{Conditions: []Condition{{Subject: models.DocumentTypeFieldID, Op: OpIs, Value: "oacme"}}, Actions: []Action{tag}}, "does not exist"},
		{"unknown tag", Rule{Actions: []Action{{Type: ActionAddTag, Value: "tother"}}}, "does not exist"},
		{"unknown choice", Rule{Actions: []Action{{Type: ActionSetField, Field: "fstatus", Value: "Paid"}}}, "does not exist"},
		{"AI without fields", Rule{Actions: []Action{{Type: ActionAIExtract, Prompt: "x"}}}, "at least one field"},
		{"AI into an option field", Rule{Actions: []Action{{Type: ActionAIExtract, Fields: []string{models.DocumentTypeFieldID}}}}, "does not exist"},
		{"webhook scheme", Rule{Actions: []Action{{Type: ActionWebhook, URL: "ftp://example.com"}}}, "http or https"},
		{"webhook private", Rule{Actions: []Action{{Type: ActionWebhook, URL: "http://10.0.0.5/hook"}}}, "private network"},
		{"webhook metadata", Rule{Actions: []Action{{Type: ActionWebhook, URL: "http://169.254.169.254/"}}}, "never allowed"},
		{"unknown action", Rule{Actions: []Action{{Type: "delete"}}}, "unknown type"},
	}
	for _, tc := range cases {
		err := Validate(tc.rule, cat)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// A global rule names tags and options, so it reads the same for every owner:
// each owner's ids stand in for the names, and a name the owner lacks matches
// nothing.
func TestGlobalRulesNameTagsAndOptions(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	rule := Rule{Global: true, Conditions: []Condition{
		{Subject: SubjectTag, Op: OpIs, Value: " TO PAY "},
		{Subject: models.DocumentTypeFieldID, Op: OpIs, Value: "invoice"},
		{Subject: SubjectTag, Op: OpIsNot, Value: "archived"},
	}, Actions: []Action{{Type: ActionAddTag, Value: "brand new"}}}

	doc := &Document{Tags: []string{"tpay"}, Values: models.FieldValues{
		models.DocumentTypeFieldID: {Type: models.CustomFieldOption, Option: "oinvoice"},
	}}
	if ok, _ := rule.Localize(cat).Matches(doc, cat.Fields, nil); !ok {
		t.Fatalf("localized = %+v", rule.Localize(cat).Conditions)
	}
	if ok, _ := rule.Matches(doc, cat.Fields, nil); ok {
		t.Fatal("unlocalized, a name is no id")
	}

	// Names need not exist anywhere yet; the worker creates them per owner.
	if err := Validate(rule, Catalog{Fields: testFields}); err != nil {
		t.Fatalf("a global rule naming new tags was refused: %v", err)
	}
	// The same rule as somebody's own points at nothing.
	rule.Global = false
	if err := Validate(rule, cat); err == nil {
		t.Fatal("a user's rule may not name a tag")
	}
}

func TestDeliverSignsAndWantsA2xx(t *testing.T) {
	t.Parallel()
	var signature, body string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, signature = string(raw), r.Header.Get(SignatureHeader)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	if err := Deliver(context.Background(), srv.Client(), srv.URL+"/hook?token=abc", "secret", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if body != `{"a":1}` || signature != Sign("secret", []byte(`{"a":1}`)) || !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("body %q, signature %q", body, signature)
	}

	status = http.StatusInternalServerError
	err := Deliver(context.Background(), srv.Client(), srv.URL+"/hook?token=abc", "", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "answered 500") {
		t.Fatalf("a 500 must fail the delivery, got %v", err)
	}
	if strings.Contains(err.Error(), "token=abc") {
		t.Fatalf("the error leaks the URL's query: %v", err)
	}
}
