package webhook

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

	return pool
}

func TestNormalizeE164(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"919876543210", "+919876543210"},
		{"", ""},
		{"   ", ""},
		{"+91 (987) 654-3210", "+919876543210"},
		{"abc-123", "+123"},
	}

	for _, tt := range tests {
		result := repository.NormalizeE164(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeE164(%q) = %q; expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestClassify(t *testing.T) {
	// 1. Join
	joinPayload := `{
		"event_type": "message.received",
		"message": {
			"type": "text",
			"body": "JOIN STAR-SALON JN8K4P"
		},
		"sender": {
			"phone_number": "919876543210"
		}
	}`
	msg, err := Classify([]byte(joinPayload), nil)
	if err != nil {
		t.Fatalf("Failed to classify: %v", err)
	}
	if msg.Action != ActionJoin || msg.TokenCode != "JN8K4P" || msg.SlugFromBody != "STAR-SALON" {
		t.Errorf("Unexpected Join classification: %+v", msg)
	}

	// 2. Button on the way
	otwPayload := `{
		"event_type": "message.received",
		"message": {
			"button_payload": "ON_THE_WAY:019001b3-4f9c-70e1-8000-017f8a9b2c3d"
		}
	}`
	msg, err = Classify([]byte(otwPayload), nil)
	if err != nil {
		t.Fatalf("Failed to classify: %v", err)
	}
	if msg.Action != ActionOnTheWay || msg.EntryID != "019001b3-4f9c-70e1-8000-017f8a9b2c3d" {
		t.Errorf("Unexpected OnTheWay classification: %+v", msg)
	}

	// 3. Plain Rating
	ratingPayload := `{
		"event_type": "message.received",
		"message": {
			"type": "text",
			"body": " 4 "
		}
	}`
	msg, err = Classify([]byte(ratingPayload), nil)
	if err != nil {
		t.Fatalf("Failed to classify: %v", err)
	}
	if msg.Action != ActionPlainRating || msg.Rating != 4 {
		t.Errorf("Unexpected PlainRating classification: %+v", msg)
	}
}

func TestIntegration_JoinWildcardSlug(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed Tenant
	tenantID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Wildcard Tenant', 'wildcard-tenant', '+919876543211')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	// Seed Location with slug having % and _
	locationID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Wildcard Location', 'star%salon_branch', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	// Seed checkin intent
	intentID := uuid.New()
	expiresAt := time.Now().Add(10 * time.Hour)
	_, err = pool.Exec(ctx, `
		INSERT INTO checkin_intents (id, tenant_id, location_id, token_code, shop_status_at_creation, variant_ids, party_size, expires_at, status)
		VALUES ($1, $2, $3, 'JN8K4P', 'open', '[]'::jsonb, 1, $4, 'created')
	`, intentID, tenantID, locationID, expiresAt)
	if err != nil {
		t.Fatalf("Failed to seed intent: %v", err)
	}

	// Dispatch a JOIN webhook
	payload := `{
		"event_type": "message.received",
		"business_phone_number": "912212345678",
		"message": {
			"type": "text",
			"body": "JOIN STAR%SALON_BRANCH JN8K4P"
		},
		"sender": {
			"phone_number": "919876543210",
			"display_name": "Rahul"
		}
	}`

	os.Setenv("HMAC_SECRET", "testsecret")
	os.Setenv("BHEJNA_FROM_PHONE", "+912212345678")

	broadcaster := NoopBroadcaster{}
	proc := NewProcessor(pool, broadcaster)

	eventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, payload, status)
		VALUES ($1, 'bhejna', 'ext1', 'message.received', $2, 'pending')
	`, eventID, []byte(payload))
	if err != nil {
		t.Fatalf("Failed to insert webhook event: %v", err)
	}

	row, err := proc.claimEvent(ctx)
	if err != nil {
		t.Fatalf("Failed to claim event: %v", err)
	}
	if row == nil {
		t.Fatal("Expected to claim event, got nil")
	}

	err = proc.processEvent(ctx, row)
	if err != nil {
		t.Fatalf("processEvent failed: %v", err)
	}

	// Verify visit and queue entry were created
	var countVisits int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM visits WHERE tenant_id = $1", tenantID).Scan(&countVisits)
	if err != nil {
		t.Fatalf("Failed to query visits: %v", err)
	}
	if countVisits != 1 {
		t.Errorf("Expected 1 visit, got %d", countVisits)
	}

	var countEntries int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM queue_entries WHERE customer_id IS NOT NULL").Scan(&countEntries)
	if err != nil {
		t.Fatalf("Failed to query queue entries: %v", err)
	}
	if countEntries != 1 {
		t.Errorf("Expected 1 queue entry, got %d", countEntries)
	}
}

func TestIntegration_ConcurrentClaim(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed one pending event
	payload := `{"event_type": "message.received"}`
	eventID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, payload, status)
		VALUES ($1, 'bhejna', 'ext_concur', 'message.received', $2, 'pending')
	`, eventID, []byte(payload))
	if err != nil {
		t.Fatalf("Failed to seed event: %v", err)
	}

	proc := NewProcessor(pool, NoopBroadcaster{})

	var wg sync.WaitGroup
	var claim1, claim2 *WebhookEventRow
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		claim1, err1 = proc.claimEvent(ctx)
	}()
	go func() {
		defer wg.Done()
		claim2, err2 = proc.claimEvent(ctx)
	}()
	wg.Wait()

	if err1 != nil {
		t.Errorf("worker 1 error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("worker 2 error: %v", err2)
	}

	// Exactly one worker must claim the event, the other must get nil
	if (claim1 == nil && claim2 == nil) || (claim1 != nil && claim2 != nil) {
		t.Errorf("Expected exactly one worker to claim the event, but got claim1=%v, claim2=%v", claim1, claim2)
	}
}

func TestIntegration_ReclaimStuckEvent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed one event in 'processing' status but locked_until in the past
	eventID := uuid.New()
	pastTime := time.Now().Add(-60 * time.Second)
	_, err := pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, payload, status, locked_until, attempts)
		VALUES ($1, 'bhejna', 'ext_stuck', 'message.received', '{"event_type": "message.received"}'::jsonb, 'processing', $2, 1)
	`, eventID, pastTime)
	if err != nil {
		t.Fatalf("Failed to seed event: %v", err)
	}

	proc := NewProcessor(pool, NoopBroadcaster{})
	row, err := proc.claimEvent(ctx)
	if err != nil {
		t.Fatalf("Failed to claim event: %v", err)
	}
	if row == nil {
		t.Fatal("Expected to reclaim stuck event, got nil")
	}

	if row.ID != eventID {
		t.Errorf("Expected to reclaim %s, got %s", eventID, row.ID)
	}
	if row.Attempts != 2 {
		t.Errorf("Expected attempts to increment to 2, got %d", row.Attempts)
	}
}

func TestIntegration_MaxAttemptsExclusion(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed one event with attempts=10 and status='failed'
	eventID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, payload, status, attempts)
		VALUES ($1, 'bhejna', 'ext_failed_max', 'message.received', '{"event_type": "message.received"}'::jsonb, 'failed', 10)
	`, eventID)
	if err != nil {
		t.Fatalf("Failed to seed event: %v", err)
	}

	proc := NewProcessor(pool, NoopBroadcaster{})
	row, err := proc.claimEvent(ctx)
	if err != nil {
		t.Fatalf("Failed to claim event: %v", err)
	}
	if row != nil {
		t.Errorf("Expected attempts=10 failed event to be ignored, but claimed row: %s", row.ID)
	}
}

func TestIntegration_DuplicateDeliveryIdempotency(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// First delivery
	err := repository.InsertWebhookEvent(ctx, pool, "dup_event_1", "message.received", nil, nil, []byte(`{"event_type": "message.received"}`))
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Second delivery with same source + external_event_id
	err = repository.InsertWebhookEvent(ctx, pool, "dup_event_1", "message.received", nil, nil, []byte(`{"event_type": "message.received"}`))
	if err != nil {
		t.Fatalf("Second insert failed: %v", err)
	}

	// Assert only one row exists in webhook_events
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM webhook_events WHERE external_event_id = 'dup_event_1'").Scan(&count)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 webhook event row, got %d", count)
	}
}
