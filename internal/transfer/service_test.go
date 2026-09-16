package transfer

import (
	"testing"
	"time"
)

func TestRetryDelayIsBoundedExponential(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 16 * time.Minute}
	for i, expected := range want {
		if got := RetryDelay(i + 1); got != expected {
			t.Fatalf("attempt %d: got %s want %s", i+1, got, expected)
		}
	}
}
