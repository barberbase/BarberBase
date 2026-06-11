package notification

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"barberbase-core/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/SherClockHolmes/webpush-go"
)

type mockHTTPClient struct {
	doFunc func(*http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.doFunc != nil {
		return m.doFunc(req)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}, nil
}

func seedTenantAndLocation(t *testing.T, pool *pgxpool.Pool, ctx context.Context, tenantID, locationID uuid.UUID) {
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Test Tenant', 'test-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Test Location', 'test-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}
}

func generateValidKeys(t *testing.T) (string, string) {
	_, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("Failed to generate VAPID keys for subscription mock: %v", err)
	}
	auth := base64.RawURLEncoding.EncodeToString([]byte("1234567890123456"))
	return publicKey, auth
}

func setupTestConfig(t *testing.T) *config.Config {
	privKey, pubKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("Failed to generate VAPID keys: %v", err)
	}
	return &config.Config{
		HMACSecret:      "test_hmac_secret_must_be_long_enough_32_chars",
		VAPIDSubject:    "mailto:ops@barberbase.in",
		VAPIDPublicKey:  pubKey,
		VAPIDPrivateKey: privKey,
	}
}

func TestHandleWebPushSend_ZeroArrived(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	// Seed push-enabled staff member
	staffID := uuid.New()
	p256dh, auth := generateValidKeys(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber John', '+919999988888', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint1', $4, $5)
	`, staffID, tenantID, locationID, p256dh, auth)
	if err != nil {
		t.Fatalf("Failed to seed staff: %v", err)
	}

	payload := WebPushSendPayload{
		LocationID: locationID.String(),
		TenantID:   tenantID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:      outboxEventID,
		Type:    "web_push.send",
		Payload: payloadBytes,
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status)
		VALUES ($1, $2, 'web_push.send', $3, 'processing')
	`, outboxEventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	reqCount := 0
	mockClient := &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			reqCount++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		},
	}

	cfg := setupTestConfig(t)

	handler := &PushHandler{
		Pool:       pool,
		Config:     cfg,
		HTTPClient: mockClient,
	}

	err = handler.HandleWebPushSend(ctx, event)
	if err != nil {
		t.Fatalf("HandleWebPushSend failed: %v", err)
	}

	if reqCount != 0 {
		t.Errorf("Expected 0 push requests, got %d", reqCount)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox_events WHERE id = $1", outboxEventID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected outbox status 'dispatched', got %q", status)
	}
}

func TestHandleWebPushSend_410Gone(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	sessionID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+918888777766', 'Alice')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 'waiting', 'arrived', true)
	`, visitID, sessionID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed queue entry: %v", err)
	}

	staffID := uuid.New()
	p256dh, auth := generateValidKeys(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber John', '+919999988888', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint1', $4, $5)
	`, staffID, tenantID, locationID, p256dh, auth)
	if err != nil {
		t.Fatalf("Failed to seed staff: %v", err)
	}

	payload := WebPushSendPayload{
		LocationID: locationID.String(),
		TenantID:   tenantID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:      outboxEventID,
		Type:    "web_push.send",
		Payload: payloadBytes,
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status)
		VALUES ($1, $2, 'web_push.send', $3, 'processing')
	`, outboxEventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockClient := &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusGone, // 410
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		},
	}

	cfg := setupTestConfig(t)

	handler := &PushHandler{
		Pool:       pool,
		Config:     cfg,
		HTTPClient: mockClient,
	}

	err = handler.HandleWebPushSend(ctx, event)
	if err != nil {
		t.Fatalf("HandleWebPushSend failed: %v", err)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox_events WHERE id = $1", outboxEventID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected outbox status 'dispatched', got %q", status)
	}

	var pushEnabled bool
	var dbEndpoint, dbP256dh, dbAuth *string
	err = pool.QueryRow(ctx, `
		SELECT push_enabled, push_endpoint, push_p256dh, push_auth
		FROM staff_members WHERE id = $1
	`, staffID).Scan(&pushEnabled, &dbEndpoint, &dbP256dh, &dbAuth)
	if err != nil {
		t.Fatalf("Failed to query staff_members: %v", err)
	}
	if pushEnabled {
		t.Errorf("Expected staff.push_enabled to be false")
	}
	if dbEndpoint != nil || dbP256dh != nil || dbAuth != nil {
		t.Errorf("Expected staff push columns to be NULL, got %v, %v, %v", dbEndpoint, dbP256dh, dbAuth)
	}

	var notifStatus, errorMsg, channel, sourceType string
	var sourceID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT status, error_message, channel, source_type, source_id
		FROM notification_events WHERE source_id = $1
	`, staffID).Scan(&notifStatus, &errorMsg, &channel, &sourceType, &sourceID)
	if err != nil {
		t.Fatalf("Failed to query notification_events: %v", err)
	}
	if notifStatus != "failed" {
		t.Errorf("Expected status='failed', got %q", notifStatus)
	}
	if errorMsg != "410_gone" {
		t.Errorf("Expected error_message='410_gone', got %q", errorMsg)
	}
	if channel != "web_push" {
		t.Errorf("Expected channel='web_push', got %q", channel)
	}
	if sourceType != "staff_member" {
		t.Errorf("Expected source_type='staff_member', got %q", sourceType)
	}
}

func TestHandleWebPushSend_2xxResponse(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	sessionID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+918888777766', 'Alice')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 'waiting', 'arrived', true)
	`, visitID, sessionID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed queue entry: %v", err)
	}

	staffID := uuid.New()
	p256dh, auth := generateValidKeys(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber John', '+919999988888', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint1', $4, $5)
	`, staffID, tenantID, locationID, p256dh, auth)
	if err != nil {
		t.Fatalf("Failed to seed staff: %v", err)
	}

	payload := WebPushSendPayload{
		LocationID: locationID.String(),
		TenantID:   tenantID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:      outboxEventID,
		Type:    "web_push.send",
		Payload: payloadBytes,
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status)
		VALUES ($1, $2, 'web_push.send', $3, 'processing')
	`, outboxEventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockClient := &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		},
	}

	cfg := setupTestConfig(t)

	handler := &PushHandler{
		Pool:       pool,
		Config:     cfg,
		HTTPClient: mockClient,
	}

	err = handler.HandleWebPushSend(ctx, event)
	if err != nil {
		t.Fatalf("HandleWebPushSend failed: %v", err)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox_events WHERE id = $1", outboxEventID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected outbox status 'dispatched', got %q", status)
	}

	var notifStatus, channel, sourceType string
	var custID *uuid.UUID
	var sourceID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT status, channel, source_type, source_id, customer_id
		FROM notification_events WHERE source_id = $1
	`, staffID).Scan(&notifStatus, &channel, &sourceType, &sourceID, &custID)
	if err != nil {
		t.Fatalf("Failed to query notification_events: %v", err)
	}
	if notifStatus != "sent" {
		t.Errorf("Expected status='sent', got %q", notifStatus)
	}
	if channel != "web_push" {
		t.Errorf("Expected channel='web_push', got %q", channel)
	}
	if sourceType != "staff_member" {
		t.Errorf("Expected source_type='staff_member', got %q", sourceType)
	}
	if custID != nil {
		t.Errorf("Expected customer_id to be NULL, got %v", custID)
	}
}

func TestHandleWebPushSend_QuotaBypass(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	sessionID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+918888777766', 'Alice')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 'waiting', 'arrived', true)
	`, visitID, sessionID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed queue entry: %v", err)
	}

	staffID := uuid.New()
	p256dh, auth := generateValidKeys(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber John', '+919999988888', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint1', $4, $5)
	`, staffID, tenantID, locationID, p256dh, auth)
	if err != nil {
		t.Fatalf("Failed to seed staff: %v", err)
	}

	payload := WebPushSendPayload{
		LocationID: locationID.String(),
		TenantID:   tenantID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:      outboxEventID,
		Type:    "web_push.send",
		Payload: payloadBytes,
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status)
		VALUES ($1, $2, 'web_push.send', $3, 'processing')
	`, outboxEventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockClient := &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		},
	}

	cfg := setupTestConfig(t)

	handler := &PushHandler{
		Pool:       pool,
		Config:     cfg,
		HTTPClient: mockClient,
	}

	err = handler.HandleWebPushSend(ctx, event)
	if err != nil {
		t.Fatalf("HandleWebPushSend failed: %v", err)
	}

	var countQuotaPeriods, countQuotaLedger int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM tenant_quota_periods").Scan(&countQuotaPeriods)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM quota_usage_ledger").Scan(&countQuotaLedger)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if countQuotaPeriods != 0 || countQuotaLedger != 0 {
		t.Errorf("Expected quota tables to be untouched, got periods=%d ledger=%d", countQuotaPeriods, countQuotaLedger)
	}
}

func TestHandleWebPushSend_Non410PerStaffFailureDoesNotAbort(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	sessionID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+918888777766', 'Alice')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 'waiting', 'arrived', true)
	`, visitID, sessionID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed queue entry: %v", err)
	}

	staffID1 := uuid.New()
	p256dh1, auth1 := generateValidKeys(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber One', '+919999988888', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint1', $4, $5)
	`, staffID1, tenantID, locationID, p256dh1, auth1)
	if err != nil {
		t.Fatalf("Failed to seed staff 1: %v", err)
	}

	staffID2 := uuid.New()
	p256dh2, auth2 := generateValidKeys(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active, push_enabled, push_endpoint, push_p256dh, push_auth)
		VALUES ($1, $2, $3, 'Barber Two', '+919999977777', 'barber', 'idle', true, true, 'https://fcm.googleapis.com/fcm/send/endpoint2', $4, $5)
	`, staffID2, tenantID, locationID, p256dh2, auth2)
	if err != nil {
		t.Fatalf("Failed to seed staff 2: %v", err)
	}

	payload := WebPushSendPayload{
		LocationID: locationID.String(),
		TenantID:   tenantID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:      outboxEventID,
		Type:    "web_push.send",
		Payload: payloadBytes,
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status)
		VALUES ($1, $2, 'web_push.send', $3, 'processing')
	`, outboxEventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockClient := &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.URL.String() == "https://fcm.googleapis.com/fcm/send/endpoint1" {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		},
	}

	cfg := setupTestConfig(t)

	handler := &PushHandler{
		Pool:       pool,
		Config:     cfg,
		HTTPClient: mockClient,
	}

	err = handler.HandleWebPushSend(ctx, event)
	if err != nil {
		t.Fatalf("HandleWebPushSend failed: %v", err)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox_events WHERE id = $1", outboxEventID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected outbox status 'dispatched', got %q", status)
	}

	var notifStatus1 string
	err = pool.QueryRow(ctx, `
		SELECT status FROM notification_events WHERE source_id = $1
	`, staffID1).Scan(&notifStatus1)
	if err != nil {
		t.Fatalf("Failed to query notification_events for staff 1: %v", err)
	}
	if notifStatus1 != "failed" {
		t.Errorf("Expected staff 1 event status='failed', got %q", notifStatus1)
	}

	var notifStatus2 string
	err = pool.QueryRow(ctx, `
		SELECT status FROM notification_events WHERE source_id = $1
	`, staffID2).Scan(&notifStatus2)
	if err != nil {
		t.Fatalf("Failed to query notification_events for staff 2: %v", err)
	}
	if notifStatus2 != "sent" {
		t.Errorf("Expected staff 2 event status='sent', got %q", notifStatus2)
	}
}
