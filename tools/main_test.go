package tools

import (
	"io"
	"os"
	"testing"
	"time"
)

// TestMain shrinks the retry backoff so the suite does not spend seconds
// sleeping, and silences the logger so test output stays readable.
func TestMain(m *testing.M) {
	retryBaseDelay = time.Millisecond
	logOutput = io.Discard
	os.Exit(m.Run())
}
