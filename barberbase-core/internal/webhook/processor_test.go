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

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM webhook_events").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count webhook events: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected exactly 1 webhook event row, got %d", count)
	}
}

func TestIntegration_RatingButtonIdempotency(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()
	sessionID := uuid.New()
	requestID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Rating Tenant', 'rating-tenant', '+919876543212')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Rating Location', 'rating-location', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	// Seed Customer
	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+919876543210', 'Rahul')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	// Seed Queue Session
	_, err = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	// Seed Visit
	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	// Seed Feedback Request (status='sent')
	_, err = pool.Exec(ctx, `
		INSERT INTO feedback_requests (id, tenant_id, location_id, visit_id, customer_id, channel, status, scheduled_at, sent_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'whatsapp', 'sent', NOW(), NOW(), NOW() + INTERVAL '23 hours')
	`, requestID, tenantID, locationID, visitID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed feedback request: %v", err)
	}

	// Construct webhook_event with button_payload
	payload := `{
		"event_type": "message.received",
		"business_phone_number": "912212345678",
		"message": {
			"type": "button",
			"button_payload": "RATING:4:` + visitID.String() + `"
		},
		"sender": {
			"phone_number": "919876543210",
			"display_name": "Rahul"
		}
	}`

	os.Setenv("HMAC_SECRET", "testsecret")
	os.Setenv("BHEJNA_FROM_PHONE", "+912212345678")

	proc := NewProcessor(pool, NoopBroadcaster{})

	// Simulate first delivery
	eventID1 := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, tenant_id, location_id, payload, status)
		VALUES ($1, 'bhejna', 'ext_rating_1', 'message.received', $2, $3, $4, 'pending')
	`, eventID1, tenantID, locationID, []byte(payload))
	if err != nil {
		t.Fatalf("Failed to insert webhook event: %v", err)
	}

	row1, err := proc.claimEvent(ctx)
	if err != nil || row1 == nil {
		t.Fatalf("Failed to claim event 1: %v", err)
	}

	err = proc.processEvent(ctx, row1)
	if err != nil {
		t.Fatalf("processEvent 1 failed: %v", err)
	}

	// Verify feedback response exists and status is responded
	var ratingVal int
	err = pool.QueryRow(ctx, "SELECT rating FROM feedback_responses WHERE visit_id = $1", visitID).Scan(&ratingVal)
	if err != nil {
		t.Fatalf("Failed to find feedback response: %v", err)
	}
	if ratingVal != 4 {
		t.Errorf("Expected rating 4, got %d", ratingVal)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM feedback_requests WHERE id = $1", requestID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query status: %v", err)
	}
	if status != "responded" {
		t.Errorf("Expected feedback request status 'responded', got %q", status)
	}

	// Simulate duplicate delivery
	eventID2 := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, tenant_id, location_id, payload, status)
		VALUES ($1, 'bhejna', 'ext_rating_2', 'message.received', $2, $3, $4, 'pending')
	`, eventID2, tenantID, locationID, []byte(payload))
	if err != nil {
		t.Fatalf("Failed to insert webhook event 2: %v", err)
	}

	row2, err := proc.claimEvent(ctx)
	if err != nil || row2 == nil {
		t.Fatalf("Failed to claim event 2: %v", err)
	}

	err = proc.processEvent(ctx, row2)
	if err != nil {
		t.Fatalf("processEvent 2 failed: %v", err)
	}

	// Verify exactly one response exists
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM feedback_responses WHERE visit_id = $1", visitID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count feedback responses: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 response on duplicate delivery, got %d", count)
	}
}

func TestIntegration_RatingPlainText(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()
	tenantID := uuid.New()
	locationID := uuid.New()
	customerID := uuid.New()
	visitID := uuid.New()
	sessionID := uuid.New()
	requestID := uuid.New()

	// Seed Tenant & Location
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Rating Tenant PT', 'rating-tenant-pt', '+919876543213')
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed tenant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, timezone, is_active)
		VALUES ($1, $2, 'Rating Location PT', 'rating-location-pt', 'Asia/Kolkata', true)
	`, locationID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed location: %v", err)
	}

	// Seed Customer
	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+919876543210', 'Rahul')
	`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed customer: %v", err)
	}

	// Seed Queue Session
	_, err = pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active')
	`, sessionID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to seed queue session: %v", err)
	}

	// Seed Visit
	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 30)
	`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed visit: %v", err)
	}

	// Seed Feedback Request (status='sent')
	_, err = pool.Exec(ctx, `
		INSERT INTO feedback_requests (id, tenant_id, location_id, visit_id, customer_id, channel, status, scheduled_at, sent_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'whatsapp', 'sent', NOW(), NOW(), NOW() + INTERVAL '23 hours')
	`, requestID, tenantID, locationID, visitID, customerID)
	if err != nil {
		t.Fatalf("Failed to seed feedback request: %v", err)
	}

	// Construct plain text webhook event
	payload := `{
		"event_type": "message.received",
		"business_phone_number": "912212345678",
		"message": {
			"type": "text",
			"body": "4"
		},
		"sender": {
			"phone_number": "919876543210",
			"display_name": "Rahul"
		}
	}`

	os.Setenv("HMAC_SECRET", "testsecret")
	os.Setenv("BHEJNA_FROM_PHONE", "+912212345678")

	proc := NewProcessor(pool, NoopBroadcaster{})

	eventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO webhook_events (id, source, external_event_id, event_type, tenant_id, location_id, payload, status)
		VALUES ($1, 'bhejna', 'ext_rating_pt', 'message.received', $2, $3, $4, 'pending')
	`, eventID, tenantID, locationID, []byte(payload))
	if err != nil {
		t.Fatalf("Failed to insert webhook event: %v", err)
	}

	row, err := proc.claimEvent(ctx)
	if err != nil || row == nil {
		t.Fatalf("Failed to claim event: %v", err)
	}

	err = proc.processEvent(ctx, row)
	if err != nil {
		t.Fatalf("processEvent failed: %v", err)
	}

	// Verify feedback response exists and status is responded
	var ratingVal int
	err = pool.QueryRow(ctx, "SELECT rating FROM feedback_responses WHERE visit_id = $1", visitID).Scan(&ratingVal)
	if err != nil {
		t.Fatalf("Failed to find feedback response: %v", err)
	}
	if ratingVal != 4 {
		t.Errorf("Expected rating 4, got %d", ratingVal)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM feedback_requests WHERE id = $1", requestID).Scan(&status)
	if err != nil {
		t.Fatalf("Failed to query status: %v", err)
	}
	if status != "responded" {
		t.Errorf("Expected feedback request status 'responded', got %q", status)
	}
}

