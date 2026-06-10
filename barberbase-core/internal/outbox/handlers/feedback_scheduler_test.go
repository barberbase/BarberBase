package notification

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/config"
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
	_, _ = pool.Exec(ctx, "TRUNCATE outbox_events CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE feedback_requests CASCADE")
	_, _ = pool.Exec(ctx, "TRUNCATE feedback_responses CASCADE")

	return pool
}

func TestFeedbackScheduler_Handle(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()
	barberID := uuid.New()
	sessionID := uuid.New()

	// Seed tenant
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Barber Shop', 'barber-shop', '+919876543210')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	// Seed location
	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, whatsapp_mode, business_whatsapp_number)
		VALUES ($1, $2, 'Downtown Branch', 'barber-shop-downtown', 'Asia/Kolkata', 'own_number', '+911122334455')
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	// Seed staff member
	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active)
		VALUES ($1, $2, $3, 'John Doe', '+919999888877', 'barber', 'idle', true)
	`, barberID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed staff: %v", err)
	}

	// Seed customer
	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+918888777766', 'Alice')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	// Seed queue session
	_, err = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	// Seed visit
	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	// Seed queue entry
	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, assigned_barber_id)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 'waiting', 'remote', $4)
	`, visitID, sessionID, customerID, barberID)
	if err != nil {
		t.Fatalf("Failed to seed queue entry: %v", err)
	}

	cfg := &config.Config{
		BhejnaFromPhone: "+911234567890",
	}

	scheduler := NewFeedbackScheduler(pool, cfg)

	payload := FeedbackSchedulePayload{
		VisitID:    visitID.String(),
		TenantID:   tenantID.String(),
		LocationID: locationID.String(),
	}
	payloadBytes, _ := json.Marshal(payload)

	outboxEventID := uuid.New().String()
	event := &OutboxEvent{
		ID:           outboxEventID,
		TenantID:     nil,
		Type:         "feedback_request.schedule",
		Payload:      payloadBytes,
		ProcessAfter: time.Now(),
	}

	// Test 1: schedule processes correctly
	err = scheduler.Handle(ctx, pool, event)
	if err != nil {
		t.Fatalf("Scheduler handle failed: %v", err)
	}

	// Assert exactly one feedback_requests row exists
	var countFR int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM feedback_requests WHERE visit_id = $1 AND channel = 'whatsapp'", visitID).Scan(&countFR)
	if err != nil {
		t.Fatalf("Failed to query feedback_requests: %v", err)
	}
	if countFR != 1 {
		t.Errorf("Expected exactly 1 feedback request, got %d", countFR)
	}

	// Assert exactly one notification.send outbox_event exists
	var countOutbox int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE type = 'notification.send'").Scan(&countOutbox)
	if err != nil {
		t.Fatalf("Failed to query outbox_events: %v", err)
	}
	if countOutbox != 1 {
		t.Errorf("Expected exactly 1 notification.send outbox event, got %d", countOutbox)
	}

	// Retrieve outbox event and verify payload template_code
	var outboxPayloadBytes []byte
	err = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE type = 'notification.send' LIMIT 1").Scan(&outboxPayloadBytes)
	if err != nil {
		t.Fatalf("Failed to query outbox event payload: %v", err)
	}
	var outboxPayload map[string]interface{}
	if err := json.Unmarshal(outboxPayloadBytes, &outboxPayload); err != nil {
		t.Fatalf("Failed to unmarshal outbox payload: %v", err)
	}
	if outboxPayload["template_code"] != "bb_service_feedback" {
		t.Errorf("Expected template_code 'bb_service_feedback', got %v", outboxPayload["template_code"])
	}

	// Test 2: idempotency on replay
	err = scheduler.Handle(ctx, pool, event)
	if err != nil {
		t.Fatalf("Scheduler handle retry failed: %v", err)
	}

	// Assert still exactly one feedback_requests row
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM feedback_requests WHERE visit_id = $1 AND channel = 'whatsapp'", visitID).Scan(&countFR)
	if err != nil {
		t.Fatalf("Failed to query feedback_requests: %v", err)
	}
	if countFR != 1 {
		t.Errorf("Expected exactly 1 feedback request on replay, got %d", countFR)
	}

	// Assert still exactly one notification.send outbox_event
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE type = 'notification.send'").Scan(&countOutbox)
	if err != nil {
		t.Fatalf("Failed to query outbox_events: %v", err)
	}
	if countOutbox != 1 {
		t.Errorf("Expected exactly 1 notification.send outbox event on replay, got %d", countOutbox)
	}
}
