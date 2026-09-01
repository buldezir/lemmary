package ai

import "testing"

func TestContextBudgetHoldsBackTheAnswerReserve(t *testing.T) {
	t.Parallel()
	b := newContextBudget(10000)
	want := (10000 - answerReserveTokens) * charsPerToken
	if b.Remaining() != want {
		t.Fatalf("remaining = %d, want %d", b.Remaining(), want)
	}

	b.Add(want - 10)
	if b.Exhausted() {
		t.Fatal("budget reported exhausted with room left")
	}
	if b.Remaining() != 10 {
		t.Fatalf("remaining = %d, want 10", b.Remaining())
	}

	b.Add(100)
	if !b.Exhausted() {
		t.Fatal("budget should be exhausted after overspending")
	}
	if b.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0 (never negative)", b.Remaining())
	}
}

func TestContextBudgetClampsTinyWindows(t *testing.T) {
	t.Parallel()
	// A window smaller than the answer reserve would otherwise leave nothing
	// to research with — or a negative budget.
	b := newContextBudget(1)
	if b.Remaining() <= 0 {
		t.Fatalf("remaining = %d, want a usable floor", b.Remaining())
	}
	if b.Remaining() != (minContextTokens-answerReserveTokens)*charsPerToken {
		t.Fatalf("remaining = %d, want the clamped floor", b.Remaining())
	}
}

func TestContextBudgetLeftPercent(t *testing.T) {
	t.Parallel()
	b := newContextBudget(10000)
	if got := b.LeftPercent(); got != 100 {
		t.Fatalf("fresh budget = %d%%, want 100%%", got)
	}
	b.Add(b.limitChars / 2)
	if got := b.LeftPercent(); got != 50 {
		t.Fatalf("half-spent budget = %d%%, want 50%%", got)
	}
	b.Add(b.limitChars)
	if got := b.LeftPercent(); got != 0 {
		t.Fatalf("spent budget = %d%%, want 0%%", got)
	}
}

func TestContextBudgetIgnoresNegativeCharges(t *testing.T) {
	t.Parallel()
	b := newContextBudget(10000)
	before := b.Remaining()
	b.Add(-500)
	if b.Remaining() != before {
		t.Fatalf("a negative charge changed the budget: %d -> %d", before, b.Remaining())
	}
}
