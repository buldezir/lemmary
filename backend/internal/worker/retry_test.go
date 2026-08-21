package worker

import (
	"testing"
	"time"
)

func TestRetryDelayGrowsAndCaps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: retryBackoffBase},
		{attempts: 1, want: retryBackoffBase},
		{attempts: 2, want: 2 * retryBackoffBase},
		{attempts: 3, want: 4 * retryBackoffBase},
		{attempts: 4, want: 8 * retryBackoffBase},
		{attempts: 20, want: retryBackoffMax},
	}
	for _, tc := range cases {
		if got := RetryDelay(tc.attempts); got != tc.want {
			t.Fatalf("RetryDelay(%d)=%s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestRetryDelayNeverExceedsMax(t *testing.T) {
	t.Parallel()

	for attempts := 0; attempts < 64; attempts++ {
		if got := RetryDelay(attempts); got > retryBackoffMax {
			t.Fatalf("RetryDelay(%d)=%s exceeds cap %s", attempts, got, retryBackoffMax)
		}
	}
}

func TestTimestampAfterIsInTheFuture(t *testing.T) {
	t.Parallel()

	now := nowTimestamp()
	later := timestampAfter(time.Minute)
	if later <= now {
		t.Fatalf("expected %q to sort after %q", later, now)
	}
}

func TestProvidersReady(t *testing.T) {
	t.Parallel()

	if err := providersReady(snapshotWithProviders(nil, nil)); err == nil {
		t.Fatal("expected an error when no providers are configured")
	}
	if err := providersReady(snapshotWithProviders(stubOCR{}, nil)); err == nil {
		t.Fatal("expected an error when the AI extractor is missing")
	}
	if err := providersReady(snapshotWithProviders(stubOCR{}, stubExtractor{})); err != nil {
		t.Fatalf("expected no error with both providers, got %v", err)
	}
}
