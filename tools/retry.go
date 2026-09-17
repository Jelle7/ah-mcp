package tools

import (
	"context"
	"strings"
	"time"
)

const retryMaxAttempts = 3

// retryBaseDelay is the first backoff step, doubled on each further attempt.
// A variable so tests can run without real sleeps.
var retryBaseDelay = time.Second

// withRetry retries fn when AH returns a rate-limit error (429), backing off
// 1s then 2s between attempts. Any other error is returned immediately without
// retrying: only a 429 is guaranteed not to have been applied, so retrying
// anything else risks repeating a write.
func withRetry(ctx context.Context, tool string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !isRateLimited(lastErr) {
			return lastErr
		}
		if attempt == retryMaxAttempts-1 {
			break // out of attempts; sleeping here would only add dead time
		}
		wait := retryBaseDelay * (1 << uint(attempt)) // 1s, 2s
		LogWarn(tool, "rate limited by AH, retrying in %v (attempt %d/%d)", wait, attempt+1, retryMaxAttempts)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "too many requests")
}
