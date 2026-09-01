package ai

const (
	// charsPerToken converts the configured context window into a character
	// budget. Deliberately low: archive OCR text is multilingual and tokenizes
	// worse than English prose, so under-estimating the room we have is the
	// safe direction to be wrong in.
	charsPerToken = 3

	// answerReserveTokens is held back from the window so the final answer
	// always has room, however much the research phase read.
	answerReserveTokens = 4000

	// minContextTokens guards against a context window configured so small
	// that the reserve would leave nothing to research with.
	minContextTokens = answerReserveTokens + 2000
)

// contextBudget tracks how much of the model's context window a research run
// has spent. Unlike a round or document cap it bounds the thing that actually
// breaks — the request outgrowing the window — which is why the research loop
// needs no other limit: every round appends at least an assistant message and a
// tool result, so the budget is always reached in finite time.
type contextBudget struct {
	limitChars int
	usedChars  int
}

func newContextBudget(contextTokens int) *contextBudget {
	if contextTokens < minContextTokens {
		contextTokens = minContextTokens
	}
	return &contextBudget{limitChars: (contextTokens - answerReserveTokens) * charsPerToken}
}

func (b *contextBudget) Add(chars int) {
	if chars > 0 {
		b.usedChars += chars
	}
}

func (b *contextBudget) Remaining() int {
	if remaining := b.limitChars - b.usedChars; remaining > 0 {
		return remaining
	}
	return 0
}

func (b *contextBudget) Exhausted() bool {
	return b.Remaining() <= 0
}

// LeftPercent is what the UI shows as the run's remaining headroom.
func (b *contextBudget) LeftPercent() int {
	if b.limitChars <= 0 {
		return 0
	}
	return b.Remaining() * 100 / b.limitChars
}
