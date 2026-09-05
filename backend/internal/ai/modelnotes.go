package ai

import (
	"strings"
	"sync"
)

// What this process has learned about a model while talking to it. A provider
// refusing a request the same way twice is worth remembering: the discovery
// then costs a rejected request per model per process rather than one per call.
//
// Every note is keyed by endpoint as well as model. A model string means
// nothing on its own -- an instance can bind several providers at once, each
// with its own base URL (see internal/appapi/providers.go), and "gpt-5.6-luna"
// behind OpenCode Zen is not the same endpoint as the same name at
// api.openai.com. Keying on the name alone would let one gateway's quirk
// follow the model onto another.
type modelNote struct {
	baseURL string
	model   string
}

func noteFor(baseURL, model string) modelNote {
	return modelNote{
		baseURL: strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/")),
		model:   strings.ToLower(strings.TrimSpace(model)),
	}
}

func (n modelNote) empty() bool { return n.model == "" }

var (
	// Models that would not take function tools alongside their default
	// reasoning_effort, and had to be pinned to "none".
	noReasoningEffortNotes sync.Map
	// Models this endpoint does not serve on /chat/completions at all.
	responsesAPINotes sync.Map
	// Models /chat/completions has answered at least once, which makes a later
	// refusal a transient failure rather than a wrong endpoint.
	chatCompletionsWorked sync.Map
	// How many times /chat/completions has refused a model with a status that
	// could mean either.
	ambiguousRefusals sync.Map
)

func rememberNoReasoningEffort(baseURL, model string) { store(&noReasoningEffortNotes, baseURL, model) }
func needsNoReasoningEffort(baseURL, model string) bool {
	return loaded(&noReasoningEffortNotes, baseURL, model)
}

func rememberResponsesAPI(baseURL, model string) { store(&responsesAPINotes, baseURL, model) }
func needsResponsesAPI(baseURL, model string) bool {
	return loaded(&responsesAPINotes, baseURL, model)
}

// rememberChatCompletionsWorked records a completion this endpoint served, so a
// later failure is read as the provider having a bad minute rather than as a
// model that lives somewhere else.
func rememberChatCompletionsWorked(baseURL, model string) {
	store(&chatCompletionsWorked, baseURL, model)
}
func chatCompletionsHasWorked(baseURL, model string) bool {
	return loaded(&chatCompletionsWorked, baseURL, model)
}

// countAmbiguousRefusal returns how many times this endpoint has now refused
// the model with a status that does not say which of the two it means.
func countAmbiguousRefusal(baseURL, model string) int {
	note := noteFor(baseURL, model)
	if note.empty() {
		return 0
	}
	count, _ := ambiguousRefusals.Load(note)
	n, _ := count.(int)
	n++
	ambiguousRefusals.Store(note, n)
	return n
}

func store(m *sync.Map, baseURL, model string) {
	if note := noteFor(baseURL, model); !note.empty() {
		m.Store(note, struct{}{})
	}
}

func loaded(m *sync.Map, baseURL, model string) bool {
	note := noteFor(baseURL, model)
	if note.empty() {
		return false
	}
	_, ok := m.Load(note)
	return ok
}

// resetModelNotes clears everything this process has learned. Tests only.
func resetModelNotes() {
	for _, m := range []*sync.Map{
		&noReasoningEffortNotes, &responsesAPINotes, &chatCompletionsWorked, &ambiguousRefusals,
	} {
		m.Range(func(k, _ any) bool {
			m.Delete(k)
			return true
		})
	}
}
