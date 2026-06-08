package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/config"
	"barberbase-core/internal/repository"
	pkgmiddleware "barberbase-core/pkg/middleware"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type mockBhejna struct{}

func (m mockBhejna) SendText(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTextReq) (*bhejna.SendResult, error) {
	return &bhejna.SendResult{JobID: "test-job-text"}, nil
}

func (m mockBhejna) SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTemplateReq) (*bhejna.SendResult, error) {
	return &bhejna.SendResult{JobID: "test-job-template"}, nil
}

func setupTestServer(t *testing.T) (*Server, *pgxpool.Pool, uuid.UUID, uuid.UUID, uuid.UUID, string) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := repository.InitPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to initialize pool: %v", err)
	}

	// Run migrations
	err = repository.Migrate(ctx, pool, "../../migrations/001_complete_schema.sql")
	if err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	// Clean tables
	_, _ = pool.Exec(ctx, "DELETE FROM webhook_events")
	_, _ = pool.Exec(ctx, "DELETE FROM staff_otps")
	_, _ = pool.Exec(ctx, "DELETE FROM staff_members")
	_, _ = pool.Exec(ctx, "DELETE FROM locations")
	_, _ = pool.Exec(ctx, "DELETE FROM tenants")

	// Seed data
	tenantID := uuid.New()
	locationID := uuid.New()
	staffID := uuid.New()
	phone := "+919999999999"

	_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Test Tenant', 'test-tenant', $2)", tenantID, phone)
	if err != nil {
		t.Fatalf("Failed to insert tenant: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'Test Location', 'test-location')", locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to insert location: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active) VALUES ($1, $2, $3, 'Test Staff', $4, 'barber', true)", staffID, tenantID, locationID, phone)
	if err != nil {
		t.Fatalf("Failed to insert staff member: %v", err)
	}

	s := &Server{
		Pool:   pool,
		Bhejna: mockBhejna{},
		Config: cfg,
	}

	return s, pool, tenantID, locationID, staffID, phone
}

func TestVerifyStaffOTP_ReplayedOTP(t *testing.T) {
	s, pool, _, _, _, phone := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	otpCode := "123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	_, err := pool.Exec(ctx, "INSERT INTO staff_otps (phone_number, otp_hash, expires_at) VALUES ($1, $2, NOW() + INTERVAL '5 minutes')", phone, string(hash))
	if err != nil {
		t.Fatalf("Failed to insert test OTP: %v", err)
	}

	// First verification: should succeed (200)
	body := map[string]string{"phone_number": phone, "otp": otpCode}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
	rec := httptest.NewRecorder()

	s.VerifyStaffOTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Second verification: should fail (401) because consumed_at is set
	req2 := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
	rec2 := httptest.NewRecorder()

	s.VerifyStaffOTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized for replayed OTP, got %d. Response: %s", rec2.Code, rec2.Body.String())
	}
}

func TestVerifyStaffOTP_LockoutAfter5Attempts(t *testing.T) {
	s, pool, _, _, _, phone := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	otpCode := "123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	_, err := pool.Exec(ctx, "INSERT INTO staff_otps (phone_number, otp_hash, expires_at) VALUES ($1, $2, NOW() + INTERVAL '5 minutes')", phone, string(hash))
	if err != nil {
		t.Fatalf("Failed to insert test OTP: %v", err)
	}

	// Make 5 incorrect attempts
	body := map[string]string{"phone_number": phone, "otp": "000000"}
	jsonBody, _ := json.Marshal(body)

	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
		rec := httptest.NewRecorder()
		s.VerifyStaffOTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Attempt %d: Expected 401, got %d", i, rec.Code)
		}
	}

	// 6th attempt with the CORRECT OTP should still fail (401)
	bodyCorrect := map[string]string{"phone_number": phone, "otp": otpCode}
	jsonBodyCorrect, _ := json.Marshal(bodyCorrect)
	req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBodyCorrect))
	rec := httptest.NewRecorder()

	s.VerifyStaffOTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("6th attempt (correct OTP): Expected 401, got %d", rec.Code)
	}
}

func TestVerifyStaffOTP_ExpiredOTP(t *testing.T) {
	s, pool, _, _, _, phone := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	otpCode := "123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	// Insert expired OTP
	_, err := pool.Exec(ctx, "INSERT INTO staff_otps (phone_number, otp_hash, expires_at) VALUES ($1, $2, NOW() - INTERVAL '1 minute')", phone, string(hash))
	if err != nil {
		t.Fatalf("Failed to insert test OTP: %v", err)
	}

	body := map[string]string{"phone_number": phone, "otp": otpCode}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
	rec := httptest.NewRecorder()

	s.VerifyStaffOTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for expired OTP, got %d", rec.Code)
	}
}

func TestTenantMiddleware_RejectBodyTenantID(t *testing.T) {
	// Setup a dummy handler that returns 200 OK
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	middleware := pkgmiddleware.RejectBodyTenantID(dummyHandler)

	// Case 1: Body with tenant_id -> should reject with 400
	bodyWithTenant := map[string]interface{}{"tenant_id": "01900000-0000-0000-0000-000000000000", "name": "Barber"}
	jsonBody, _ := json.Marshal(bodyWithTenant)
	req := httptest.NewRequest(http.MethodPost, "/staff/members", bytes.NewReader(jsonBody))
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Case 2: Body without tenant_id -> should pass to next (200)
	bodyWithoutTenant := map[string]interface{}{"name": "Barber"}
	jsonBody2, _ := json.Marshal(bodyWithoutTenant)
	req2 := httptest.NewRequest(http.MethodPost, "/staff/members", bytes.NewReader(jsonBody2))
	rec2 := httptest.NewRecorder()

	middleware.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec2.Code, rec2.Body.String())
	}
}

func TestVerifyStaffOTP_Concurrency(t *testing.T) {
	s, pool, _, _, _, phone := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	otpCode := "123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	_, err := pool.Exec(ctx, "INSERT INTO staff_otps (phone_number, otp_hash, expires_at) VALUES ($1, $2, NOW() + INTERVAL '5 minutes')", phone, string(hash))
	if err != nil {
		t.Fatalf("Failed to insert test OTP: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	resCodes := make(chan int, 2)

	body := map[string]string{"phone_number": phone, "otp": otpCode}
	jsonBody, _ := json.Marshal(body)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
			rec := httptest.NewRecorder()
			s.VerifyStaffOTP(rec, req)
			resCodes <- rec.Code
		}()
	}

	wg.Wait()
	close(resCodes)

	code1 := <-resCodes
	code2 := <-resCodes

	// Exactly one request must return 200, and the other must return 401
	if (code1 == 200 && code2 == 401) || (code1 == 401 && code2 == 200) {
		// Pass!
	} else {
		t.Fatalf("Expected exactly one 200 and one 401, but got codes: %d and %d", code1, code2)
	}
}

func TestVerifyStaffOTP_RefreshTokenTTL30Days(t *testing.T) {
	s, pool, _, _, _, phone := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	otpCode := "123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	_, err := pool.Exec(ctx, "INSERT INTO staff_otps (phone_number, otp_hash, expires_at) VALUES ($1, $2, NOW() + INTERVAL '5 minutes')", phone, string(hash))
	if err != nil {
		t.Fatalf("Failed to insert test OTP: %v", err)
	}

	body := map[string]string{"phone_number": phone, "otp": otpCode}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/staff/verify-otp", bytes.NewReader(jsonBody))
	rec := httptest.NewRecorder()

	s.VerifyStaffOTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Parse cookies and find bb_refresh
	cookies := rec.Result().Cookies()
	var refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "bb_refresh" {
			refreshCookie = c
		}
	}
	if refreshCookie == nil {
		t.Fatal("bb_refresh cookie was not returned in response")
	}

	// Parse refresh claims
	claims, err := auth.ParseAndVerifyRefreshToken(refreshCookie.Value, []byte(s.Config.JWTSecret))
	if err != nil {
		t.Fatalf("Failed to parse refresh token: %v", err)
	}

	expiresAt := claims.ExpiresAt.Time
	duration := expiresAt.Sub(time.Now())

	// Should be approximately 30 days (allow 10 seconds skew for testing runtime delay)
	expectedDuration := 30 * 24 * time.Hour
	diff := duration - expectedDuration
	if diff < 0 {
		diff = -diff
	}

	if diff > 10*time.Second {
		t.Fatalf("Expected refresh token TTL to be 30 days (skew <= 10s), got %v (diff: %v)", duration, diff)
	}
}

// ===========================================================================
// WEBHOOK INGRESS INTEGRATION TESTS
// ===========================================================================

type failingMockBhejna struct {
	t *testing.T
}

func (f failingMockBhejna) SendText(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTextReq) (*bhejna.SendResult, error) {
	f.t.Fatal("Downstream processing (SendText) was called synchronously on the request path!")
	return nil, nil
}

func (f failingMockBhejna) SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTemplateReq) (*bhejna.SendResult, error) {
	f.t.Fatal("Downstream processing (SendTemplate) was called synchronously on the request path!")
	return nil, nil
}

func computeHMACSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return BhejnaSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func TestReceiveBhejnaWebhook_ModeA_Success(t *testing.T) {
	s, pool, _, _, _, _ := setupTestServer(t)
	defer pool.Close()

	secret := "super-secret-platform-key-for-webhooks-12345"
	os.Setenv("BHEJNA_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("BHEJNA_WEBHOOK_SECRET")

	eventID := uuid.New().String()
	payload := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":      "message.received",
		"channel":         "whatsapp",
		"received_at":     time.Now().Format(time.RFC3339),
		"business_phone_number": "912212345678",
		"sender": map[string]interface{}{
			"phone_number": "919876543210",
		},
		"message": map[string]interface{}{
			"type": "text",
			"body": "JOIN test-salon JN8K4P",
		},
	}
	bodyBytes, _ := json.Marshal(payload)
	signature := computeHMACSignature(bodyBytes, secret)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/bhejna", bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Verify database insert
	ctx := context.Background()
	var exists bool
	var dbStatus string
	var dbLocationID *uuid.UUID
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM webhook_events WHERE external_event_id = $1), status, location_id FROM webhook_events WHERE external_event_id = $1 GROUP BY status, location_id", eventID).Scan(&exists, &dbStatus, &dbLocationID)
	if err != nil {
		t.Fatalf("Failed to query webhook_events: %v", err)
	}
	if !exists {
		t.Fatal("Webhook event was not inserted into database")
	}
	if dbStatus != "pending" {
		t.Errorf("Expected status 'pending', got %s", dbStatus)
	}
	if dbLocationID != nil {
		t.Errorf("Expected location_id to be nil, got %v", dbLocationID)
	}
}

func TestReceiveBhejnaWebhook_ModeA_InvalidSignature(t *testing.T) {
	s, pool, _, _, _, _ := setupTestServer(t)
	defer pool.Close()

	secret := "super-secret-platform-key-for-webhooks-12345"
	os.Setenv("BHEJNA_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("BHEJNA_WEBHOOK_SECRET")

	eventID := uuid.New().String()
	payload := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":      "message.received",
	}
	bodyBytes, _ := json.Marshal(payload)
	signature := computeHMACSignature(bodyBytes, "wrong-secret-value")

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/bhejna", bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Verify no database insert
	ctx := context.Background()
	var exists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM webhook_events WHERE external_event_id = $1)", eventID).Scan(&exists)
	if err != nil {
		t.Fatalf("Failed to query webhook_events: %v", err)
	}
	if exists {
		t.Fatal("Webhook event should NOT have been inserted into database")
	}
}

func TestReceiveBhejnaWebhookModeB_Success(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	testSecret := "my-awesome-shop-mode-b-webhook-secret-9999"

	// Encrypt the secret
	encryptedSecret, err := bhejna.EncryptAESGCM(testSecret, s.Config.AESEncryptionKey)
	if err != nil {
		t.Fatalf("Failed to encrypt webhook secret: %v", err)
	}

	// Update location to own_number and set encrypted secret
	_, err = pool.Exec(ctx, `
		UPDATE locations 
		SET whatsapp_mode = 'own_number', bhejna_webhook_secret_encrypted = $1 
		WHERE id = $2
	`, encryptedSecret, locationID)
	if err != nil {
		t.Fatalf("Failed to update location to Mode B: %v", err)
	}

	eventID := uuid.New().String()
	payload := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":      "message.received",
		"channel":         "whatsapp",
		"received_at":     time.Now().Format(time.RFC3339),
		"business_phone_number": "912212345678",
		"sender": map[string]interface{}{
			"phone_number": "919876543210",
		},
	}
	bodyBytes, _ := json.Marshal(payload)
	signature := computeHMACSignature(bodyBytes, testSecret)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/webhooks/bhejna/loc/%s", locationID.String()), bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Verify database insert with tenant_id and location_id
	var exists bool
	var dbStatus string
	var dbLocationID uuid.UUID
	var dbTenantID uuid.UUID
	err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM webhook_events WHERE external_event_id = $1), status, tenant_id, location_id FROM webhook_events WHERE external_event_id = $1 GROUP BY status, tenant_id, location_id", eventID).Scan(&exists, &dbStatus, &dbTenantID, &dbLocationID)
	if err != nil {
		t.Fatalf("Failed to query webhook_events: %v", err)
	}
	if !exists {
		t.Fatal("Webhook event was not inserted into database")
	}
	if dbStatus != "pending" {
		t.Errorf("Expected status 'pending', got %s", dbStatus)
	}
	if dbLocationID != locationID {
		t.Errorf("Expected location_id %v, got %v", locationID, dbLocationID)
	}
	if dbTenantID != tenantID {
		t.Errorf("Expected tenant_id %v, got %v", tenantID, dbTenantID)
	}
}

func TestReceiveBhejnaWebhookModeB_ConfigErrors(t *testing.T) {
	s, pool, _, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()
	eventID := uuid.New().String()
	payload := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":      "message.received",
	}
	bodyBytes, _ := json.Marshal(payload)
	signature := computeHMACSignature(bodyBytes, "some-secret")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/webhooks/bhejna/loc/%s", locationID.String()), bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found for location in shared mode, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	randomLocID := uuid.New()
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/webhooks/bhejna/loc/%s", randomLocID.String()), bytes.NewReader(bodyBytes))
	req2.Header.Set(BhejnaSignatureHeader, signature)
	rec2 := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec2, req2, randomLocID)

	if rec2.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found for non-existent location, got %d. Response: %s", rec2.Code, rec2.Body.String())
	}

	_, err := pool.Exec(ctx, "UPDATE locations SET is_active = false WHERE id = $1", locationID)
	if err != nil {
		t.Fatalf("Failed to soft-delete location: %v", err)
	}

	req3 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/webhooks/bhejna/loc/%s", locationID.String()), bytes.NewReader(bodyBytes))
	req3.Header.Set(BhejnaSignatureHeader, signature)
	rec3 := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec3, req3, locationID)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found for soft-deleted location, got %d. Response: %s", rec3.Code, rec3.Body.String())
	}
}

func TestReceiveBhejnaWebhook_InvalidJSON_ValidSignature(t *testing.T) {
	s, pool, _, _, _, _ := setupTestServer(t)
	defer pool.Close()

	secret := "super-secret-platform-key-for-webhooks-12345"
	os.Setenv("BHEJNA_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("BHEJNA_WEBHOOK_SECRET")

	bodyBytes := []byte("this is not valid JSON string!!!")
	signature := computeHMACSignature(bodyBytes, secret)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/bhejna", bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for invalid JSON but valid signature, got %d. Response: %s", rec.Code, rec.Body.String())
	}
}

func TestReceiveBhejnaWebhook_Latency_And_NoDownstream(t *testing.T) {
	s, pool, _, _, _, _ := setupTestServer(t)
	defer pool.Close()

	secret := "super-secret-platform-key-for-webhooks-12345"
	os.Setenv("BHEJNA_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("BHEJNA_WEBHOOK_SECRET")

	s.Bhejna = failingMockBhejna{t: t}

	eventID := uuid.New().String()
	payload := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":      "message.received",
		"channel":         "whatsapp",
		"received_at":     time.Now().Format(time.RFC3339),
		"business_phone_number": "912212345678",
		"sender": map[string]interface{}{
			"phone_number": "919876543210",
		},
	}
	bodyBytes, _ := json.Marshal(payload)
	signature := computeHMACSignature(bodyBytes, secret)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/bhejna", bytes.NewReader(bodyBytes))
	req.Header.Set(BhejnaSignatureHeader, signature)
	rec := httptest.NewRecorder()

	start := time.Now()
	s.ReceiveBhejnaWebhook(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	if elapsed >= 50*time.Millisecond {
		t.Errorf("Expected handler response time < 50ms, took %s", elapsed)
	}
}

