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

// TestLoad_MissingVAPIDPublicKey verifies Load() returns an error when VAPID_PUBLIC_KEY is absent.
func TestLoad_MissingVAPIDPublicKey(t *testing.T) {
	baseEnv(t)
	t.Setenv("VAPID_PUBLIC_KEY", "") // override: make absent

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when VAPID_PUBLIC_KEY is empty, got nil — server would start without web-push capability")
	}
	if !strings.Contains(err.Error(), "VAPID_PUBLIC_KEY") {
		t.Errorf("error message should mention VAPID_PUBLIC_KEY; got: %v", err)
	}
}

// TestLoad_MissingVAPIDPrivateKey verifies Load() returns an error when VAPID_PRIVATE_KEY is absent.
func TestLoad_MissingVAPIDPrivateKey(t *testing.T) {
	baseEnv(t)
	t.Setenv("VAPID_PRIVATE_KEY", "") // override: make absent

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when VAPID_PRIVATE_KEY is empty, got nil — server would start without web-push capability")
	}
	if !strings.Contains(err.Error(), "VAPID_PRIVATE_KEY") {
		t.Errorf("error message should mention VAPID_PRIVATE_KEY; got: %v", err)
	}
}

// TestLoad_MissingVAPIDSubject verifies Load() returns an error when VAPID_SUBJECT is absent.
func TestLoad_MissingVAPIDSubject(t *testing.T) {
	baseEnv(t)
	t.Setenv("VAPID_SUBJECT", "") // override: make absent

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when VAPID_SUBJECT is empty, got nil — server would start without web-push capability")
	}
	if !strings.Contains(err.Error(), "VAPID_SUBJECT") {
		t.Errorf("error message should mention VAPID_SUBJECT; got: %v", err)
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
