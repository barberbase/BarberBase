package bhejna

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestClassFor(t *testing.T) {
	tests := []struct {
		code     string
		expected SenderClass
	}{
		{"bb_staff_otp", SenderPlatform},
		{"bb_weekly_summary", SenderPlatform},
		{"bb_queue_joined", SenderCustomer},
		{"bb_near_turn", SenderCustomer},
		{"bb_you_are_next", SenderCustomer},
		{"bb_service_feedback", SenderCustomer},
		{"bb_appointment_confirmed", SenderCustomer},
		{"bb_appointment_reminder", SenderCustomer},
		{"bb_queue_cancelled", SenderCustomer},
		{"bb_queue_snoozed", SenderCustomer},
		{"bb_shop_closing_early", SenderCustomer},
		{"bb_marketing_broadcast", SenderCustomer},
		{"unknown_code", SenderCustomer},
		{"", SenderCustomer},
	}

	for _, tc := range tests {
		got := ClassFor(tc.code)
		if got != tc.expected {
			t.Errorf("ClassFor(%q) = %q; expected %q", tc.code, got, tc.expected)
		}
	}
}

func TestBhejnaClient_SenderClass_NoDBLookup(t *testing.T) {
	client := &bhejnaClient{
		pool:       nil, // nil pool will panic if queried
		modeAKey:   "mode-a-key-123",
		modeAPhone: "+912200000001",
	}

	// Should resolve platform credentials without panicking since it skips DB lookup.
	apiKey, fromPhone, err := client.resolveCredentials(context.Background(), uuid.New(), uuid.New(), SenderPlatform)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}

	if apiKey != "mode-a-key-123" || fromPhone != "+912200000001" {
		t.Errorf("Expected platform creds, got apiKey=%q, fromPhone=%q", apiKey, fromPhone)
	}
}

func TestBhejnaClient_ResolveCredentials_SenderClass(t *testing.T) {
	pool := setupTestDBForBhejna(t)
	defer pool.Close()

	ctx := context.Background()

	aesKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	encryptedKey, err := EncryptAESGCM("mode-b-secret-key", aesKey)
	if err != nil {
		t.Fatalf("Failed to encrypt Mode B API key: %v", err)
	}

	tenantID := uuid.New()
	locationID := uuid.New()

	_, err = pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Bhejna Tenant B', 'bhejna-tenant-b', '+919876543213')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, whatsapp_mode, business_whatsapp_number, bhejna_api_key_encrypted)
		VALUES ($1, $2, 'Bhejna Loc B', 'bhejna-tenant-b/loc-b', 'own_number', '+919876543222', $3)
	`, locationID, tenantID, encryptedKey)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	client := NewClient(pool, aesKey, "mode-a-key-123", "+912200000001").(*bhejnaClient)

	// Mode B + SenderPlatform -> platform credentials (mode A key/phone)
	apiKey, fromPhone, err := client.resolveCredentials(ctx, tenantID, locationID, SenderPlatform)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}
	if apiKey != "mode-a-key-123" || fromPhone != "+912200000001" {
		t.Errorf("Mode B + Platform template: expected platform credentials, got key=%q phone=%q", apiKey, fromPhone)
	}

	// Mode B + SenderCustomer -> shop credentials
	apiKey, fromPhone, err = client.resolveCredentials(ctx, tenantID, locationID, SenderCustomer)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}
	if apiKey != "mode-b-secret-key" || fromPhone != "+919876543222" {
		t.Errorf("Mode B + Customer template: expected shop credentials, got key=%q phone=%q", apiKey, fromPhone)
	}
}

func TestBhejnaClient_ResolveCredentials_ModeA(t *testing.T) {
	pool := setupTestDBForBhejna(t)
	defer pool.Close()

	ctx := context.Background()

	tenantID := uuid.New()
	locationID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Bhejna Tenant A', 'bhejna-tenant-a', '+919876543212')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, whatsapp_mode)
		VALUES ($1, $2, 'Bhejna Loc A', 'bhejna-tenant-a/loc-a', 'shared')
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	aesKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	client := NewClient(pool, aesKey, "mode-a-key-123", "+912200000001").(*bhejnaClient)

	// Mode A + SenderPlatform -> platform credentials
	apiKey, fromPhone, err := client.resolveCredentials(ctx, tenantID, locationID, SenderPlatform)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}
	if apiKey != "mode-a-key-123" || fromPhone != "+912200000001" {
		t.Errorf("Mode A + Platform template: expected platform credentials, got key=%q phone=%q", apiKey, fromPhone)
	}

	// Mode A + SenderCustomer -> platform credentials
	apiKey, fromPhone, err = client.resolveCredentials(ctx, tenantID, locationID, SenderCustomer)
	if err != nil {
		t.Fatalf("resolveCredentials failed: %v", err)
	}
	if apiKey != "mode-a-key-123" || fromPhone != "+912200000001" {
		t.Errorf("Mode A + Customer template: expected platform credentials, got key=%q phone=%q", apiKey, fromPhone)
	}
}
