package api

import (
	"testing"
	"time"
)

// Regression: opens_at leaked as "2000-01-01T09:00:00Z" into customer copy.
func TestHHMM(t *testing.T) {
	ts := time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC) // pgx scan shape for time '09:00:00'
	if got := *hhmm(&ts); got != "09:00" {
		t.Fatalf("hhmm = %q, want %q", got, "09:00")
	}
	if hhmm(nil) != nil {
		t.Fatal("hhmm(nil) should be nil")
	}
}
