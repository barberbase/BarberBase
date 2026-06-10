package repository_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

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

func TestQuotaEnforcement_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant with monthly_marketing_quota=2
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number, monthly_marketing_quota, monthly_transactional_quota)
		VALUES ($1, 'Limit Tenant', 'limit-tenant', '+919876543212', 2, 1000)
	`, tenantID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Limit Location', 'limit-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	require.NoError(t, err)

	mockBhejna := &mockBhejnaClient{}
	handler := notification.NewHandler(pool, mockBhejna)

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_marketing_broadcast",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "marketing_broadcast",
	}
	payloadBytes, _ := json.Marshal(payload)

	tenantIDStr := tenantID.String()

	// Dispatch 1st marketing event -> succeeds
	evt1 := &notification.OutboxEvent{
		ID:          uuid.New().String(),
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}
	err = handler.Handle(ctx, pool, evt1)
	require.NoError(t, err)

	// Dispatch 2nd marketing event -> succeeds
	evt2 := &notification.OutboxEvent{
		ID:          uuid.New().String(),
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}
	err = handler.Handle(ctx, pool, evt2)
	require.NoError(t, err)

	// Dispatch 3rd marketing event -> blocked (terminal error)
	evt3 := &notification.OutboxEvent{
		ID:          uuid.New().String(),
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}
	err = handler.Handle(ctx, pool, evt3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota_exhausted")

	// Verify notification_events status='blocked_quota'
	var notifStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM notification_events WHERE source_id = $1 AND source_type = 'outbox_event'", evt3.ID).Scan(&notifStatus)
	require.NoError(t, err)
	require.Equal(t, "blocked_quota", notifStatus)

	// Dispatch a concurrent bb_you_are_next event for same tenant dispatches normally
	payloadTx := notification.NotificationPayload{
		TemplateCode:     "bb_you_are_next",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "you_are_next",
	}
	payloadTxBytes, _ := json.Marshal(payloadTx)
	evtTx := &notification.OutboxEvent{
		ID:          uuid.New().String(),
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadTxBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}
	err = handler.Handle(ctx, pool, evtTx)
	require.NoError(t, err)
}

func TestQuotaEnforcement_Concurrent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant with monthly_marketing_quota=100
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number, monthly_marketing_quota, monthly_transactional_quota)
		VALUES ($1, 'Concurrent Tenant', 'concurrent-tenant', '+919876543213', 100, 1000)
	`, tenantID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Concurrent Location', 'concurrent-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	require.NoError(t, err)

	mockBhejna := &mockBhejnaClient{}
	handler := notification.NewHandler(pool, mockBhejna)

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_marketing_broadcast",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "marketing_broadcast",
	}
	payloadBytes, _ := json.Marshal(payload)

	tenantIDStr := tenantID.String()

	N := 20
	var wg sync.WaitGroup
	wg.Add(N)

	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			evt := &notification.OutboxEvent{
				ID:          uuid.New().String(),
				TenantID:    &tenantIDStr,
				Type:        "notification.send",
				Payload:     payloadBytes,
				Attempts:    1,
				MaxAttempts: 3,
			}
			err := handler.Handle(ctx, pool, evt)
			if err != nil {
				t.Errorf("Handle failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// Verify used_count is exactly N
	var usedCount int
	err = pool.QueryRow(ctx, "SELECT used_count FROM tenant_quota_periods WHERE tenant_id = $1 AND quota_type = 'whatsapp_marketing'", tenantID).Scan(&usedCount)
	require.NoError(t, err)
	require.Equal(t, N, usedCount)
}

func TestQuotaEnforcement_CrashIdempotency(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()

	// Seed Tenant
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number, monthly_marketing_quota, monthly_transactional_quota)
		VALUES ($1, 'Crash Tenant', 'crash-tenant', '+919876543214', 100, 1000)
	`, tenantID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Crash Location', 'crash-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	require.NoError(t, err)

	// Insert period row
	var periodID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO tenant_quota_periods (id, tenant_id, quota_type, period_start, period_end, included_limit, used_count)
		VALUES (gen_random_uuid(), $1, 'whatsapp_marketing', date_trunc('month', NOW())::DATE, (date_trunc('month', NOW()) + INTERVAL '1 month' - INTERVAL '1 day')::DATE, 100, 1)
		RETURNING id
	`, tenantID).Scan(&periodID)
	require.NoError(t, err)

	// Insert pre-existing ledger row representing a crashed/partial try
	evtID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO quota_usage_ledger (id, tenant_id, quota_type, quota_period_id, usage_count, source_type, source_id, idempotency_key)
		VALUES (gen_random_uuid(), $1, 'whatsapp_marketing', $2, 1, 'outbox_event', $3, $4)
	`, tenantID, periodID, evtID, evtID.String())
	require.NoError(t, err)

	mockBhejna := &mockBhejnaClient{}
	handler := notification.NewHandler(pool, mockBhejna)

	payload := notification.NotificationPayload{
		TemplateCode:     "bb_marketing_broadcast",
		To:               "+919876543210",
		LocationID:       locationID.String(),
		Language:         "en",
		Components:       []json.RawMessage{},
		NotificationType: "marketing_broadcast",
	}
	payloadBytes, _ := json.Marshal(payload)

	tenantIDStr := tenantID.String()
	evt := &notification.OutboxEvent{
		ID:          evtID.String(),
		TenantID:    &tenantIDStr,
		Type:        "notification.send",
		Payload:     payloadBytes,
		Attempts:    1,
		MaxAttempts: 3,
	}

	// Process the event
	err = handler.Handle(ctx, pool, evt)
	require.NoError(t, err)

	// Verify used_count is STILL 1 (did not double increment to 2)
	var usedCount int
	err = pool.QueryRow(ctx, "SELECT used_count FROM tenant_quota_periods WHERE id = $1", periodID).Scan(&usedCount)
	require.NoError(t, err)
	require.Equal(t, 1, usedCount)
}
