package bhejna

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func getTestDatabaseURL() string {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}
	return url
}

func setupTestDBForBhejna(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	connStr := getTestDatabaseURL()

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("Failed to parse DB URL: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("Failed to init DB pool: %v", err)
	}

	// Run migration to ensure tables exist
	err = repository.Migrate(ctx, pool, "../../migrations/001_complete_schema.sql")
	if err != nil {
		t.Fatalf("Failed to run migrations for test: %v", err)
	}

	// Clean up database tables for a clean test run
	_, _ = pool.Exec(ctx, "TRUNCATE tenants CASCADE")

	return pool
}

func TestBhejnaClient_SendText_ModeA(t *testing.T) {
	pool := setupTestDBForBhejna(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed tenant and location in Mode A (whatsapp_mode = 'shared')
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

	// Setup mock Bhejna API server
	var receivedAuthHeader string
	var receivedPayload map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")

		dec := json.NewDecoder(r.Body)
		_ = dec.Decode(&receivedPayload)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted) // 202
		_, _ = w.Write([]byte(`{"job_id":"job-12345","status":"queued"}`))
	}))
	defer ts.Close()

	aesKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	client := NewClient(pool, aesKey, "mode-a-key-123", "+912200000001").(*bhejnaClient)
	client.bhejnaAPIURL = ts.URL

	req := SendTextReq{
		To:             "+919876543220",
		Body:           "Hello Test OTP: 123456",
		IdempotencyKey: "barberbase:otp:otp-123",
	}

	res, err := client.SendText(ctx, tenantID, locationID, req)
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	if res.JobID != "job-12345" {
		t.Errorf("Expected JobID 'job-12345', got '%s'", res.JobID)
	}

	// Verify Mode A credential resolution
	expectedAuth := "Bearer mode-a-key-123"
	if receivedAuthHeader != expectedAuth {
		t.Errorf("Expected Auth header '%s', got '%s'", expectedAuth, receivedAuthHeader)
	}

	if receivedPayload["from_business_phone"] != "+912200000001" {
		t.Errorf("Expected fromPhone '+912200000001', got '%v'", receivedPayload["from_business_phone"])
	}
}

func TestBhejnaClient_SendText_ModeB(t *testing.T) {
	pool := setupTestDBForBhejna(t)
	defer pool.Close()

	ctx := context.Background()

	aesKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	encryptedKey, err := EncryptAESGCM("mode-b-secret-key", aesKey)
	if err != nil {
		t.Fatalf("Failed to encrypt Mode B API key: %v", err)
	}

	// Seed tenant and location in Mode B (whatsapp_mode = 'own_number')
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

	// Setup mock Bhejna API server
	var receivedAuthHeader string
	var receivedPayload map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")

		dec := json.NewDecoder(r.Body)
		_ = dec.Decode(&receivedPayload)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted) // 202
		_, _ = w.Write([]byte(`{"job_id":"job-67890","status":"queued"}`))
	}))
	defer ts.Close()

	client := NewClient(pool, aesKey, "mode-a-key-123", "+912200000001").(*bhejnaClient)
	client.bhejnaAPIURL = ts.URL

	req := SendTextReq{
		To:             "+919876543220",
		Body:           "Hello Mode B",
		IdempotencyKey: "barberbase:otp:otp-456",
	}

	res, err := client.SendText(ctx, tenantID, locationID, req)
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	if res.JobID != "job-67890" {
		t.Errorf("Expected JobID 'job-67890', got '%s'", res.JobID)
	}

	// Verify decrypted Mode B credentials
	expectedAuth := "Bearer mode-b-secret-key"
	if receivedAuthHeader != expectedAuth {
		t.Errorf("Expected Auth header '%s', got '%s'", expectedAuth, receivedAuthHeader)
	}

	if receivedPayload["from_business_phone"] != "+919876543222" {
		t.Errorf("Expected fromPhone '+919876543222', got '%v'", receivedPayload["from_business_phone"])
	}
}

func TestBhejnaClient_ErrorTaxonomy(t *testing.T) {
	pool := setupTestDBForBhejna(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed tenant and location in Mode A
	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)
	_, _ = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug, whatsapp_mode) VALUES ($1, $2, 'Loc', 'slug/loc', 'shared')", locationID, tenantID)

	aesKey := []byte("0123456789abcdef0123456789abcdef")

	tests := []struct {
		name           string
		httpStatus     int
		responseBody   string
		expectedCode   string
		expectedRetry  bool
		expectErrorType bool
	}{
		{
			name:            "HTTP 500 Server Error",
			httpStatus:      http.StatusInternalServerError,
			responseBody:    `{"error": "internal error"}`,
			expectedRetry:   true,
			expectErrorType: true,
		},
		{
			name:            "HTTP 400 Retriable Error",
			httpStatus:      http.StatusBadRequest,
			responseBody:    `{"success":false,"error":{"code":"rate_limit_exceeded","retryable":true},"request_id":"req-1"}`,
			expectedCode:    "rate_limit_exceeded",
			expectedRetry:   true,
			expectErrorType: true,
		},
		{
			name:            "HTTP 400 Non-Retriable Error",
			httpStatus:      http.StatusBadRequest,
			responseBody:    `{"success":false,"error":{"code":"invalid_phone","retryable":false},"request_id":"req-2"}`,
			expectedCode:    "invalid_phone",
			expectedRetry:   false,
			expectErrorType: true,
		},
		{
			name:            "HTTP 400 Unparseable Error",
			httpStatus:      http.StatusBadRequest,
			responseBody:    `invalid json`,
			expectedCode:    "unparseable_error",
			expectedRetry:   false,
			expectErrorType: true,
		},
		{
			name:            "HTTP 200 Unexpected Status",
			httpStatus:      http.StatusOK, // success but not 202
			responseBody:    `{"success":true}`,
			expectedCode:    "unexpected_status",
			expectedRetry:   false,
			expectErrorType: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpStatus)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer ts.Close()

			client := NewClient(pool, aesKey, "mode-a-key", "+912200000001").(*bhejnaClient)
			client.bhejnaAPIURL = ts.URL

			_, err := client.SendText(ctx, tenantID, locationID, SendTextReq{
				To:             "+919876543220",
				Body:           "body",
				IdempotencyKey: "key",
			})

			if err == nil {
				t.Fatal("Expected error, but got nil")
			}

			var bErr BhejnaError
			if errors.As(err, &bErr) {
				if tc.expectedCode != "" && bErr.Code != tc.expectedCode {
					t.Errorf("Expected code '%s', got '%s'", tc.expectedCode, bErr.Code)
				}
				if bErr.Retriable != tc.expectedRetry {
					t.Errorf("Expected retriable=%t, got %t", tc.expectedRetry, bErr.Retriable)
				}
			} else {
				t.Errorf("Expected error to be BhejnaError, got %T", err)
			}
		})
	}
}

func TestAESGCMRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	plaintext := "my-secret-payload-12345"

	// Test successful round-trip
	encrypted, err := AESGCMEncrypt(plaintext, key)
	if err != nil {
		t.Fatalf("AESGCMEncrypt failed: %v", err)
	}

	decrypted, err := AESGCMDecrypt(encrypted, key)
	if err != nil {
		t.Fatalf("AESGCMDecrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("Expected decrypted text to equal plaintext, got %q, expected %q", decrypted, plaintext)
	}

	// Test invalid key size
	invalidKey := []byte("too-short")
	_, err = AESGCMEncrypt(plaintext, invalidKey)
	if err == nil {
		t.Error("Expected error when encrypting with invalid key size, got nil")
	}

	_, err = AESGCMDecrypt(encrypted, invalidKey)
	if err == nil {
		t.Error("Expected error when decrypting with invalid key size, got nil")
	}
}

