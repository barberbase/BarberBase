package push

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	CommandCallNext = "call_next"
	patTTL          = 4 * time.Hour
)

// Sentinel errors. Callers map these to HTTP status codes:
//   ErrInvalidFormat, ErrInvalidMAC, ErrExpired → 401
//   ErrWrongCommand                             → 403
var (
	ErrInvalidFormat = errors.New("pat: invalid format")
	ErrInvalidMAC    = errors.New("pat: invalid mac")
	ErrExpired       = errors.New("pat: token expired")
	ErrWrongCommand  = errors.New("pat: wrong command") // Law 20
)

// PATClaims holds verified, parsed claims from a Push Action Token.
// tenant_id is intentionally absent — caller derives it via
// SELECT staff_members WHERE id=$1 (Law 11 pattern).
type PATClaims struct {
	StaffMemberID string
	LocationID    string
	Command       string
	ExpiresAt     time.Time
}

// GeneratePAT produces a signed Push Action Token for the given staff member
// and location. Command is always "call_next" (Law 20). TTL is 4h (Law 18).
// hmacSecret is the raw HMAC_SECRET env var bytes — no new secret.
func GeneratePAT(hmacSecret []byte, staffMemberID, locationID string) (string, error) {
	if len(hmacSecret) == 0 {
		return "", fmt.Errorf("pat: hmac secret must not be empty")
	}
	raw := fmt.Sprintf("%s:%s:%s:%d",
		staffMemberID, locationID, CommandCallNext,
		time.Now().Add(patTTL).Unix(),
	)
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(raw))
	macB64 := base64.RawURLEncoding.EncodeToString(computeMAC(hmacSecret, payloadB64))
	return payloadB64 + "." + macB64, nil
}

// VerifyPAT validates a Push Action Token.
// Verification order (from spec):
//   1. Exactly two dot-separated segments
//   2. Constant-time MAC compare   ← must precede payload decode
//   3. Parse payload
//   4. Command literal check       → ErrWrongCommand (403)
//   5. Expiry check                → ErrExpired (401)
func VerifyPAT(hmacSecret []byte, token string) (*PATClaims, error) {
	// Step 1 — format
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalidFormat
	}
	payloadB64, macB64 := parts[0], parts[1]

	// Step 2 — constant-time MAC verification before any payload parsing
	gotMAC, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return nil, ErrInvalidFormat
	}
	expectedMAC := computeMAC(hmacSecret, payloadB64)
	if subtle.ConstantTimeCompare(expectedMAC, gotMAC) != 1 {
		return nil, ErrInvalidMAC
	}

	// Step 3 — decode and parse (only after MAC is confirmed)
	rawPayload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrInvalidFormat
	}
	fields := strings.Split(string(rawPayload), ":")
	if len(fields) != 4 {
		return nil, ErrInvalidFormat
	}
	staffMemberID, locationID, command, unixStr := fields[0], fields[1], fields[2], fields[3]

	// Step 4 — command scope (Law 20): 403, not 401, so caller can distinguish
	if command != CommandCallNext {
		return nil, ErrWrongCommand
	}

	// Step 5 — expiry
	unixExp, err := strconv.ParseInt(unixStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidFormat
	}
	expiresAt := time.Unix(unixExp, 0)
	if time.Now().After(expiresAt) {
		return nil, ErrExpired
	}

	return &PATClaims{
		StaffMemberID: staffMemberID,
		LocationID:    locationID,
		Command:       command,
		ExpiresAt:     expiresAt,
	}, nil
}

// computeMAC returns HMAC-SHA256(message, key).
func computeMAC(key []byte, message string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return h.Sum(nil)
}
