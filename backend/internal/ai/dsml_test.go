package ai

import "testing"

func TestParseDSMLToolCallsOfficial(t *testing.T) {
	content := `<｜DSML｜tool_calls>
<｜DSML｜invoke name="search_documents">
<｜DSML｜parameter name="query" string="true">пенсия</｜DSML｜parameter>
<｜DSML｜parameter name="limit" string="false">10</｜DSML｜parameter>
</｜DSML｜invoke>
<｜DSML｜invoke name="search_documents">
<｜DSML｜parameter name="query" string="true">pension</｜DSML｜parameter>
<｜DSML｜parameter name="limit" string="false">10</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜tool_calls>`

	calls := parseDSMLToolCalls(content)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "search_documents" {
		t.Fatalf("name: %q", calls[0].Name)
	}
	if !stringsContains(calls[0].Arguments, "пенсия") {
		t.Fatalf("args0: %s", calls[0].Arguments)
	}
	if !stringsContains(calls[1].Arguments, "pension") {
		t.Fatalf("args1: %s", calls[1].Arguments)
	}
}

func TestParseDSMLToolCallsTriplePipe(t *testing.T) {
	// Format sometimes seen when special DeepSeek tokens are decoded poorly.
	content := `<|||DSML|||tool_calls> <|||DSML|||invoke name="search_documents"> <|||DSML|||parameter name="query" string="true">пенс</|||DSML|||parameter> <|||DSML|||parameter name="limit" string="false">10</|||DSML|||parameter> </|||DSML|||invoke> <|||DSML|||invoke name="search_documents"> <|||DSML|||parameter name="query" string="true">pensi</|||DSML|||parameter> <|||DSML|||parameter name="limit" string="false">10</|||DSML|||parameter> </|||DSML|||invoke> </|||DSML|||tool_calls>`

	calls := parseDSMLToolCalls(content)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if !stringsContains(calls[0].Arguments, "пенс") {
		t.Fatalf("args0: %s", calls[0].Arguments)
	}
	if !stringsContains(calls[1].Arguments, "pensi") {
		t.Fatalf("args1: %s", calls[1].Arguments)
	}
}

func TestParseDSMLToolCallsPipeFallback(t *testing.T) {
	content := `<|DSML|invoke name="search_documents"><|DSML|parameter name="query" string="true">пенс</|DSML|parameter><|DSML|parameter name="limit" string="false">10</|DSML|parameter></|DSML|invoke>`

	calls := parseDSMLToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d (%+v)", len(calls), calls)
	}
	if !stringsContains(calls[0].Arguments, "пенс") {
		t.Fatalf("args: %s", calls[0].Arguments)
	}
}

func TestStripDSMLMarkup(t *testing.T) {
	content := `Here is what I found.
<｜DSML｜tool_calls>
<｜DSML｜invoke name="search_documents">
<｜DSML｜parameter name="query" string="true">x</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜tool_calls>`
	got := stripDSMLMarkup(content)
	if stringsContains(got, "DSML") {
		t.Fatalf("still has DSML: %q", got)
	}
	if got != "Here is what I found." {
		t.Fatalf("got %q", got)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestParseDSMLParametersArrayWithoutStringAttr(t *testing.T) {
	body := `<｜DSML｜parameter name="tags">["invoice","tax"]</｜DSML｜parameter>`
	got := parseDSMLParameters(body)
	if got != `{"tags":["invoice","tax"]}` {
		t.Fatalf("parseDSMLParameters = %s", got)
	}
}
