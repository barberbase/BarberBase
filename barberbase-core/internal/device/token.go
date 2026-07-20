// Package device implements station-device secrets for the hardware call-next
// layer. A device authenticates with an opaque bearer secret (X-Device-Token);
// only its SHA-256 lands in station_devices.token_hash. No token material is
// ever logged anywhere.
package device

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewSecret returns a fresh device secret: "bbd_" + 256 bits base64url.
// Shown to the operator exactly once at device creation.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("device secret: %w", err)
	}
	return "bbd_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash returns SHA-256(secret) — the only form ever persisted or compared.
// Constant-time compare is unnecessary: lookup is by hash equality in SQL.
func Hash(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}
