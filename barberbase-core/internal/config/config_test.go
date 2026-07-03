package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"barberbase-core/internal/config"
)

// baseEnv sets every env var that config.Load() requires.
// t.Setenv automatically restores original values when the test ends.
func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb?sslmode=disable")
	t.Setenv("JWT_SECRET", "test-jwt-secret-value")
	t.Setenv("HMAC_SECRET", "test-hmac-secret-value")
	t.Setenv("AES_ENCRYPTION_KEY", "12345678901234567890123456789012") // exactly 32 bytes
	t.Setenv("VAPID_PUBLIC_KEY", "BNhSTbMpAHFWBkBYWMjmFPuMYSoXqPuPmPqCelgQrhs9ZITAbBuznEzGm9ZfFlm-m8jkLBm4J1P7H2RqCOhFhJo")
	t.Setenv("VAPID_PRIVATE_KEY", "tLd5AVFH6m5Y3IjUcw5hR4bTmw6RtMXRVfcQaEd9kDo")
	t.Setenv("VAPID_SUBJECT", "mailto:ops@barberbase.in")
}

// TestLoad_AllVAPIDPresent confirms Load() succeeds when all three VAPID vars are set.
func TestLoad_AllVAPIDPresent(t *testing.T) {
	baseEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error with all VAPID vars set, got: %v", err)
	}
	if cfg.VAPIDPublicKey == "" {
		t.Error("VAPIDPublicKey should not be empty")
	}
	if cfg.VAPIDPrivateKey == "" {
		t.Error("VAPIDPrivateKey should not be empty")
	}
	if cfg.VAPIDSubject == "" {
		t.Error("VAPIDSubject should not be empty")
	}
}

// TestLoad_MissingVAPID_BootsPushDisabled verifies Law 21: the server must boot
// with zero push infrastructure. Empty VAPID vars are not an error — push is
// simply disabled and queue correctness is unaffected.
func TestLoad_MissingVAPID_BootsPushDisabled(t *testing.T) {
	baseEnv(t)
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("VAPID_SUBJECT", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Law 21 violation: Load() must succeed with no VAPID config, got: %v", err)
	}
	if cfg.VAPIDPublicKey != "" || cfg.VAPIDPrivateKey != "" || cfg.VAPIDSubject != "" {
		t.Error("VAPID fields should be empty when env vars are unset")
	}
}

// TestConfig_VAPIDPrivateKey_NotInJSONSerialization verifies that VAPIDPrivateKey
// does not appear in any JSON serialization of the Config struct (no json tag leaks).
func TestConfig_VAPIDPrivateKey_NotInJSONSerialization(t *testing.T) {
	baseEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(cfg) failed: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, cfg.VAPIDPrivateKey) {
		t.Errorf("VAPIDPrivateKey value appears in JSON output — it must never be serialized: %s", jsonStr)
	}
	// Also check the field name itself would not leak in a logged output
	if strings.Contains(jsonStr, "vapid_private") || strings.Contains(strings.ToLower(jsonStr), "vapidprivatekey") {
		t.Errorf("VAPIDPrivateKey field name appears in JSON output — it must not be tagged for serialization: %s", jsonStr)
	}
}
