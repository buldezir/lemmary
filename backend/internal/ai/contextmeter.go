package ai

// charsPerToken is the ratio used only when a provider reports no usage at all.
// Wrong for CJK and for code, close enough to answer the question the number is
// asked for: is this conversation near its ceiling.
const charsPerToken = 4

// TurnUsage is how much of the model's context one turn used. Peak rather than
// total, because the research loop resends a growing array every round: what
// says how close the turn came to the wall is the largest single request, not
// the sum of them. Helper completions are a separate conversation and are not
// counted here.
type TurnUsage struct {
	PeakPrompt    int  `json:"peak_prompt"`
	ContextWindow int  `json:"context_window,omitempty"`
	Estimated     bool `json:"estimated,omitempty"`
}

func (u TurnUsage) Empty() bool { return u.PeakPrompt == 0 }

// contextMeter tracks the main conversation's size across a run. It counts
// characters as they are appended so that a provider reporting no usage still
// leaves something to show, rather than a blank where the number was.
type contextMeter struct {
	chars int
	usage TurnUsage
}

func newContextMeter(window int) *contextMeter {
	return &contextMeter{usage: TurnUsage{ContextWindow: window}}
}

// grew records text appended to the conversation.
func (m *contextMeter) grew(chars ...int) {
	for _, n := range chars {
		m.chars += n
	}
}

// observe folds one completion's reported usage in and returns the turn so far.
//
// Estimated describes the number being reported, not the run: it travels with
// the peak, so a turn whose widest request the provider counted is not marked a
// guess because some smaller round went uncounted.
func (m *contextMeter) observe(u Usage) TurnUsage {
	prompt, estimated := u.Prompt, false
	if prompt <= 0 {
		prompt, estimated = m.chars/charsPerToken, true
	}
	if prompt > m.usage.PeakPrompt {
		m.usage.PeakPrompt, m.usage.Estimated = prompt, estimated
	}
	return m.usage
}

func usageEvent(u TurnUsage) ResearchEvent {
	return ResearchEvent{
		Type:          "usage",
		PromptTokens:  u.PeakPrompt,
		ContextWindow: u.ContextWindow,
		Estimated:     u.Estimated,
	}
}
