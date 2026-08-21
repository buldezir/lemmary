package worker

import (
	"time"
)

const pbTimeLayout = "2006-01-02 15:04:05.000Z"

const (
	// retryBackoffBase is the delay before the first retry of a failed step.
	retryBackoffBase = 5 * time.Second
	// retryBackoffMax caps the exponential growth so a job still retries within
	// a useful window even after several attempts.
	retryBackoffMax = 5 * time.Minute
)

func nowTimestamp() string {
	return time.Now().UTC().Format(pbTimeLayout)
}

func timestampAfter(d time.Duration) string {
	return time.Now().UTC().Add(d).Format(pbTimeLayout)
}

// RetryDelay returns how long to wait before retrying a step that has already
// been attempted `attempts` times. It doubles per attempt up to retryBackoffMax,
// so a rate-limited provider is not hammered through every retry in milliseconds.
func RetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		return retryBackoffBase
	}
	delay := retryBackoffBase
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= retryBackoffMax {
			return retryBackoffMax
		}
	}
	return delay
}
