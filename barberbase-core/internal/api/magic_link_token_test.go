package api

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"barberbase-core/internal/domain/queue"

	"github.com/google/uuid"
)

// TestMagicLinkToken_RoundTrip locks the magic link token format to the
// CustomerSession verifier. This is the regression test for the "invalid URL"
// bug: three generators and two verifiers had drifted into three incompatible
// formats. The single source of truth is queue.GenerateMagicLinkToken and its
// output must verify against verifyCustomerSession (SSE) and be parseable as
// payload.mac with a 4-field colon payload (frontend reads location_id).
func TestMagicLinkToken_RoundTrip(t *testing.T) {
	secret := []byte("test-hmac-secret")
	customerID := uuid.New().String()
	locationID := uuid.New().String()
	visitID := uuid.New().String()
	expires := time.Now().Add(23 * time.Hour)

	token := queue.GenerateMagicLinkToken(customerID, locationID, visitID, expires, secret)

	// Shape: exactly two base64url segments
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token must have 2 segments, got %d: %q", len(parts), token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("payload segment not base64url: %v", err)
	}
	fields := strings.Split(string(payload), ":")
	if len(fields) != 4 {
		t.Fatalf("payload must have 4 colon fields, got %d: %q", len(fields), payload)
	}
	if fields[1] != locationID {
		t.Errorf("field[1] must be location_id (frontend SSE depends on it): got %q", fields[1])
	}

	// Verifies against the SSE CustomerSession check
	if !verifyCustomerSession(token, secret, locationID) {
		t.Fatal("generated token must pass verifyCustomerSession for its own location")
	}
	if verifyCustomerSession(token, secret, uuid.New().String()) {
		t.Fatal("token must not verify for a different location")
	}
	if verifyCustomerSession(token, []byte("wrong-secret"), locationID) {
		t.Fatal("token must not verify with a different secret")
	}

	// Expired token is rejected
	expired := queue.GenerateMagicLinkToken(customerID, locationID, visitID, time.Now().Add(-time.Minute), secret)
	if verifyCustomerSession(expired, secret, locationID) {
		t.Fatal("expired token must not verify")
	}

	// Deterministic: watchdog regenerates the identical string the join stored,
	// so the my-status DB lookup on the raw token keeps matching.
	if again := queue.GenerateMagicLinkToken(customerID, locationID, visitID, expires, secret); again != token {
		t.Fatal("token generation must be deterministic for fixed inputs")
	}

	// URL-safe: no chars that corrupt a query param
	if strings.ContainsAny(token, "+/= ") {
		t.Fatalf("token must be URL-safe, got %q", token)
	}
}
