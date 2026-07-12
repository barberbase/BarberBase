package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"barberbase-core/internal/bhejna"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type countingBhejna struct {
	sends int
}

func (c *countingBhejna) SendText(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTextReq) (*bhejna.SendResult, error) {
	return &bhejna.SendResult{JobID: "text-job"}, nil
}

func (c *countingBhejna) SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req bhejna.SendTemplateReq) (*bhejna.SendResult, error) {
	c.sends++
	return &bhejna.SendResult{JobID: "job-" + uuid.NewString()}, nil
}

// insertNotificationOutboxEvent mimics what the join / call-next producers now
// emit: same template + same queue_entry source, each with its own outbox row.
func insertNotificationOutboxEvent(t *testing.T, pool *pgxpool.Pool, tenantID, locationID, entryID uuid.UUID, template string) *OutboxEvent {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"template_code":     template,
		"to":                "+919876500001",
		"location_id":       locationID.String(),
		"notification_type": TemplateToNotificationType[template],
		"source_type":       "queue_entry",
		"source_id":         entryID.String(),
	})
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1, 'notification.send', $2, NOW())
		RETURNING id`, tenantID, payload).Scan(&id)
	if err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}
	tid := tenantID.String()
	return &OutboxEvent{
		ID:        id,
		TenantID:  &tid,
		Type:      "notification.send",
		Payload:   payload,
		CreatedAt: time.Now(),
	}
}

// Reproduces the double-send race: a customer joins in a call-next-eligible
// position (producer 1 enqueues bb_you_are_next), staff call-next fires moments
// later (producer 2 enqueues bb_you_are_next for the same entry). Two distinct
// outbox_events exist — Bhejna's per-event idempotency key cannot dedup them.
// The handler's dedup gate must send exactly once.
func TestNotificationHandle_DedupSameTemplateSameEntry(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tenantID := uuid.New()
	locationID := uuid.New()
	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)

	entryID := uuid.New() // notification_events.source_id has no FK — a bare UUID is enough

	mock := &countingBhejna{}
	h := NewHandler(pool, mock)

	evtJoin := insertNotificationOutboxEvent(t, pool, tenantID, locationID, entryID, "bb_you_are_next")
	evtCallNext := insertNotificationOutboxEvent(t, pool, tenantID, locationID, entryID, "bb_you_are_next")

	if err := h.Handle(ctx, pool, evtJoin); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if err := h.Handle(ctx, pool, evtCallNext); err != nil {
		t.Fatalf("second Handle (should be silent no-op): %v", err)
	}

	if mock.sends != 1 {
		t.Errorf("expected exactly 1 Bhejna send, got %d", mock.sends)
	}

	var rows int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM notification_events
		WHERE template_code = 'bb_you_are_next'
		  AND source_type = 'queue_entry' AND source_id = $1
		  AND status IN ('queued','sent')`, entryID).Scan(&rows)
	if err != nil {
		t.Fatalf("count notification_events: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected exactly 1 notification_events row for the entry, got %d", rows)
	}

	// Control: same template for a different entry is NOT deduped.
	otherEntryID := uuid.New()
	evtOther := insertNotificationOutboxEvent(t, pool, tenantID, locationID, otherEntryID, "bb_you_are_next")
	if err := h.Handle(ctx, pool, evtOther); err != nil {
		t.Fatalf("third Handle (different entry): %v", err)
	}
	if mock.sends != 2 {
		t.Errorf("expected different-entry send to go through (2 total), got %d", mock.sends)
	}

	// Control: a different template for the same entry is NOT deduped.
	evtNearTurn := insertNotificationOutboxEvent(t, pool, tenantID, locationID, entryID, "bb_near_turn")
	if err := h.Handle(ctx, pool, evtNearTurn); err != nil {
		t.Fatalf("fourth Handle (different template): %v", err)
	}
	if mock.sends != 3 {
		t.Errorf("expected different-template send to go through (3 total), got %d", mock.sends)
	}
}

// Sends older than the dedup window do not suppress a new send.
func TestNotificationHandle_DedupWindowExpires(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tenantID := uuid.New()
	locationID := uuid.New()
	seedTenantAndLocation(t, pool, ctx, tenantID, locationID)
	entryID := uuid.New()

	// A 'sent' row from 3 minutes ago — outside the 2-minute window.
	_, err := pool.Exec(ctx, `
		INSERT INTO notification_events
			(tenant_id, location_id, channel, notification_type, quota_type,
			 recipient_phone, template_code, status, source_type, source_id, created_at, sent_at)
		VALUES ($1, $2, 'whatsapp', 'you_are_next', 'whatsapp_transactional',
			 '+919876500001', 'bb_you_are_next', 'sent', 'queue_entry', $3,
			 NOW() - INTERVAL '3 minutes', NOW() - INTERVAL '3 minutes')`,
		tenantID, locationID, entryID)
	if err != nil {
		t.Fatalf("seed old notification_events row: %v", err)
	}

	mock := &countingBhejna{}
	h := NewHandler(pool, mock)
	evt := insertNotificationOutboxEvent(t, pool, tenantID, locationID, entryID, "bb_you_are_next")
	if err := h.Handle(ctx, pool, evt); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if mock.sends != 1 {
		t.Errorf("expected send outside window to go through, got %d sends", mock.sends)
	}
}
