package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedAppointment(t *testing.T, pool *pgxpool.Pool, custPhone string) (tenantID, locationID, aptID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tenantID, locationID = uuid.New(), uuid.New()
	custID := uuid.New()

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustExec(`INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Cancel Apt Tenant', $2, '+919999999901')`,
		tenantID, "cancel-apt-"+tenantID.String()[:8])
	mustExec(`INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'Cancel Apt Shop', $3)`,
		locationID, tenantID, "cancel-apt/loc-"+locationID.String()[:8])
	mustExec(`INSERT INTO customers (id, tenant_id, phone_number, name) VALUES ($1, $2, $3, 'Apt Customer')`,
		custID, tenantID, custPhone)

	err := pool.QueryRow(ctx, `
		INSERT INTO appointments (tenant_id, location_id, customer_id, status,
			scheduled_start_at, scheduled_end_at, total_duration_minutes)
		VALUES ($1, $2, $3, 'scheduled', NOW() + interval '2 day', NOW() + interval '2 day' + interval '30 min', 30)
		RETURNING id`, tenantID, locationID, custID).Scan(&aptID)
	if err != nil {
		t.Fatalf("seed appointment: %v", err)
	}
	return tenantID, locationID, aptID
}

func countReplies(t *testing.T, pool *pgxpool.Pool, to, contains string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM outbox_events
		WHERE type='notification.send' AND payload->>'type'='text'
		  AND payload->>'to'=$1 AND payload->'text'->>'body' ILIKE '%'||$2||'%'`, to, contains).Scan(&n)
	if err != nil {
		t.Fatalf("count replies: %v", err)
	}
	return n
}

func TestIntegration_CancelApt(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	p := NewProcessor(pool, nil)

	custPhone := "+919876500001"
	_, _, aptID := seedAppointment(t, pool, custPhone)

	// Pending day-before reminder that must be swept on cancel.
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_events (type, payload, process_after)
		VALUES ('notification.send',
			jsonb_build_object('template_code','bb_appointment_reminder','source_id',$1::text,'to',$2::text),
			NOW() + interval '1 day')`, aptID.String(), custPhone); err != nil {
		t.Fatalf("seed reminder: %v", err)
	}

	// Wrong sender: appointment untouched, not-found reply, no existence leak.
	if err := p.handleCancelApt(ctx, ClassifiedMessage{AptID: aptID.String(), SenderPhone: "+911111111111"}); err != nil {
		t.Fatalf("wrong sender: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM appointments WHERE id=$1`, aptID).Scan(&status); err != nil || status != "scheduled" {
		t.Fatalf("after wrong sender: status=%s err=%v; want scheduled", status, err)
	}
	if n := countReplies(t, pool, "+911111111111", "find that appointment"); n != 1 {
		t.Errorf("wrong sender replies = %d, want 1", n)
	}

	// Real customer cancels: status flips, audit fields set, reminder swept, confirmation reply queued.
	if err := p.handleCancelApt(ctx, ClassifiedMessage{AptID: aptID.String(), SenderPhone: custPhone}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var cancelledBy *string
	var cancelledAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, cancelled_by, cancelled_at FROM appointments WHERE id=$1`, aptID).
		Scan(&status, &cancelledBy, &cancelledAt); err != nil {
		t.Fatalf("read appointment: %v", err)
	}
	if status != "cancelled" || cancelledBy == nil || *cancelledBy != "customer" || cancelledAt == nil {
		t.Errorf("appointment = %s/%v/%v; want cancelled/customer/set", status, cancelledBy, cancelledAt)
	}
	var reminders int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE payload->>'template_code'='bb_appointment_reminder'`).Scan(&reminders)
	if reminders != 0 {
		t.Errorf("pending reminders after cancel = %d, want 0", reminders)
	}
	if n := countReplies(t, pool, custPhone, "has been cancelled"); n != 1 {
		t.Errorf("cancel confirmations = %d, want 1", n)
	}

	// Second tap: already-cancelled reply, no error, no second confirmation.
	if err := p.handleCancelApt(ctx, ClassifiedMessage{AptID: aptID.String(), SenderPhone: custPhone}); err != nil {
		t.Fatalf("double cancel: %v", err)
	}
	if n := countReplies(t, pool, custPhone, "already cancelled"); n != 1 {
		t.Errorf("already-cancelled replies = %d, want 1", n)
	}

	// Unknown ID: polite not-found reply, no error.
	if err := p.handleCancelApt(ctx, ClassifiedMessage{AptID: uuid.NewString(), SenderPhone: custPhone}); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
	if n := countReplies(t, pool, custPhone, "find that appointment"); n != 1 {
		t.Errorf("unknown-id replies = %d, want 1", n)
	}
}
