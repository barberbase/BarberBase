package api

import (
	"bytes"
	"context"
	"encoding/json"
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
