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

	// Ensure VAPID vars are present for config.Load() validation.
	if os.Getenv("VAPID_PUBLIC_KEY") == "" {
		os.Setenv("VAPID_PUBLIC_KEY", "BNhSTbMpAHFWBkBYWMjmFPuMYSoXqPuPmPqCelgQrhs9ZITAbBuznEzGm9ZfFlm-m8jkLBm4J1P7H2RqCOhFhJo")
	}
	if os.Getenv("VAPID_PRIVATE_KEY") == "" {
		os.Setenv("VAPID_PRIVATE_KEY", "tLd5AVFH6m5Y3IjUcw5hR4bTmw6RtMXRVfcQaEd9kDo")
	}
	if os.Getenv("VAPID_SUBJECT") == "" {
		os.Setenv("VAPID_SUBJECT", "mailto:ops@barberbase.in")
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
	_, _ = pool.Exec(ctx, "TRUNCATE tenants CASCADE")
	_, _ = pool.Exec(ctx, "DELETE FROM staff_otps")

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

func newStaffRequestWithRole(method, url string, tenantID, locationID, staffID uuid.UUID, role string) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
	ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
	ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	return req.WithContext(ctx)
}

func TestGetDailyAnalytics(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)
	ctx := context.Background()

	// Clean up any remaining charges just in case
	_, _ = pool.Exec(ctx, "DELETE FROM visit_charges")

	// 1. Test: Barber role JWT -> 403
	{
		req := newStaffRequestWithRole(http.MethodGet, "/v1/staff/analytics/daily", tenantID, locationID, barberAID, "barber")
		rec := httptest.NewRecorder()
		s.GetDailyAnalytics(rec, req, GetDailyAnalyticsParams{})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected 403 Forbidden for barber role, got %d", rec.Code)
		}
	}

	// 2. Test: Request for a date with no queue_session -> 200, all zeros, empty breakdown
	{
		req := newStaffRequestWithRole(http.MethodGet, "/v1/staff/analytics/daily?date=2026-06-10", tenantID, locationID, barberAID, "manager")
		rec := httptest.NewRecorder()
		s.GetDailyAnalytics(rec, req, GetDailyAnalyticsParams{})
		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK for no queue_session, got %d. Response: %s", rec.Code, rec.Body.String())
		}
		var resp DailyAnalytics
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}
		if resp.TotalVisits != 0 {
			t.Errorf("Expected 0 total visits, got %d", resp.TotalVisits)
		}
		if resp.TotalRevenuePaise != 0 {
			t.Errorf("Expected 0 total revenue, got %d", resp.TotalRevenuePaise)
		}
		if resp.AverageWaitMinutes != nil {
			t.Errorf("Expected nil average wait minutes, got %v", *resp.AverageWaitMinutes)
		}
		if resp.NoShowCount == nil || *resp.NoShowCount != 0 {
			t.Errorf("Expected 0 no show count, got %v", resp.NoShowCount)
		}
		if resp.CancelledCount == nil || *resp.CancelledCount != 0 {
			t.Errorf("Expected 0 cancelled count, got %v", resp.CancelledCount)
		}
		if len(resp.BarberBreakdown) != 0 {
			t.Errorf("Expected empty barber breakdown, got %d entries", len(resp.BarberBreakdown))
		}
	}

	// 3. Test: Seed and check totals & breakdown
	businessDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	sessionID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status, queue_version, last_token_number)
		VALUES ($1, $2, $3, $4, 'active', 0, 0)`, sessionID, tenantID, locationID, businessDate)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	seedVisitEntryAndCharge := func(visitID uuid.UUID, state string, barberID *uuid.UUID, remoteJoined, called, started, completed time.Time, amountPaise int, chargeStatus string) {
		customerID := uuid.New()
		_, err := pool.Exec(ctx, `
			INSERT INTO customers (id, tenant_id, phone_number, name)
			VALUES ($1, $2, $3, 'Customer')`, customerID, tenantID, "+91"+uuid.New().String()[:10])
		if err != nil {
			t.Fatalf("Failed to seed customer: %v", err)
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes)
			VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 30)`, visitID, tenantID, locationID, customerID)
		if err != nil {
			t.Fatalf("Failed to seed visit: %v", err)
		}

		var qCalled, qStarted, qCompleted *time.Time
		if !called.IsZero() {
			qCalled = &called
		}
		if !started.IsZero() {
			qStarted = &started
		}
		if !completed.IsZero() {
			qCompleted = &completed
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, assigned_barber_id, remote_joined_at, called_at, started_at, completed_at)
			VALUES ($1, $2, $3, $4, (SELECT COALESCE(MAX(token_number), 0) + 1 FROM queue_entries WHERE queue_session_id = $3), $5, 'arrived', true, $6, $7, $8, $9, $10)`,
			uuid.New(), visitID, sessionID, customerID, state, barberID, remoteJoined, qCalled, qStarted, qCompleted)
		if err != nil {
			t.Fatalf("Failed to seed queue entry: %v", err)
		}

		if amountPaise > 0 {
			_, err = pool.Exec(ctx, `
				INSERT INTO visit_charges (id, tenant_id, location_id, visit_id, total_amount_paise, status)
				VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), tenantID, locationID, visitID, amountPaise, chargeStatus)
			if err != nil {
				t.Fatalf("Failed to seed charge: %v", err)
			}
		}
	}

	// Seed completed visits
	v1ID := uuid.New()
	tJoined1 := businessDate.Add(10 * time.Minute)
	tCalled1 := tJoined1.Add(15 * time.Minute)
	tStarted1 := tCalled1.Add(5 * time.Minute)
	tCompleted1 := tStarted1.Add(25 * time.Minute)
	seedVisitEntryAndCharge(v1ID, "completed", &barberAID, tJoined1, tCalled1, tStarted1, tCompleted1, 15000, "finalized")

	v2ID := uuid.New()
	tJoined2 := businessDate.Add(40 * time.Minute)
	tCalled2 := tJoined2.Add(25 * time.Minute)
	tStarted2 := tCalled2.Add(2 * time.Minute)
	tCompleted2 := tStarted2.Add(35 * time.Minute)
	seedVisitEntryAndCharge(v2ID, "completed", &barberAID, tJoined2, tCalled2, tStarted2, tCompleted2, 25000, "finalized")

	v3ID := uuid.New()
	tJoined3 := businessDate.Add(2 * time.Hour)
	tCalled3 := tJoined3.Add(20 * time.Minute)
	tStarted3 := tCalled3.Add(1 * time.Minute)
	tCompleted3 := tStarted3.Add(30 * time.Minute)
	seedVisitEntryAndCharge(v3ID, "completed", &barberBID, tJoined3, tCalled3, tStarted3, tCompleted3, 30000, "finalized")

	// Seed no_show visit
	v4ID := uuid.New()
	seedVisitEntryAndCharge(v4ID, "no_show", &barberAID, businessDate.Add(3*time.Hour), time.Time{}, time.Time{}, time.Time{}, 0, "")

	// Seed cancelled visit
	v5ID := uuid.New()
	seedVisitEntryAndCharge(v5ID, "cancelled", &barberBID, businessDate.Add(4*time.Hour), time.Time{}, time.Time{}, time.Time{}, 0, "")

	// Request daily analytics
	req := newStaffRequestWithRole(http.MethodGet, "/v1/staff/analytics/daily?date=2026-06-10", tenantID, locationID, barberAID, "owner")
	rec := httptest.NewRecorder()
	s.GetDailyAnalytics(rec, req, GetDailyAnalyticsParams{})
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	var resp DailyAnalytics
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.TotalVisits != 3 {
		t.Errorf("Expected 3 completed visits, got %d", resp.TotalVisits)
	}
	if resp.TotalRevenuePaise != 70000 {
		t.Errorf("Expected 70000 paise revenue, got %d", resp.TotalRevenuePaise)
	}
	if resp.AverageWaitMinutes == nil || *resp.AverageWaitMinutes != 20 {
		if resp.AverageWaitMinutes == nil {
			t.Errorf("Expected average wait 20, got nil")
		} else {
			t.Errorf("Expected average wait 20, got %d", *resp.AverageWaitMinutes)
		}
	}
	if resp.NoShowCount == nil || *resp.NoShowCount != 1 {
		t.Errorf("Expected 1 no show count, got %v", resp.NoShowCount)
	}
	if resp.CancelledCount == nil || *resp.CancelledCount != 1 {
		t.Errorf("Expected 1 cancelled count, got %v", resp.CancelledCount)
	}

	if len(resp.BarberBreakdown) != 2 {
		t.Fatalf("Expected 2 barbers in breakdown, got %d", len(resp.BarberBreakdown))
	}

	var revenueSum int64
	for _, ba := range resp.BarberBreakdown {
		if ba.BarberId == nil || ba.RevenuePaise == nil || ba.VisitsCompleted == nil || ba.AverageServiceMinutes == nil {
			t.Fatalf("Expected non-nil fields in barber breakdown")
		}
		revenueSum += int64(*ba.RevenuePaise)

		if *ba.BarberId == barberAID {
			if *ba.VisitsCompleted != 2 {
				t.Errorf("Expected Barber A visits = 2, got %d", *ba.VisitsCompleted)
			}
			if *ba.RevenuePaise != 40000 {
				t.Errorf("Expected Barber A revenue = 40000, got %d", *ba.RevenuePaise)
			}
			if *ba.AverageServiceMinutes != 30 {
				t.Errorf("Expected Barber A avg service = 30, got %d", *ba.AverageServiceMinutes)
			}
		} else if *ba.BarberId == barberBID {
			if *ba.VisitsCompleted != 1 {
				t.Errorf("Expected Barber B visits = 1, got %d", *ba.VisitsCompleted)
			}
			if *ba.RevenuePaise != 30000 {
				t.Errorf("Expected Barber B revenue = 30000, got %d", *ba.RevenuePaise)
			}
			if *ba.AverageServiceMinutes != 30 {
				t.Errorf("Expected Barber B avg service = 30, got %d", *ba.AverageServiceMinutes)
			}
		} else {
			t.Errorf("Unexpected barber ID %s in breakdown", ba.BarberId.String())
		}
	}

	if revenueSum != int64(resp.TotalRevenuePaise) {
		t.Errorf("Sum of barber revenue (%d) does not match total revenue (%d)", revenueSum, resp.TotalRevenuePaise)
	}
}

func TestGetStaffMembers(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	defer pool.Close()

	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM queue_entries")
		_, _ = pool.Exec(ctx, "DELETE FROM visits")
		_, _ = pool.Exec(ctx, "DELETE FROM queue_sessions")
		_, _ = pool.Exec(ctx, "DELETE FROM staff_members")
		_, _ = pool.Exec(ctx, "DELETE FROM locations")
		_, _ = pool.Exec(ctx, "DELETE FROM tenants")
	})

	// Seed another active staff member in the same location
	staffID2 := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active) VALUES ($1, $2, $3, 'Active Staff 2', '+919999999998', 'barber', true)", staffID2, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed active staff member 2: %v", err)
	}

	// Seed an inactive staff member in the same location
	staffID3 := uuid.New()
	_, err = pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active) VALUES ($1, $2, $3, 'Inactive Staff 3', '+919999999997', 'barber', false)", staffID3, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed inactive staff member 3: %v", err)
	}

	// Seed a staff member in another tenant/location for isolation check
	otherTenantID := uuid.New()
	otherLocationID := uuid.New()
	otherStaffID := uuid.New()
	_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Other Tenant', 'other-tenant', $2)", otherTenantID, "+918888888888")
	if err != nil {
		t.Fatalf("Failed to seed other tenant: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'Other Location', 'other-location')", otherLocationID, otherTenantID)
	if err != nil {
		t.Fatalf("Failed to seed other location: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active) VALUES ($1, $2, $3, 'Other Tenant Staff', '+918888888887', 'barber', true)", otherStaffID, otherTenantID, otherLocationID)
	if err != nil {
		t.Fatalf("Failed to seed other tenant staff: %v", err)
	}

	// Seed a queue session for location
	var sessionID uuid.UUID
	err = pool.QueryRow(ctx, "INSERT INTO queue_sessions (tenant_id, location_id, business_date, status) VALUES ($1, $2, NOW()::date, 'active') RETURNING id", tenantID, locationID).Scan(&sessionID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	// Seed active queue entry for staffID (called status)
	var visitID1 uuid.UUID
	err = pool.QueryRow(ctx, "INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes) VALUES ($1, $2, 'walk_in', 30) RETURNING id", tenantID, locationID).Scan(&visitID1)
	if err != nil {
		t.Fatalf("Failed to seed visit 1: %v", err)
	}
	var entryID1 uuid.UUID
	err = pool.QueryRow(ctx, "INSERT INTO queue_entries (queue_session_id, visit_id, state, token_number, assigned_barber_id) VALUES ($1, $2, 'called', 1, $3) RETURNING id", sessionID, visitID1, staffID).Scan(&entryID1)
	if err != nil {
		t.Fatalf("Failed to seed active queue entry: %v", err)
	}

	// Seed terminal queue entry for staffID2 (completed status)
	var visitID2 uuid.UUID
	err = pool.QueryRow(ctx, "INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes) VALUES ($1, $2, 'walk_in', 30) RETURNING id", tenantID, locationID).Scan(&visitID2)
	if err != nil {
		t.Fatalf("Failed to seed visit 2: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO queue_entries (queue_session_id, visit_id, state, token_number, assigned_barber_id) VALUES ($1, $2, 'completed', 2, $3)", sessionID, visitID2, staffID2)
	if err != nil {
		t.Fatalf("Failed to seed completed queue entry: %v", err)
	}

	t.Run("200 OK with matching staff members and current_entry_id verification", func(t *testing.T) {
		req := newStaffRequestWithRole(http.MethodGet, "/v1/staff/members", tenantID, locationID, staffID, "manager")
		rec := httptest.NewRecorder()

		s.GetStaffMembers(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Staff []StaffMember `json:"staff"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}

		// Active staff member 1 and 2 should be returned, inactive 3 and other tenant staff should be excluded
		if len(resp.Staff) != 2 {
			t.Fatalf("Expected exactly 2 staff members, got %d", len(resp.Staff))
		}

		// First staff member (seeded by setupTestServer) should have the active entryID1
		if resp.Staff[0].Id != staffID {
			t.Errorf("Expected first staff member ID %s, got %s", staffID, resp.Staff[0].Id)
		}
		if resp.Staff[0].CurrentEntryId == nil {
			t.Errorf("Expected first staff member to have an active queue entry")
		} else if *resp.Staff[0].CurrentEntryId != entryID1 {
			t.Errorf("Expected current_entry_id %s, got %s", entryID1, *resp.Staff[0].CurrentEntryId)
		}

		// Second staff member (staffID2) has completed entry, should have nil CurrentEntryId
		if resp.Staff[1].Id != staffID2 {
			t.Errorf("Expected second staff member ID %s, got %s", staffID2, resp.Staff[1].Id)
		}
		if resp.Staff[1].CurrentEntryId != nil {
			t.Errorf("Expected second staff member CurrentEntryId to be nil, got %s", *resp.Staff[1].CurrentEntryId)
		}
	})

	t.Run("401 Unauthorized on missing context claims", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/staff/members", nil)
		rec := httptest.NewRecorder()

		s.GetStaffMembers(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Expected 401 Unauthorized, got %d. Response: %s", rec.Code, rec.Body.String())
		}
	})
}



