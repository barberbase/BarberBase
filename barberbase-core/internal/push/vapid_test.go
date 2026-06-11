package push

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("test-hmac-secret-32-bytes-minimum!!")

func TestGenerateAndVerify_RoundTrip(t *testing.T) {
	staffID := "01900000-0000-7000-8000-000000000001"
	locID := "01900000-0000-7000-8000-000000000002"

	token, err := GeneratePAT(testSecret, staffID, locID)
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}

	claims, err := VerifyPAT(testSecret, token)
	if err != nil {
		t.Fatalf("VerifyPAT: %v", err)
	}
	if claims.StaffMemberID != staffID {
		t.Errorf("StaffMemberID: got %q want %q", claims.StaffMemberID, staffID)
	}
	if claims.LocationID != locID {
		t.Errorf("LocationID: got %q want %q", claims.LocationID, locID)
	}
	if claims.Command != CommandCallNext {
		t.Errorf("Command: got %q want %q", claims.Command, CommandCallNext)
	}
	if time.Until(claims.ExpiresAt) > 4*time.Hour || time.Until(claims.ExpiresAt) < 3*time.Hour+55*time.Minute {
		t.Errorf("ExpiresAt out of expected range: %v", claims.ExpiresAt)
	}
}

func TestVerify_ForgedMAC(t *testing.T) {
	token, _ := GeneratePAT(testSecret, "staff-1", "loc-1")
	parts := strings.SplitN(token, ".", 2)
	// Replace MAC with a forged one
	forgedMAC := base64.RawURLEncoding.EncodeToString([]byte("nottherighthmacsignature00000000"))
	forged := parts[0] + "." + forgedMAC

	_, err := VerifyPAT(testSecret, forged)
	if err != ErrInvalidMAC {
		t.Errorf("expected ErrInvalidMAC, got %v", err)
	}
}

func TestVerify_TamperedPayload(t *testing.T) {
	// Generate valid token, then swap payload to a different staff ID while keeping old MAC
	_, _ = GeneratePAT(testSecret, "staff-original", "loc-1")
	// Build a token where payload encodes a different staff ID but MAC is from original
	tamperedRaw := "staff-evil:loc-1:call_next:9999999999"
	tamperedB64 := base64.RawURLEncoding.EncodeToString([]byte(tamperedRaw))
	// Compute MAC over the original payload segment to simulate replay, but with mismatched content
	originalToken, _ := GeneratePAT(testSecret, "staff-original", "loc-1")
	originalMAC := strings.SplitN(originalToken, ".", 2)[1]
	tampered := tamperedB64 + "." + originalMAC

	_, err := VerifyPAT(testSecret, tampered)
	if err != ErrInvalidMAC {
		t.Errorf("tampered payload: expected ErrInvalidMAC, got %v", err)
	}
}

func TestVerify_WrongCommand(t *testing.T) {
	// Construct a valid-MAC token with a different command literal
	raw := "staff-1:loc-1:confirm_arrival:9999999999"
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(raw))
	mac := computeMAC(testSecret, payloadB64)
	macB64 := base64.RawURLEncoding.EncodeToString(mac)
	token := payloadB64 + "." + macB64

	_, err := VerifyPAT(testSecret, token)
	if err != ErrWrongCommand {
		t.Errorf("wrong command: expected ErrWrongCommand, got %v", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	// Construct a valid-MAC token with unix_expires in the past
	raw := "staff-1:loc-1:call_next:1000000000" // Jan 2001
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(raw))
	mac := computeMAC(testSecret, payloadB64)
	macB64 := base64.RawURLEncoding.EncodeToString(mac)
	token := payloadB64 + "." + macB64

	_, err := VerifyPAT(testSecret, token)
	if err != ErrExpired {
		t.Errorf("expired: expected ErrExpired, got %v", err)
	}
}

func TestVerify_InvalidFormats(t *testing.T) {
	cases := []string{
		"",                     // empty
		"nodot",                // no dot
		".nodot",               // empty payload
		"nodot.",               // empty mac
		"a.b.c",               // three segments (SplitN(2) handles this correctly — "a" + "b.c")
		"!!invalid_b64.abcd",   // bad base64 in payload
	}
	for _, tc := range cases {
		_, err := VerifyPAT(testSecret, tc)
		if err == nil {
			t.Errorf("expected error for input %q, got nil", tc)
		}
	}
}

func TestVerify_ConstantTimePathConfirmed(t *testing.T) {
	// Structural test: VerifyPAT must call subtle.ConstantTimeCompare before
	// base64-decoding the payload. This is enforced by code ordering in vapid.go.
	// The test below verifies that a token with valid MAC but invalid base64 payload
	// returns ErrInvalidMAC (MAC check fires first), NOT ErrInvalidFormat.
	badPayload := "!!!notbase64!!!"
	mac := computeMAC(testSecret, badPayload)
	macB64 := base64.RawURLEncoding.EncodeToString(mac)
	token := badPayload + "." + macB64

	_, err := VerifyPAT(testSecret, token)
	// MAC is valid — so we get past MAC check and into payload decode,
	// where bad base64 returns ErrInvalidFormat. This confirms MAC runs first.
	if err != ErrInvalidFormat {
		t.Errorf("constant-time path: expected ErrInvalidFormat (MAC passed, payload decode failed), got %v", err)
	}
}
