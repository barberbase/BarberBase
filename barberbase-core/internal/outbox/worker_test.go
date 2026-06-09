package outbox

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/bhejna"
	notification "barberbase-core/internal/outbox/handlers"
	"barberbase-core/internal/repository"
)

func getTestDatabaseURL() string {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}
	return url
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	connStr := getTestDatabaseURL()

	pool, err := repository.InitPool(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to initialize test DB pool: %v", err)
	}

	// Clean up database tables for a clean test run
	_, _ = pool.Exec(ctx, "TRUNCATE tenants CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE webhook_events CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE outbox_events CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE notification_events CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE tenant_quota_periods CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE quota_usage_ledger CASCADE")

	return pool
}

type mockBhejnaClient struct {
	sendTemplateFunc func(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTemplateReq) (*bhejna.SendResult, error)
}

func (m *mockBhejnaClient) SendText(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTextReq) (*bhejna.SendResult, error) {
	return &bhejna.SendResult{JobID: "txt_job_id"}, nil
}

func (m *mockBhejnaClient) SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTemplateReq) (*bhejna.SendResult, error) {
	if m.sendTemplateFunc != nil {
		return m.sendTemplateFunc(ctx, tenantID, locationID, req)
	}
	return &bhejna.SendResult{JobID: "tmpl_job_id"}, nil
}

// 1. quota_exhausted_marketing_does_not_block_transactional
func TestQuotaExhaustedMarketingDoesNotBlockTransactional(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number, monthly_marketing_quota, monthly_transactional_quota)
		VALUES ($1, 'Quota Tenant', 'quota-tenant', '+919876543211', 100, 1000)
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Quota Location', 'quota-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	// Seed Period at limit for marketing
	_, err = pool.Exec(ctx, `
		INSERT INTO tenant_quota_periods (id, tenant_id, quota_type, period_start, period_end, included_limit, used_count)
		VALUES (gen_random_uuid(), $1, 'whatsapp_marketing', date_trunc('month', NOW())::DATE, (date_trunc('month', NOW()) + INTERVAL '1 month' - INTERVAL '1 day')::DATE, 100, 100)
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed marketing period: %v", err)
	}

	// Insert transactional event
	payload := notification.NotificationPayload{
		TemplateCode:     "bb_queue_joined",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "queue_joined",
	}
	payloadBytes, _ := json.Marshal(payload)

	eventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status, max_attempts)
		VALUES ($1, $2, 'notification.send', $3, 'pending', 3)
	`, eventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockBhejna := &mockBhejnaClient{}
	w := NewWorker(pool, mockBhejna)

	err = w.processOne(ctx)
	if err != nil {
		t.Fatalf("processOne failed: %v", err)
	}

	// Verify the transactional event is dispatched
	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox_events WHERE id = $1", eventID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query event status: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected status 'dispatched', got %q", status)
	}
}

// 2. skip_locked_prevents_double_send
func TestSkipLockedPreventsDoubleSend(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Race Tenant', 'race-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Race Location', 'race-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_queue_joined",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "queue_joined",
	}
	payloadBytes, _ := json.Marshal(payload)

	eventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status, max_attempts)
		VALUES ($1, $2, 'notification.send', $3, 'pending', 3)
	`, eventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed outbox event: %v", err)
	}

	mockBhejna := &mockBhejnaClient{}
	w := NewWorker(pool, mockBhejna)

	var wg sync.WaitGroup
	var err1, err2 error
	wg.Add(2)

	go func() {
		defer wg.Done()
		err1 = w.processOne(ctx)
	}()
	go func() {
		defer wg.Done()
		err2 = w.processOne(ctx)
	}()
	wg.Wait()

	if err1 != nil {
		t.Errorf("worker 1 error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("worker 2 error: %v", err2)
	}

	// Verify attempts incremented exactly once (meaning only one worker processed it)
	var attempts int
	err = pool.QueryRow(ctx, "SELECT attempts FROM outbox_events WHERE id = $1", eventID).Scan(&attempts)
	if err != nil {
		t.Fatalf("Failed to query attempts: %v", err)
	}
	if attempts != 1 {
		t.Errorf("Expected attempts to be 1, got %d", attempts)
	}
}

// 3. lease_recovery
func TestLeaseRecovery(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Lease Tenant', 'lease-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Lease Location', 'lease-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_queue_joined",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "queue_joined",
	}
	payloadBytes, _ := json.Marshal(payload)

	// Seed event in 'processing' status but locked_until in the past
	eventID := uuid.New()
	pastTime := time.Now().Add(-60 * time.Second)
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status, locked_until, attempts, max_attempts)
		VALUES ($1, $2, 'notification.send', $3, 'processing', $4, 1, 3)
	`, eventID, tenantID.String(), payloadBytes, pastTime)
	if err != nil {
		t.Fatalf("Failed to seed stuck event: %v", err)
	}

	mockBhejna := &mockBhejnaClient{}
	w := NewWorker(pool, mockBhejna)

	err = w.processOne(ctx)
	if err != nil {
		t.Fatalf("processOne failed: %v", err)
	}

	// Verify event was claimed and status updated to dispatched
	var status string
	var attempts int
	err = pool.QueryRow(ctx, "SELECT status, attempts FROM outbox_events WHERE id = $1", eventID).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "dispatched" {
		t.Errorf("Expected status 'dispatched', got %q", status)
	}
	if attempts != 2 {
		t.Errorf("Expected attempts to increment to 2, got %d", attempts)
	}
}

// 4. terminal_not_claimed
func TestTerminalNotClaimed(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Term Tenant', 'term-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Term Location', 'term-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_queue_joined",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "queue_joined",
	}
	payloadBytes, _ := json.Marshal(payload)

	// Seed failed outbox event at max_attempts
	eventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, tenant_id, type, payload, status, attempts, max_attempts)
		VALUES ($1, $2, 'notification.send', $3, 'failed', 3, 3)
	`, eventID, tenantID.String(), payloadBytes)
	if err != nil {
		t.Fatalf("Failed to seed terminal event: %v", err)
	}

	mockBhejna := &mockBhejnaClient{}
	w := NewWorker(pool, mockBhejna)

	err = w.processOne(ctx)
	if err != nil {
		t.Fatalf("processOne failed: %v", err)
	}

	// Verify event was not claimed
	var status string
	var attempts int
	err = pool.QueryRow(ctx, "SELECT status, attempts FROM outbox_events WHERE id = $1", eventID).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("Failed to query outbox: %v", err)
	}
	if status != "failed" {
		t.Errorf("Expected status 'failed', got %q", status)
	}
	if attempts != 3 {
		t.Errorf("Expected attempts to remain 3, got %d", attempts)
	}
}

// 5. all_12_templates_accepted
func TestAll12TemplatesAccepted(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Tmpl Tenant', 'tmpl-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Tmpl Location', 'tmpl-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	mockBhejna := &mockBhejnaClient{}
	handler := notification.NewHandler(pool, mockBhejna)

	for code, notifType := range notification.TemplateToNotificationType {
		payload := notification.NotificationPayload{
			TemplateCode:     code,
			To:               "+919876543210",
			LocationID:       locationID.String(),
			Language:         "en",
			Components:       []json.RawMessage{},
			NotificationType: notifType,
		}
		payloadBytes, _ := json.Marshal(payload)

		tenantIDStr := tenantID.String()
		event := &notification.OutboxEvent{
			ID:          uuid.New().String(),
			TenantID:    &tenantIDStr,
			Type:        "notification.send",
			Payload:     payloadBytes,
			Attempts:    1,
			MaxAttempts: 3,
		}

		err = handler.Handle(ctx, pool, event)
		if err != nil {
			t.Errorf("Template %s failed with error: %v", code, err)
		}
	}
}

// 6. quota_ledger_idempotent
func TestQuotaLedgerIdempotent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Idem Tenant', 'idem-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Idem Location', 'idem-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_queue_joined",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "queue_joined",
	}
	payloadBytes, _ := json.Marshal(payload)

	tenantIDStr := tenantID.String()
	eventID := uuid.New().String()
	event := &notification.OutboxEvent{
		ID:          eventID,
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}

	mockBhejna := &mockBhejnaClient{}
	handler := notification.NewHandler(pool, mockBhejna)

	// First run
	err = handler.Handle(ctx, pool, event)
	if err != nil {
		t.Fatalf("First handle failed: %v", err)
	}

	// Verify used_count is 1
	var usedCount int
	err = pool.QueryRow(ctx, "SELECT used_count FROM tenant_quota_periods WHERE tenant_id = $1 AND quota_type = 'whatsapp_transactional'", tenantID).Scan(&usedCount)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if usedCount != 1 {
		t.Fatalf("Expected used_count to be 1, got %d", usedCount)
	}

	// Second run (simulate retry with same outbox event ID / idempotency key)
	err = handler.Handle(ctx, pool, event)
	if err != nil {
		t.Fatalf("Second handle failed: %v", err)
	}

	// Verify used_count is STILL 1
	err = pool.QueryRow(ctx, "SELECT used_count FROM tenant_quota_periods WHERE tenant_id = $1 AND quota_type = 'whatsapp_transactional'", tenantID).Scan(&usedCount)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if usedCount != 1 {
		t.Errorf("Expected used_count to remain 1 on retry, got %d", usedCount)
	}

	// Verify exactly one ledger row exists
	var ledgerCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM quota_usage_ledger WHERE idempotency_key = $1", eventID).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("Expected exactly 1 ledger row, got %d", ledgerCount)
	}
}
