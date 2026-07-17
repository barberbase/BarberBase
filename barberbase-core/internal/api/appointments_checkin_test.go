package api

import (
	"context"
	"os"
	"testing"
	"time"

	"barberbase-core/internal/domain/queue"
	"github.com/google/uuid"
)

// Book → check-in end to end: the appointment becomes a queue_entry with the
// protected priority_group (50), presence 'arrived' (structurally outside the
// watchdog near-turn/auto-snooze path), and a canonical /q/status magic link.
// A second check-in must fail (status no longer 'scheduled').
func TestAppointment_BookThenCheckIn(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	_, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)
	for d := 0; d < 7; d++ {
		_, _ = pool.Exec(ctx, `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
	}

	repo := queue.QueueRepository{Pool: pool}
	name := "Apt Customer"
	booked, err := repo.BookAppointment(ctx, tenantID, queue.BookAppointmentRequest{
		LocationID:       locationID,
		VariantIDs:       []uuid.UUID{variantID},
		PartySize:        1,
		ScheduledStartAt: time.Now().Add(30 * time.Minute), // today
		PhoneNumber:      "+919876543211",
		CustomerName:     &name,
		InitiatedVia:     "staff_dashboard",
		IdempotencyKey:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("BookAppointment: %v", err)
	}

	// Booking must enqueue a confirmation with a sendable payload (to + location_id).
	var confCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE type='notification.send'
		  AND payload->>'template_code'='bb_appointment_confirmed'
		  AND payload->>'to' <> '' AND payload->>'location_id' <> ''`).Scan(&confCount); err != nil || confCount != 1 {
		t.Fatalf("expected 1 sendable confirmation outbox event, got %d (err %v)", confCount, err)
	}

	res, err := repo.CheckInAppointment(ctx, tenantID, locationID, booked.AppointmentID)
	if err != nil {
		t.Fatalf("CheckInAppointment: %v", err)
	}
	if res.NewQueueVersion == 0 {
		t.Error("expected NewQueueVersion > 0 for SSE broadcast")
	}
	if want := "https://barberbase.in/q/status?t="; len(res.MagicLink) <= len(want) || res.MagicLink[:len(want)] != want {
		t.Errorf("magic link not canonical /q/status form: %q", res.MagicLink)
	}

	var prio int
	var presence, state, entryType string
	err = pool.QueryRow(ctx, `
		SELECT qe.priority_group, qe.presence_state, qe.state, v.entry_type
		FROM queue_entries qe JOIN visits v ON v.id = qe.visit_id
		WHERE qe.id = $1`, res.QueueEntryID).Scan(&prio, &presence, &state, &entryType)
	if err != nil {
		t.Fatalf("entry query: %v", err)
	}
	if prio != 50 || presence != "arrived" || state != "waiting" || entryType != "appointment" {
		t.Errorf("entry = prio %d presence %s state %s type %s; want 50/arrived/waiting/appointment", prio, presence, state, entryType)
	}

	// Magic link token stored on the visit must round-trip the customer session path.
	var storedToken string
	if err := pool.QueryRow(ctx, `SELECT magic_link_token_hash FROM visits WHERE id=$1`, res.VisitID).Scan(&storedToken); err != nil || storedToken == "" {
		t.Fatalf("visit magic token missing: %v", err)
	}

	if _, err := repo.CheckInAppointment(ctx, tenantID, locationID, booked.AppointmentID); err == nil {
		t.Error("second check-in should fail (appointment no longer 'scheduled')")
	}
}

// A requested barber on the appointment must survive check-in onto the queue
// entry — barber_specific dispatch matches only requested_barber_id = caller
// (Law 12); dropping it would strand the entry undispatchable.
func TestAppointment_CheckInPropagatesRequestedBarber(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	_, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)
	for d := 0; d < 7; d++ {
		_, _ = pool.Exec(ctx, `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
	}

	repo := queue.QueueRepository{Pool: pool}
	booked, err := repo.BookAppointment(ctx, tenantID, queue.BookAppointmentRequest{
		LocationID:        locationID,
		VariantIDs:        []uuid.UUID{variantID},
		PartySize:         1,
		ScheduledStartAt:  time.Now().Add(30 * time.Minute),
		PhoneNumber:       "+919876543212",
		RequestedBarberID: &staffID,
		InitiatedVia:      "staff_dashboard",
		IdempotencyKey:    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("BookAppointment: %v", err)
	}

	res, err := repo.CheckInAppointment(ctx, tenantID, locationID, booked.AppointmentID)
	if err != nil {
		t.Fatalf("CheckInAppointment: %v", err)
	}

	var gotBarber *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT requested_barber_id FROM queue_entries WHERE id=$1`, res.QueueEntryID).Scan(&gotBarber); err != nil {
		t.Fatalf("entry query: %v", err)
	}
	if gotBarber == nil || *gotBarber != staffID {
		t.Errorf("requested_barber_id = %v; want %s", gotBarber, staffID)
	}
}
