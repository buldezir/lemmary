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
	// Models this endpoint would not serve on /chat/completions with tools
	// present, and that the Responses API served instead.
	responsesAPINotes sync.Map
)

func rememberNoReasoningEffort(baseURL, model string) { store(&noReasoningEffortNotes, baseURL, model) }
func needsNoReasoningEffort(baseURL, model string) bool {
	return loaded(&noReasoningEffortNotes, baseURL, model)
}

func rememberResponsesAPI(baseURL, model string) { store(&responsesAPINotes, baseURL, model) }
func needsResponsesAPI(baseURL, model string) bool {
	return loaded(&responsesAPINotes, baseURL, model)
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
	for _, m := range []*sync.Map{&noReasoningEffortNotes, &responsesAPINotes} {
		m.Range(func(k, _ any) bool {
			m.Delete(k)
			return true
		})
	}
}
