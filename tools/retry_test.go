package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsRateLimited(t *testing.T) {
	rateLimited := []error{
		errors.New("API error: 429 Too Many Requests"),
		errors.New("rate limit exceeded"),
		errors.New("TOO MANY REQUESTS"),
	}
	for _, err := range rateLimited {
		if !isRateLimited(err) {
			t.Errorf("isRateLimited(%v) = false, want true", err)
		}
	}
	for _, err := range []error{nil, errors.New("API error: 500"), errors.New("connection refused")} {
		if isRateLimited(err) {
			t.Errorf("isRateLimited(%v) = true, want false", err)
		}
	}
}

func TestWithRetrySucceedsImmediately(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), "test", func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want nil, 1", err, calls)
	}
}

// Non-rate-limit failures must surface at once rather than being retried —
// retrying a rejected write would be both slow and potentially unsafe.
func TestWithRetryDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	want := errors.New("API error: 400 bad request")
	err := withRetry(context.Background(), "test", func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestWithRetryRecoversFromRateLimit(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), "test", func() error {
		calls++
		if calls < 2 {
			return errors.New("API error: 429")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after a successful retry", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestWithRetryGivesUp(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), "test", func() error {
		calls++
		return errors.New("429 too many requests")
	})
	if err == nil {
		t.Fatal("want the last error after exhausting attempts")
	}
	if calls != retryMaxAttempts {
		t.Fatalf("calls = %d, want %d", calls, retryMaxAttempts)
	}
}

func TestWithRetryHonoursContextCancellation(t *testing.T) {
	// A long backoff guarantees the cancelled context is the only ready case.
	orig := retryBaseDelay
	retryBaseDelay = time.Hour
	defer func() { retryBaseDelay = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := withRetry(ctx, "test", func() error {
		calls++
		return errors.New("429")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 before the cancelled backoff", calls)
	}
}
