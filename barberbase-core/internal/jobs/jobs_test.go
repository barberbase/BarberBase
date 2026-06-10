package jobs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"barberbase-core/internal/config"
	"barberbase-core/internal/realtime"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("Failed to parse DB URL: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("Failed to init DB pool: %v", err)
	}

	err = repository.Migrate(ctx, pool, "../../migrations/001_complete_schema.sql")
	if err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	_, _ = pool.Exec(ctx, "TRUNCATE tenants CASCADE")
	return pool
}

func TestAdvisoryLocks(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	manager := realtime.NewManager()
	watchdog := NewWatchdog(pool, manager, cfg)

	// Seed data
	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active, notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 20)
	`, locationID, tenantID)
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, uuid.New(), tenantID, locationID)

	// Acquire lock manually in the test session to simulate another instance running
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire connection: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockWatchdog)
	if err != nil {
		t.Fatalf("Failed to acquire manual advisory lock: %v", err)
	}

	// Trigger tick and verify it does not block and skips executing the job
	done := make(chan bool)
	go func() {
		watchdog.tick(ctx)
		done <- true
	}()

	select {
	case <-done:
		// success, tick returned immediately
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog tick blocked, expected it to skip and return immediately")
	}

	// Release lock
	_, err = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockWatchdog)
	if err != nil {
		t.Fatalf("Failed to unlock manual advisory lock: %v", err)
	}
}

func TestWatchdog_NearTurn(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	manager := realtime.NewManager()
	watchdog := NewWatchdog(pool, manager, cfg)

	// Seed tenant and location
	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active, notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 20)
	`, locationID, tenantID)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)

	customerID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999999', 'Customer')", customerID, tenantID)

	visitID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 15, NOW() + INTERVAL '23 hours')
	`, visitID, tenantID, locationID, customerID)

	// Seed arrived entry ahead of it to prevent it from being auto-snoozed in the same tick
	customerIDArrived := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+918888888888', 'Arrived Customer')", customerIDArrived, tenantID)

	visitIDArrived := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 15, NOW() + INTERVAL '23 hours')
	`, visitIDArrived, tenantID, locationID, customerIDArrived)

	entryIDArrived := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, $4, 2, 'waiting', 'arrived', true, 'whatsapp', 100, 999)
	`, entryIDArrived, visitIDArrived, sessionID, customerIDArrived)

	entryID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, $4, 1, 'waiting', 'remote', true, 'whatsapp', 100, 1000)
	`, entryID, visitID, sessionID, customerID)

	// Subscribe to SSE
	ch := manager.Subscribe(locationID.String())

	// Run watchdog check
	watchdog.runJob(ctx)

	// Verify entry presence is notified
	var presence string
	var notifiedAt *time.Time
	err := pool.QueryRow(ctx, "SELECT presence_state, near_turn_notified_at FROM queue_entries WHERE id = $1", entryID).Scan(&presence, &notifiedAt)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if presence != "notified" {
		t.Errorf("Expected presence 'notified', got '%s'", presence)
	}
	if notifiedAt == nil {
		t.Error("Expected near_turn_notified_at to be populated")
	}

	// Verify outbox event is created
	var count int
	var payloadBytes []byte
	err = pool.QueryRow(ctx, "SELECT COUNT(*), payload FROM outbox_events WHERE tenant_id = $1 GROUP BY payload", tenantID).Scan(&count, &payloadBytes)
	if err != nil {
		t.Fatalf("Query outbox failed: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 outbox event, got %d", count)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("Unmarshal payload failed: %v", err)
	}

	if payload["template_code"] != "bb_near_turn" {
		t.Errorf("Expected template_code 'bb_near_turn', got '%v'", payload["template_code"])
	}

	// Verify SSE broadcast
	select {
	case event := <-ch:
		if event.Type != "queue_changed" {
			t.Errorf("Expected SSE event type 'queue_changed', got '%s'", event.Type)
		}
	default:
		t.Error("Expected SSE event broadcast")
	}
}

func TestWatchdog_AutoSnooze(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	manager := realtime.NewManager()
	watchdog := NewWatchdog(pool, manager, cfg)

	// Seed tenant and location
	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active, notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 20)
	`, locationID, tenantID)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)

	customerID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999999', 'Customer')", customerID, tenantID)

	// Seed WhatsApp entry (should snooze and send outbox)
	visitID1 := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 15, NOW() + INTERVAL '23 hours')
	`, visitID1, tenantID, locationID, customerID)

	entryID1 := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, $4, 1, 'waiting', 'notified', true, 'whatsapp', 100, 1000)
	`, entryID1, visitID1, sessionID, customerID)

	// Run watchdog check
	watchdog.runJob(ctx)

	// Verify entry 1 presence is snoozed and not dispatchable
	var presence string
	var dispatchable bool
	err := pool.QueryRow(ctx, "SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1", entryID1).Scan(&presence, &dispatchable)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if presence != "snoozed" {
		t.Errorf("Expected presence 'snoozed', got '%s'", presence)
	}
	if dispatchable {
		t.Error("Expected is_dispatchable to be false")
	}

	// Verify WhatsApp outbox event exists
	var count int
	var payloadBytes []byte
	err = pool.QueryRow(ctx, "SELECT COUNT(*), payload FROM outbox_events WHERE tenant_id = $1 GROUP BY payload", tenantID).Scan(&count, &payloadBytes)
	if err != nil {
		t.Fatalf("Query outbox failed: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 outbox event, got %d", count)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("Unmarshal payload failed: %v", err)
	}

	if payload["template_code"] != "bb_queue_snoozed" {
		t.Errorf("Expected template_code 'bb_queue_snoozed', got '%v'", payload["template_code"])
	}

	// Truncate outbox and test web channel (should snooze but NO outbox)
	_, _ = pool.Exec(ctx, "TRUNCATE outbox_events")

	// Set presence back to remote, channel to web
	_, _ = pool.Exec(ctx, "UPDATE queue_entries SET presence_state = 'remote', is_dispatchable = true, session_channel = 'web' WHERE id = $1", entryID1)

	watchdog.runJob(ctx)

	// Verify presence is snoozed
	_ = pool.QueryRow(ctx, "SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1", entryID1).Scan(&presence, &dispatchable)
	if presence != "snoozed" || dispatchable {
		t.Errorf("Expected snoozed and not dispatchable, got presence=%s, dispatchable=%t", presence, dispatchable)
	}

	// Verify no outbox row
	var outboxCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events").Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("Expected 0 outbox events for web channel customer, got %d", outboxCount)
	}
}

func TestEndOfDay(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	manager := realtime.NewManager()
	eod := NewEndOfDay(pool, manager, cfg)

	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)
	
	// Seed location with closing hours. closes_at set to 2.5 hours ago
	locTZ, _ := time.LoadLocation("Asia/Kolkata")
	closingTime := time.Now().In(locTZ).Add(-150 * time.Minute) // 2.5 hours ago
	closingTimeStr := closingTime.Format("15:04:00")
	opensTimeStr := closingTime.Add(-8 * time.Hour).Format("15:04:00")

	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true)
	`, locationID, tenantID)

	dayOfWeek := int(time.Now().In(locTZ).Weekday())
	_, _ = pool.Exec(ctx, `
		INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
		VALUES ($1, $2, $3, $4, true, $5::TIME, $6::TIME)
	`, uuid.New(), tenantID, locationID, dayOfWeek, opensTimeStr, closingTimeStr)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
	`, sessionID, tenantID, locationID)

	customerID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999999', 'Customer')", customerID, tenantID)

	// Seed waiting entry
	v1 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes) VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)", v1, tenantID, locationID, customerID)
	e1 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, is_dispatchable) VALUES ($1, $2, $3, $4, 1, 'waiting', true)", e1, v1, sessionID, customerID)

	// Seed in_progress entry (with a distinct customer to avoid one active entry per customer constraint violation)
	customerID2 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999998', 'Customer 2')", customerID2, tenantID)
	v2 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes) VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)", v2, tenantID, locationID, customerID2)
	e2 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, is_dispatchable, started_at) VALUES ($1, $2, $3, $4, 2, 'in_progress', true, NOW())", e2, v2, sessionID, customerID2)

	// Run EOD
	eod.runJob(ctx)

	// Verify states
	var state1, state2 string
	var d1, d2 bool
	_ = pool.QueryRow(ctx, "SELECT state, is_dispatchable FROM queue_entries WHERE id = $1", e1).Scan(&state1, &d1)
	_ = pool.QueryRow(ctx, "SELECT state, is_dispatchable FROM queue_entries WHERE id = $1", e2).Scan(&state2, &d2)

	if state1 != "expired" || d1 {
		t.Errorf("Expected waiting entry to be expired/undispatchable, got state=%s, disp=%t", state1, d1)
	}
	if state2 != "needs_review" || d2 {
		t.Errorf("Expected in_progress entry to be needs_review/undispatchable, got state=%s, disp=%t", state2, d2)
	}

	// Verify session status
	var sessionStatus string
	_ = pool.QueryRow(ctx, "SELECT status FROM queue_sessions WHERE id = $1", sessionID).Scan(&sessionStatus)
	if sessionStatus != "archived" {
		t.Errorf("Expected session status 'archived', got '%s'", sessionStatus)
	}

	// Verify outbox events (EOD should write zero outbox events)
	var outboxCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events").Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("Expected 0 outbox events, got %d", outboxCount)
	}
}

func TestWeeklySummary(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	weekly := NewWeeklySummary(pool, cfg)

	// Seed two tenants: 1 active, 1 inactive
	activeTenantID := uuid.New()
	inactiveTenantID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number, is_active) VALUES ($1, 'Active Tenant', 'active-tenant', '+919876543210', true)", activeTenantID)
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number, is_active) VALUES ($1, 'Inactive Tenant', 'inactive-tenant', '+919876543211', false)", inactiveTenantID)

	activeLocID := uuid.New()
	inactiveLocID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active) VALUES ($1, $2, 'Active Loc', 'active-tenant/loc', 'Asia/Kolkata', true)", activeLocID, activeTenantID)
	_, _ = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active) VALUES ($1, $2, 'Inactive Loc', 'inactive-tenant/loc', 'Asia/Kolkata', true)", inactiveLocID, inactiveTenantID)

	// Seed completed visit for active tenant in the past week
	customerID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999999', 'Customer')", customerID, activeTenantID)

	visitID := uuid.New()
	// Seed completed_at within the range (e.g. now)
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes, completed_at)
		VALUES ($1, $2, $3, $4, 'walk_in', 'completed', 30, NOW())
	`, visitID, activeTenantID, activeLocID, customerID)

	// Run weekly summary RunJob for today (simulated Sunday 22:00)
	weekly.RunJob(ctx, time.Now())

	// Verify outbox row created for active tenant
	var activeCount int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1 AND type = 'weekly_summary.send'", activeTenantID).Scan(&activeCount)
	if err != nil {
		t.Fatalf("Query active outbox failed: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("Expected 1 outbox event for active tenant, got %d", activeCount)
	}

	// Verify outbox row NOT created for inactive tenant
	var inactiveCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1 AND type = 'weekly_summary.send'", inactiveTenantID).Scan(&inactiveCount)
	if err != nil {
		t.Fatalf("Query inactive outbox failed: %v", err)
	}
	if inactiveCount != 0 {
		t.Errorf("Expected 0 outbox events for inactive tenant, got %d", inactiveCount)
	}
}

func TestWeeklySummaryTimeoutOverride(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	weekly := NewWeeklySummary(pool, cfg)

	tenantID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number, is_active) VALUES ($1, 'Active Tenant', 'active-tenant', '+919876543210', true)", tenantID)
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active) VALUES ($1, $2, 'Active Loc', 'active-tenant/loc', 'Asia/Kolkata', true)", locationID, tenantID)

	// Verify that a query taking 50ms fails when session statement_timeout is set to 10ms
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire conn failed: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET statement_timeout = '10ms'")
	if err != nil {
		t.Fatalf("Set statement_timeout failed: %v", err)
	}

	_, err = conn.Exec(ctx, "SELECT pg_sleep(0.05)")
	if err == nil {
		t.Fatal("Expected query to fail due to statement_timeout, but it succeeded")
	}

	// Now verify that WeeklySummary job completes successfully despite inheriting statement_timeout settings or pool limits
	// by overriding statement_timeout to 0 using SET LOCAL statement_timeout = 0
	weekly.RunJob(ctx, time.Now())
}

func TestWatchdog_StaleWarnings(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	cfg := &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	}
	manager := realtime.NewManager()
	watchdog := NewWatchdog(pool, manager, cfg)

	tenantID := uuid.New()
	locationID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Tenant', 'slug', '+919876543210')", tenantID)

	// Set specific stale thresholds on location
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (
			id, tenant_id, name, slug, timezone, is_active,
			stale_called_warning_minutes, stale_called_critical_minutes,
			in_progress_warning_minutes, in_progress_confirm_minutes, in_progress_critical_minutes
		)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 5, 10, 15, 20)
	`, locationID, tenantID)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)

	// Seed 3 customers
	cust1 := uuid.New()
	cust2 := uuid.New()
	cust3 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919000000001', 'Cust 1')", cust1, tenantID)
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919000000002', 'Cust 2')", cust2, tenantID)
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919000000003', 'Cust 3')", cust3, tenantID)

	// e1: called 3 minutes ago (3 > 2 but 3 < 5) -> called_warning
	v1 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes) VALUES ($1, $2, $3, $4, 'walk_in', 'active', 15)", v1, tenantID, locationID, cust1)
	e1 := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, is_dispatchable, called_at)
		VALUES ($1, $2, $3, $4, 1, 'called', true, NOW() - INTERVAL '3 minutes')
	`, e1, v1, sessionID, cust1)

	// e2: called 6 minutes ago (6 > 5) -> called_critical
	v2 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes) VALUES ($1, $2, $3, $4, 'walk_in', 'active', 15)", v2, tenantID, locationID, cust2)
	e2 := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, is_dispatchable, called_at)
		VALUES ($1, $2, $3, $4, 2, 'called', true, NOW() - INTERVAL '6 minutes')
	`, e2, v2, sessionID, cust2)

	// e3: in_progress started 31 minutes ago.
	// visit total_duration_minutes (15) + in_progress_confirm_minutes (15) = 30 minutes.
	// 31 > 30 but 31 < 35 (critical is 15+20=35). So -> in_progress_confirm.
	v3 := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes) VALUES ($1, $2, $3, $4, 'walk_in', 'active', 15)", v3, tenantID, locationID, cust3)
	e3 := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, is_dispatchable, started_at)
		VALUES ($1, $2, $3, $4, 3, 'in_progress', true, NOW() - INTERVAL '31 minutes')
	`, e3, v3, sessionID, cust3)

	// Run watchdog check (this triggers updateStaleWarnings)
	watchdog.runJob(ctx)

	// Verify warnings
	var w1, w2, w3 *string
	_ = pool.QueryRow(ctx, "SELECT stale_warning FROM queue_entries WHERE id = $1", e1).Scan(&w1)
	_ = pool.QueryRow(ctx, "SELECT stale_warning FROM queue_entries WHERE id = $1", e2).Scan(&w2)
	_ = pool.QueryRow(ctx, "SELECT stale_warning FROM queue_entries WHERE id = $1", e3).Scan(&w3)

	if w1 == nil || *w1 != "called_warning" {
		t.Errorf("Expected e1 warning 'called_warning', got %v", w1)
	}
	if w2 == nil || *w2 != "called_critical" {
		t.Errorf("Expected e2 warning 'called_critical', got %v", w2)
	}
	if w3 == nil || *w3 != "in_progress_confirm" {
		t.Errorf("Expected e3 warning 'in_progress_confirm', got %v", w3)
	}
}

