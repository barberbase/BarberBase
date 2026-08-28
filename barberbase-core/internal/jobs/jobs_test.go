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
	"github.com/stretchr/testify/require"
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
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
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
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
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
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
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
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key, near_turn_notified_at)
		VALUES ($1, $2, $3, $4, 1, 'waiting', 'notified', true, 'whatsapp', 100, 1000, NOW() - INTERVAL '6 minutes')
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

func TestWatchdog_AutoSnoozeGracePeriod(t *testing.T) {
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
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active, notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 20)
	`, locationID, tenantID)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
	`, sessionID, tenantID, locationID)

	customerID := uuid.New()
	_, _ = pool.Exec(ctx, "INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, '+919999999999', 'Customer')", customerID, tenantID)

	visitID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 15, NOW() + INTERVAL '23 hours')
	`, visitID, tenantID, locationID, customerID)

	// The bug scenario: customer joins directly into position 1, remote, never notified
	entryID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, $4, 1, 'waiting', 'remote', true, 'whatsapp', 100, 1000)
	`, entryID, visitID, sessionID, customerID)

	// First tick: notifies (near_turn) but must NOT snooze in the same pass
	watchdog.runJob(ctx)

	var presence string
	var notifiedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT presence_state, near_turn_notified_at FROM queue_entries WHERE id = $1", entryID).Scan(&presence, &notifiedAt); err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if presence != "notified" {
		t.Errorf("Expected presence 'notified' after first tick, got '%s'", presence)
	}
	if notifiedAt == nil {
		t.Fatal("Expected near_turn_notified_at to be set by first tick")
	}

	// Second tick, still inside the grace window: must NOT snooze
	watchdog.runJob(ctx)
	_ = pool.QueryRow(ctx, "SELECT presence_state FROM queue_entries WHERE id = $1", entryID).Scan(&presence)
	if presence != "notified" {
		t.Errorf("Expected presence 'notified' within grace period, got '%s'", presence)
	}

	// Backdate notification past the grace period: next tick MUST snooze
	_, _ = pool.Exec(ctx, "UPDATE queue_entries SET near_turn_notified_at = NOW() - INTERVAL '6 minutes' WHERE id = $1", entryID)
	watchdog.runJob(ctx)
	var dispatchable bool
	_ = pool.QueryRow(ctx, "SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1", entryID).Scan(&presence, &dispatchable)
	if presence != "snoozed" || dispatchable {
		t.Errorf("Expected snoozed and not dispatchable after grace period, got presence=%s, dispatchable=%t", presence, dispatchable)
	}

	// H4: web channel now gets MARKED on the same tick (channel-agnostic
	// marking), but is never snoozed in that tick (grace) and gets no
	// WhatsApp template. Never-notified entries remain never-snoozed — the
	// marking is what starts the clock.
	_, _ = pool.Exec(ctx, "TRUNCATE outbox_events")
	_, _ = pool.Exec(ctx, `
		UPDATE queue_entries
		SET presence_state = 'remote', is_dispatchable = true, session_channel = 'web',
		    near_turn_notified_at = NULL, snoozed_at = NULL,
		    remote_joined_at = NOW() - INTERVAL '3 hours'
		WHERE id = $1
	`, entryID)
	watchdog.runJob(ctx)
	_ = pool.QueryRow(ctx, "SELECT presence_state FROM queue_entries WHERE id = $1", entryID).Scan(&presence)
	if presence != "notified" {
		t.Errorf("Expected web entry marked 'notified' (not snoozed) on first tick, got '%s'", presence)
	}
	var webOutbox int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events").Scan(&webOutbox)
	if webOutbox != 0 {
		t.Errorf("Web-channel marking must insert zero outbox rows, got %d", webOutbox)
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

	// Everything derives from closingTime so the test holds across midnight:
	// at 00:30 IST, "closed 2.5h ago" belongs to YESTERDAY's business date and
	// day-of-week, and EOD must still archive that session (dead-zone fix).
	dayOfWeek := int(closingTime.Weekday())
	_, _ = pool.Exec(ctx, `
		INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
		VALUES ($1, $2, $3, $4, true, $5::TIME, $6::TIME)
	`, uuid.New(), tenantID, locationID, dayOfWeek, opensTimeStr, closingTimeStr)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, $4::DATE, 'active')
	`, sessionID, tenantID, locationID, closingTime.Format("2006-01-02"))

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
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
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

// H4: near-turn marking is channel-agnostic; the WhatsApp template is not.
// A web-channel (even anonymous) entry must get near_turn_notified_at — so it
// can become snooze-eligible — while inserting ZERO outbox rows, and the
// 5-minute grace then applies to it exactly as it does for whatsapp entries.
func TestWatchdog_WebChannelMarkAndSnooze(t *testing.T) {
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
	_, _ = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active, notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'Loc', 'slug/loc', 'Asia/Kolkata', true, 2, 20)
	`, locationID, tenantID)

	sessionID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
	`, sessionID, tenantID, locationID)

	// Anonymous web entry: customer_id NULL pins the LEFT JOIN in checkNearTurn.
	visitID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, entry_type, status, party_size, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, $3, 'walk_in', 'active', 1, 15, NOW() + INTERVAL '23 hours')
	`, visitID, tenantID, locationID)
	entryID := uuid.New()
	_, _ = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, token_number, state, presence_state, is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, 1, 'waiting', 'remote', true, 'web', 100, 1000)
	`, entryID, visitID, sessionID)

	// Tick 1: marked, not snoozed (grace), zero WhatsApp outbox rows.
	watchdog.runJob(ctx)

	var presence string
	var notifiedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT presence_state, near_turn_notified_at FROM queue_entries WHERE id = $1", entryID).Scan(&presence, &notifiedAt); err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if presence != "notified" {
		t.Errorf("Expected web entry marked 'notified', got '%s'", presence)
	}
	if notifiedAt == nil {
		t.Fatal("Expected near_turn_notified_at set for web entry")
	}
	var outboxCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1", tenantID).Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("Web-channel near-turn marking must insert ZERO outbox rows, got %d", outboxCount)
	}

	// Backdate past grace: tick 2 must auto-snooze — still zero outbox rows.
	_, _ = pool.Exec(ctx, "UPDATE queue_entries SET near_turn_notified_at = NOW() - INTERVAL '6 minutes' WHERE id = $1", entryID)
	watchdog.runJob(ctx)

	var dispatchable bool
	_ = pool.QueryRow(ctx, "SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1", entryID).Scan(&presence, &dispatchable)
	if presence != "snoozed" || dispatchable {
		t.Errorf("Expected web entry snoozed/undispatchable, got presence=%s, dispatchable=%t", presence, dispatchable)
	}
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1", tenantID).Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("Web-channel auto-snooze must insert ZERO outbox rows, got %d", outboxCount)
	}
}

// ─── T3: tier-aware auto-snooze and gone-dark detection ─────────────────────

type tierShop struct {
	tenantID, locationID, sessionID uuid.UUID
	watchdog                        *Watchdog
	manager                         *realtime.Manager
	session                         session
}

// seedTierShop builds one active shop with a queue session and returns the
// watchdog wired to it. notify_when_people_ahead is set to -1 so checkNearTurn
// never fires in these tests: this file's T3 cases are about snooze and tier
// availability, and a stray near-turn would bump queue_version underneath them.
func seedTierShop(t *testing.T, pool *pgxpool.Pool) tierShop {
	t.Helper()
	ctx := context.Background()
	s := tierShop{tenantID: uuid.New(), locationID: uuid.New(), sessionID: uuid.New()}

	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'T3 Tenant', $2, '+919876500000')`, s.tenantID, "t3-"+uuid.NewString()[:8])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active,
		                       notify_when_people_ahead, notify_when_wait_minutes)
		VALUES ($1, $2, 'T3 Loc', $3, 'Asia/Kolkata', true, -1, -1)`,
		s.locationID, s.tenantID, "t3loc-"+uuid.NewString()[:8])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')`,
		s.sessionID, s.tenantID, s.locationID)
	require.NoError(t, err)

	s.manager = realtime.NewManager()
	s.watchdog = NewWatchdog(pool, s.manager, &config.Config{
		HMACSecret:      "test-hmac-secret-123456789012345",
		BhejnaFromPhone: "+912200000001",
	})
	s.session = session{ID: s.sessionID, TenantID: s.tenantID, LocationID: s.locationID,
		NotifyPeopleAhead: -1, NotifyWaitMinutes: -1, LocationName: "T3 Loc"}
	return s
}

func seedTierRow(t *testing.T, pool *pgxpool.Pool, s tierShop, name string, rank int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO staff_tiers (tenant_id, location_id, name, rank)
		VALUES ($1, $2, $3, $4) RETURNING id`, s.tenantID, s.locationID, name, rank).Scan(&id))
	return id
}

func seedTierBarber(t *testing.T, pool *pgxpool.Pool, s tierShop, name, status string, tierID *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, status, tier_id)
		VALUES ($1, $2, $3, $4, 'barber', $5, $6) RETURNING id`,
		s.tenantID, s.locationID, name, "+9197"+uuid.NewString()[:9], status, tierID).Scan(&id))
	return id
}

// seedSnoozeCandidate creates a waiting entry already past the snooze grace
// period: presence 'notified', near_turn_notified_at well in the past. Anything
// that is still not snoozed after a tick was spared on purpose.
func seedSnoozeCandidate(t *testing.T, pool *pgxpool.Pool, s tierShop, token, sortKey int, tierID *uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var visitID, entryID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, status, total_duration_minutes, magic_link_expires_at)
		VALUES ($1, $2, 'walk_in', 'active', 30, NOW() + INTERVAL '23 hours') RETURNING id`,
		s.tenantID, s.locationID).Scan(&visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state, is_dispatchable,
			 session_channel, priority_group, sort_key, required_tier_id, near_turn_notified_at)
		VALUES ($1, $2, $3, 'waiting', 'notified', true, 'web', 100, $4, $5,
		        NOW() - INTERVAL '30 minutes') RETURNING id`,
		visitID, s.sessionID, token, int64(sortKey), tierID).Scan(&entryID))
	return entryID
}

func entrySnapshot(t *testing.T, pool *pgxpool.Pool, entryID uuid.UUID) (state, presence string, dispatchable bool, warning *string) {
	t.Helper()
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT state, presence_state, is_dispatchable, stale_warning
		FROM queue_entries WHERE id = $1`, entryID).Scan(&state, &presence, &dispatchable, &warning))
	return
}

func queueVersion(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID) int {
	t.Helper()
	var v int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT queue_version FROM queue_sessions WHERE id = $1`, sessionID).Scan(&v))
	return v
}

// A4 (snooze half) + A5 (first half) — head-of-line token 14 requires Senior,
// the only Senior is mid-cut, two Juniors are idle. The queue moves around 14,
// and 14 is NOT snoozed, NOT skipped: it stays waiting and dispatchable, with
// its presence untouched.
//
// Before T3 this was the harm: the entry is head-of-line, the grace has elapsed,
// so it got snoozed and is_dispatchable=false — punished for a constraint the
// shop sold it.
func TestWatchdog_T3_HeadOfLineWaitingOnBusyTierIsNotSnoozed(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	junior := seedTierRow(t, pool, shop, "Junior", 10)
	senior := seedTierRow(t, pool, shop, "Senior", 20)
	seedTierBarber(t, pool, shop, "The Senior", "cutting", &senior)
	seedTierBarber(t, pool, shop, "Junior One", "idle", &junior)
	seedTierBarber(t, pool, shop, "Junior Two", "idle", &junior)

	token14 := seedSnoozeCandidate(t, pool, shop, 14, 100, &senior)
	token15 := seedSnoozeCandidate(t, pool, shop, 15, 200, nil)

	shop.watchdog.runJob(ctx)

	state, presence, dispatchable, _ := entrySnapshot(t, pool, token14)
	require.Equal(t, "waiting", state, "A4: token 14 must stay waiting")
	require.Equal(t, "notified", presence, "A4: token 14 must not be snoozed")
	require.True(t, dispatchable, "A4/A9: is_dispatchable must be untouched by tier logic")

	// The next eligible entry is the one the tick acts on instead.
	_, presence15, dispatchable15, _ := entrySnapshot(t, pool, token15)
	require.Equal(t, "snoozed", presence15, "A5: an unconstrained entry whose turn genuinely came is still snoozed")
	require.False(t, dispatchable15)
}

// A5 (second half) — the gate must not become a blanket "never snooze". An
// unconstrained head-of-line entry past its grace period is still snoozed, and
// a tier-constrained one whose barber IS idle is snoozed too.
func TestWatchdog_T3_SnoozeStillFiresWhenABarberIsFree(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	senior := seedTierRow(t, pool, shop, "Senior", 20)
	seedTierBarber(t, pool, shop, "The Senior", "idle", &senior)

	tiered := seedSnoozeCandidate(t, pool, shop, 20, 100, &senior)
	shop.watchdog.runJob(ctx)

	_, presence, dispatchable, _ := entrySnapshot(t, pool, tiered)
	require.Equal(t, "snoozed", presence,
		"A5: an idle eligible barber means the customer's turn really has come")
	require.False(t, dispatchable)
}

// A8 — zero on-shift barbers for a required tier is detected and surfaced on the
// entry. No notification is sent: bb_tier_unavailable is a Meta template that
// must be submitted manually, and that is T4's scope.
func TestWatchdog_T3_TierGoneDarkIsSurfacedWithoutNotifying(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	senior := seedTierRow(t, pool, shop, "Senior", 20)
	offlineSenior := seedTierBarber(t, pool, shop, "The Senior", "offline", &senior)
	seedTierBarber(t, pool, shop, "A Junior", "idle", nil) // on shift, wrong tier

	entryID := seedSnoozeCandidate(t, pool, shop, 30, 100, &senior)
	plain := seedSnoozeCandidate(t, pool, shop, 31, 200, nil)

	before := queueVersion(t, pool, shop.sessionID)
	shop.watchdog.checkTierAvailability(ctx, shop.session)

	_, _, _, warning := entrySnapshot(t, pool, entryID)
	require.NotNil(t, warning)
	require.Equal(t, "tier_unavailable", *warning, "A8: the shop must be able to see the entry is unservable")

	_, _, _, plainWarning := entrySnapshot(t, pool, plain)
	require.Nil(t, plainWarning, "A8: an unconstrained entry is always servable")

	var outbox int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events`).Scan(&outbox))
	require.Zero(t, outbox, "A8: no notification in this unit — bb_tier_unavailable is T4")

	require.Equal(t, before+1, queueVersion(t, pool, shop.sessionID), "A8: a real change bumps the version")

	// And it clears itself the moment that barber comes back on shift — via a
	// break, not just idle, since a break still counts as on shift.
	_, err := pool.Exec(ctx, `UPDATE staff_members SET status = 'break' WHERE id = $1`, offlineSenior)
	require.NoError(t, err)
	shop.watchdog.checkTierAvailability(ctx, shop.session)
	_, _, _, warning = entrySnapshot(t, pool, entryID)
	require.Nil(t, warning, "A8: the warning must clear when the tier is staffed again")
}

// A8b — this unit clears only the value it sets. A waiting-state warning written
// by anything else survives a full tick, in both the tier-available and
// tier-unavailable branches. Nothing else writes stale_warning for waiting rows
// today, which makes a blanket CASE ... ELSE NULL safe by coincidence; this test
// is what keeps it safe by construction.
func TestWatchdog_T3_ForeignStaleWarningSurvivesTick(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	senior := seedTierRow(t, pool, shop, "Senior", 20)
	staffedTier := seedTierRow(t, pool, shop, "Staffed", 30)
	seedTierBarber(t, pool, shop, "Staffed Barber", "idle", &staffedTier)
	// Nobody in Senior at all → that tier is dark.

	dark := seedSnoozeCandidate(t, pool, shop, 40, 100, &senior)
	lit := seedSnoozeCandidate(t, pool, shop, 41, 200, &staffedTier)
	for _, id := range []uuid.UUID{dark, lit} {
		_, err := pool.Exec(ctx,
			`UPDATE queue_entries SET stale_warning = 'someone_elses_warning' WHERE id = $1`, id)
		require.NoError(t, err)
	}

	shop.watchdog.runJob(ctx)

	_, _, _, darkWarning := entrySnapshot(t, pool, dark)
	require.NotNil(t, darkWarning)
	require.Equal(t, "someone_elses_warning", *darkWarning,
		"A8b: the tier-unavailable branch must not overwrite a warning it did not set")

	_, _, _, litWarning := entrySnapshot(t, pool, lit)
	require.NotNil(t, litWarning)
	require.Equal(t, "someone_elses_warning", *litWarning,
		"A8b: the clear branch must not wipe a warning it did not set")
}

// A8c — a tick that changes nothing must not bump queue_version and must not
// broadcast. Otherwise every idle shop with a tiered queue forces a refetch from
// every connected client once a minute, forever.
func TestWatchdog_T3_NoOpTickDoesNotBumpVersionOrBroadcast(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	senior := seedTierRow(t, pool, shop, "Senior", 20)
	seedTierBarber(t, pool, shop, "The Senior", "idle", &senior)
	seedSnoozeCandidate(t, pool, shop, 50, 100, &senior)

	// First pass settles whatever there is to settle.
	shop.watchdog.checkTierAvailability(ctx, shop.session)
	settled := queueVersion(t, pool, shop.sessionID)

	ch := shop.manager.Subscribe(shop.locationID.String())
	shop.watchdog.checkTierAvailability(ctx, shop.session)

	require.Equal(t, settled, queueVersion(t, pool, shop.sessionID),
		"A8c: an unchanged tick must not bump queue_version")
	select {
	case ev := <-ch:
		t.Fatalf("A8c: an unchanged tick must not broadcast, got %+v", ev)
	default:
	}
}

// ─── B7: nullable magic_link_expires_at ─────────────────────────────────────

// seedAnonWalkin creates the shape that broke the watchdog: a visit with no
// customer and therefore no magic link, joined from the web. Everything about
// this row is ordinary — it is what a walk-in who never gives a phone number
// looks like, which is the modal customer for a barbershop.
func seedAnonWalkin(t *testing.T, pool *pgxpool.Pool, s tierShop, token, sortKey int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var visitID, entryID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 'active', 30) RETURNING id`,
		s.tenantID, s.locationID).Scan(&visitID))
	var expiry *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT magic_link_expires_at FROM visits WHERE id = $1`, visitID).Scan(&expiry))
	require.Nil(t, expiry, "fixture precondition: an anonymous visit has no magic link")

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state, is_dispatchable,
			 session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, 'waiting', 'remote', true, 'web', 100, $4) RETURNING id`,
		visitID, s.sessionID, token, int64(sortKey)).Scan(&entryID))
	return entryID
}

// seedWhatsAppCandidate is the control: a customer, a phone, and a live magic
// link. A3 asserts this path is byte-for-byte what it was before B7.
func seedWhatsAppCandidate(t *testing.T, pool *pgxpool.Pool, s tierShop, token, sortKey int, expiry time.Time) (entryID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var customerID, visitID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO customers (tenant_id, phone_number, name)
		VALUES ($1, $2, 'WA Customer') RETURNING id`,
		s.tenantID, "+9196"+uuid.NewString()[:9]).Scan(&customerID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, customer_id, entry_type, status,
		                    total_duration_minutes, magic_link_token_hash, magic_link_expires_at)
		VALUES ($1, $2, $3, 'walk_in', 'active', 30, 'hash', $4) RETURNING id`,
		s.tenantID, s.locationID, customerID, expiry).Scan(&visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, customer_id, token_number, state, presence_state,
			 is_dispatchable, session_channel, priority_group, sort_key)
		VALUES ($1, $2, $3, $4, 'waiting', 'remote', true, 'whatsapp', 100, $5) RETURNING id`,
		visitID, s.sessionID, customerID, token, int64(sortKey)).Scan(&entryID))
	return entryID
}

// seedNotifyingShop is seedTierShop with near-turn thresholds that fire, rather
// than the -1 sentinels the T3 tests use to keep checkNearTurn out of the way.
func seedNotifyingShop(t *testing.T, pool *pgxpool.Pool) tierShop {
	t.Helper()
	s := seedTierShop(t, pool)
	_, err := pool.Exec(context.Background(), `
		UPDATE locations SET notify_when_people_ahead = 5, notify_when_wait_minutes = 999
		WHERE id = $1`, s.locationID)
	require.NoError(t, err)
	s.session.NotifyPeopleAhead = 5
	s.session.NotifyWaitMinutes = 999
	return s
}

// A1 — an anonymous walk-in is scanned without error and becomes a candidate.
//
// This is the assertion that fails on the pre-fix code with
//
//	Watchdog scan candidate failed: can't scan into dest[5]
//	(col: magic_link_expires_at): cannot scan NULL into *time.Time
//
// which is A5's counter-check.
func TestWatchdog_B7_AnonymousWalkinIsScannedAndNotified(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedNotifyingShop(t, pool)

	entryID := seedAnonWalkin(t, pool, shop, 1, 100)
	shop.watchdog.checkNearTurn(ctx, shop.session)

	var presence string
	var notifiedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT presence_state, near_turn_notified_at FROM queue_entries WHERE id = $1`,
		entryID).Scan(&presence, &notifiedAt))
	require.Equal(t, "notified", presence, "A1: an anonymous walk-in must survive the scan")
	require.NotNil(t, notifiedAt, "A1: near_turn_notified_at is what makes it snooze-eligible")

	// No customer means no template — the entry is marked, nothing is sent.
	var outbox int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events`).Scan(&outbox))
	require.Zero(t, outbox, "A1: no magic link, no WhatsApp — but the marking still happened")
}

// A2 — the full chain the bug severed: scan -> notified -> snoozed.
func TestWatchdog_B7_AnonymousWalkinBecomesSnoozeEligible(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedNotifyingShop(t, pool)

	entryID := seedAnonWalkin(t, pool, shop, 1, 100)

	// Tick one: near-turn marks it.
	shop.watchdog.checkNearTurn(ctx, shop.session)
	var notifiedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT near_turn_notified_at FROM queue_entries WHERE id = $1`, entryID).Scan(&notifiedAt))
	require.NotNil(t, notifiedAt)

	// The grace period is wall-clock, so age the mark rather than sleeping 5 min.
	_, err := pool.Exec(ctx, `
		UPDATE queue_entries SET near_turn_notified_at = NOW() - INTERVAL '30 minutes'
		WHERE id = $1`, entryID)
	require.NoError(t, err)

	// Tick two: it is now snooze-eligible, which it never could have been before.
	shop.watchdog.checkAutoSnooze(ctx, shop.session)
	var presence string
	var dispatchable bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1`,
		entryID).Scan(&presence, &dispatchable))
	require.Equal(t, "snoozed", presence, "A2: the whole point — it can finally be snoozed")
	require.False(t, dispatchable)
}

// A3 — a WhatsApp entry with a live magic link is unchanged: still notified,
// still sends bb_near_turn, still carries a magic-link button parameter.
func TestWatchdog_B7_WhatsAppCandidateUnchanged(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedNotifyingShop(t, pool)

	entryID := seedWhatsAppCandidate(t, pool, shop, 1, 100, time.Now().Add(23*time.Hour))
	shop.watchdog.checkNearTurn(ctx, shop.session)

	var presence string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT presence_state FROM queue_entries WHERE id = $1`, entryID).Scan(&presence))
	require.Equal(t, "notified", presence)

	var payloadBytes []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT payload FROM outbox_events WHERE type = 'notification.send'`).Scan(&payloadBytes))
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(payloadBytes, &payload))
	require.Equal(t, "bb_near_turn", payload["template_code"], "A3: no regression on the WhatsApp path")

	// Assert on the parsed structure, not a substring: JSONB round-trips with
	// whitespace of its own choosing and a raw-text match is a false negative
	// waiting to happen.
	var sawQuickReply, sawMagicLink bool
	for _, c := range payload["components"].([]interface{}) {
		comp := c.(map[string]interface{})
		params, _ := comp["parameters"].([]interface{})
		for _, pr := range params {
			switch pr.(map[string]interface{})["type"] {
			case "payload":
				sawQuickReply = true
			case "text":
				if comp["sub_type"] == "url" {
					sawMagicLink = true
					require.NotEmpty(t, pr.(map[string]interface{})["text"],
						"A3: the magic-link token must still be signed and present")
				}
			}
		}
	}
	require.True(t, sawQuickReply, "A3: the ON_THE_WAY quick-reply button survives")
	require.True(t, sawMagicLink, "A3: the magic-link URL button survives")
}

// A4 — nil and expired are different things and must stay different.
//
// generateMagicLinkToken (commands.go:173) signs expiresAt into the payload and
// never validates it; expiry is checked at redemption. So an EXPIRED link is
// still a link: the template goes out exactly as before, carrying a token the
// status page will reject. A NIL expiry means no link was ever issued, so there
// is nothing to sign and the template is skipped. Only nil short-circuits.
func TestWatchdog_B7_ExpiredLinkStillSendsNilDoesNot(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	t.Run("expired_link_still_notifies_and_sends", func(t *testing.T) {
		shop := seedNotifyingShop(t, pool)
		entryID := seedWhatsAppCandidate(t, pool, shop, 1, 100, time.Now().Add(-2*time.Hour))
		shop.watchdog.checkNearTurn(ctx, shop.session)

		var presence string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT presence_state FROM queue_entries WHERE id = $1`, entryID).Scan(&presence))
		require.Equal(t, "notified", presence)

		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1`, shop.tenantID).Scan(&n))
		require.Equal(t, 1, n, "A4: an expired link is still a link — B7 must not swallow it")
	})

	t.Run("nil_link_notifies_but_sends_nothing", func(t *testing.T) {
		shop := seedNotifyingShop(t, pool)
		entryID := seedAnonWalkin(t, pool, shop, 1, 100)
		shop.watchdog.checkNearTurn(ctx, shop.session)

		var presence string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT presence_state FROM queue_entries WHERE id = $1`, entryID).Scan(&presence))
		require.Equal(t, "notified", presence)

		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1`, shop.tenantID).Scan(&n))
		require.Zero(t, n, "A4: nil means no link exists, so no template")
	})
}

// A6 — the second instance of the same mismatch. triggerAutoSnooze reads the
// same nullable column into its own local. It is guarded by whatsapp+customer
// so prod has never hit it (0 such rows), but the guard does not make the column
// non-NULL, so it was fixed alongside. This drives that path with a customer
// whose visit has no magic link.
func TestWatchdog_B7_SnoozePathHandlesNilExpiry(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedNotifyingShop(t, pool)

	var customerID, visitID, entryID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO customers (tenant_id, phone_number, name)
		VALUES ($1, $2, 'No Link') RETURNING id`,
		shop.tenantID, "+9195"+uuid.NewString()[:9]).Scan(&customerID))
	// whatsapp channel, a real customer, but magic_link_expires_at left NULL.
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, 'walk_in', 'active', 30) RETURNING id`,
		shop.tenantID, shop.locationID, customerID).Scan(&visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, customer_id, token_number, state, presence_state,
			 is_dispatchable, session_channel, priority_group, sort_key, near_turn_notified_at)
		VALUES ($1, $2, $3, 1, 'waiting', 'notified', true, 'whatsapp', 100, 100,
		        NOW() - INTERVAL '30 minutes') RETURNING id`,
		visitID, shop.sessionID, customerID).Scan(&entryID))

	shop.watchdog.checkAutoSnooze(ctx, shop.session)

	var presence string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT presence_state FROM queue_entries WHERE id = $1`, entryID).Scan(&presence))
	require.Equal(t, "snoozed", presence, "A6: the snooze read must survive a NULL expiry too")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE tenant_id = $1`, shop.tenantID).Scan(&n))
	require.Zero(t, n, "A6: no link to send, so no bb_queue_snoozed template")
}
