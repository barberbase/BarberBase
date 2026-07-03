package api

// Concurrency/integration tests for the five staff queue mutations
// (add-walkin, skip, no-show, reactivate, reassign). Requires DATABASE_URL.
// Reuses setupCallNextTestServer / seed helpers from call_next_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"barberbase-core/internal/auth"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedVariant creates category → group → variant and returns the variant id.
func seedVariant(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	catID, groupID, variantID := uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO service_categories (id, tenant_id, location_id, name) VALUES ($1, $2, $3, 'Hair')`,
		catID, tenantID, locationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO service_groups (id, tenant_id, location_id, category_id, name) VALUES ($1, $2, $3, $4, 'Fade')`,
		groupID, tenantID, locationID, catID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO service_variants (id, tenant_id, location_id, group_id, name, duration_minutes, price_paise)
		VALUES ($1, $2, $3, $4, 'Mid Fade', 30, 15000)`,
		variantID, tenantID, locationID, groupID)
	require.NoError(t, err)
	return variantID
}

func newStaffJSONRequest(method, url string, body any, tenantID, locationID, staffID uuid.UUID, role string) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := req.Context()
	ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
	ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
	ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	return req.WithContext(ctx)
}

// Two concurrent add-walkin taps with the SAME idempotency key must create
// exactly one entry; the loser gets a replay 200 or clean 409, never a 500.
func TestAddWalkIn_ConcurrentDoubleTap(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	variantID := seedVariant(t, pool, tenantID, locationID)
	key := uuid.New().String()

	body := map[string]any{
		"variant_ids":     []string{variantID.String()},
		"customer_name":   "Uncle",
		"idempotency_key": key,
	}

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newStaffJSONRequest(http.MethodPost, "/v1/staff/queue/add-walkin", body, tenantID, locationID, barberAID, "barber")
			rec := httptest.NewRecorder()
			s.AddWalkIn(rec, req)
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)

	got := []int{<-codes, <-codes}
	okCount := 0
	for _, c := range got {
		require.Contains(t, []int{200, 409}, c, "unexpected status codes: %v", got)
		if c == 200 {
			okCount++
		}
	}
	require.GreaterOrEqual(t, okCount, 1, "at least one tap must succeed: %v", got)

	ctx := context.Background()
	var entryCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM queue_entries`).Scan(&entryCount))
	require.Equal(t, 1, entryCount, "double tap must create exactly one entry")

	var presence string
	var dispatchable bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT presence_state, is_dispatchable FROM queue_entries`).Scan(&presence, &dispatchable))
	require.Equal(t, "arrived", presence, "anonymous walk-in must be arrived (staff-tap verification)")
	require.True(t, dispatchable)

	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, `SELECT queue_version FROM queue_sessions`).Scan(&queueVersion))
	require.Equal(t, 1, queueVersion, "queue_version must increment exactly once")
}

// Two concurrent skips on the same entry: exactly one state transition, one
// queue_version bump; the second tap is a clean idempotent 200 no-op.
func TestSkipEntry_ConcurrentDoubleTap(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/skip", tenantID, locationID, barberAID)
			rec := httptest.NewRecorder()
			s.SkipEntry(rec, req, UUIDv7(entryID))
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	require.Equal(t, 200, <-codes)
	require.Equal(t, 200, <-codes)

	ctx := context.Background()
	var state string
	var dispatchable bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT state, is_dispatchable FROM queue_entries WHERE id=$1`, entryID).Scan(&state, &dispatchable))
	require.Equal(t, "skipped", state)
	require.False(t, dispatchable)

	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, `SELECT queue_version FROM queue_sessions WHERE id=$1`, sessionID).Scan(&queueVersion))
	require.Equal(t, 1, queueVersion, "second tap must not bump queue_version")
}

// Skip from an illegal state is a clean 422, no mutation.
func TestSkipEntry_InvalidState(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	_, err := pool.Exec(context.Background(), `UPDATE queue_entries SET state='in_progress' WHERE id=$1`, entryID)
	require.NoError(t, err)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/skip", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.SkipEntry(rec, req, UUIDv7(entryID))
	require.Equal(t, 422, rec.Code)

	var state string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT state FROM queue_entries WHERE id=$1`, entryID).Scan(&state))
	require.Equal(t, "in_progress", state)
}

// Two concurrent no-show taps: one transition, ONE outbox notification (no
// double WhatsApp), one version bump; second tap is a clean 200 no-op.
func TestMarkNoShow_ConcurrentDoubleTap(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "notified", nil)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE queue_entries SET state='called', session_channel='whatsapp' WHERE id=$1`, entryID)
	require.NoError(t, err)

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/no-show", tenantID, locationID, barberAID)
			rec := httptest.NewRecorder()
			s.MarkNoShow(rec, req, UUIDv7(entryID))
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	require.Equal(t, 200, <-codes)
	require.Equal(t, 200, <-codes)

	var state string
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM queue_entries WHERE id=$1`, entryID).Scan(&state))
	require.Equal(t, "no_show", state)

	var outboxCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE type='notification.send' AND payload->>'template_code'='bb_queue_noshow'`).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount, "exactly one WhatsApp notification, never two")

	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, `SELECT queue_version FROM queue_sessions WHERE id=$1`, sessionID).Scan(&queueVersion))
	require.Equal(t, 1, queueVersion)
}

// No-show on a waiting (never called) entry must be rejected 422 — the state
// machine only allows called → no_show.
func TestMarkNoShow_RequiresCalledState(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/no-show", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.MarkNoShow(rec, req, UUIDv7(entryID))
	require.Equal(t, 422, rec.Code)
}

// Two concurrent reactivates on a skipped entry: one transition, one version
// bump, at most ONE displaced-customer notification; loser is a clean 200 no-op.
func TestReactivateEntry_ConcurrentDoubleTap(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	ctx := context.Background()

	// Force the displacement path: reactivated entry lands ahead of the one
	// remote waiting entry (notify_when_people_ahead=0 → insert at front).
	_, err := pool.Exec(ctx, `UPDATE locations SET notify_when_people_ahead=0 WHERE id=$1`, locationID)
	require.NoError(t, err)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)

	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	_, err = pool.Exec(ctx, `UPDATE queue_entries SET state='skipped', is_dispatchable=false WHERE id=$1`, entryID)
	require.NoError(t, err)

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/reactivate", tenantID, locationID, barberAID)
			rec := httptest.NewRecorder()
			s.ReactivateEntry(rec, req, UUIDv7(entryID))
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	require.Equal(t, 200, <-codes)
	require.Equal(t, 200, <-codes)

	var state, presence string
	var dispatchable bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT state, presence_state, is_dispatchable FROM queue_entries WHERE id=$1`, entryID).
		Scan(&state, &presence, &dispatchable))
	require.Equal(t, "waiting", state)
	require.Equal(t, "arrived", presence)
	require.True(t, dispatchable)

	var delayedCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE type='notification.send' AND payload->>'template_code'='bb_queue_delayed'`).Scan(&delayedCount))
	require.Equal(t, 1, delayedCount, "displaced customer must be notified exactly once")

	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, `SELECT queue_version FROM queue_sessions WHERE id=$1`, sessionID).Scan(&queueVersion))
	require.Equal(t, 1, queueVersion)
}

// Reactivate on a terminal entry must be rejected 422.
func TestReactivateEntry_TerminalStateRejected(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "notified", nil)
	_, err := pool.Exec(context.Background(), `UPDATE queue_entries SET state='no_show', is_dispatchable=false WHERE id=$1`, entryID)
	require.NoError(t, err)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/reactivate", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.ReactivateEntry(rec, req, UUIDv7(entryID))
	require.Equal(t, 422, rec.Code)
}

// Barber role must get 403 (Law 20 pattern: scope rejection is 403, not 401).
func TestReassignBarber_BarberRoleForbidden(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	body := map[string]any{"new_barber_id": barberBID.String()}
	req := newStaffJSONRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/reassign", body, tenantID, locationID, barberAID, "barber")
	rec := httptest.NewRecorder()
	s.ReassignBarber(rec, req, UUIDv7(entryID))
	require.Equal(t, 403, rec.Code)
}

// Mid-service reassign: old barber → idle, new barber → cutting, atomically.
func TestReassignBarber_InProgressFlipsStatuses(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE queue_entries SET state='in_progress', assigned_barber_id=$2 WHERE id=$1`, entryID, barberAID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE staff_members SET status='cutting' WHERE id=$1`, barberAID)
	require.NoError(t, err)

	body := map[string]any{"new_barber_id": barberBID.String()}
	req := newStaffJSONRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/reassign", body, tenantID, locationID, barberAID, "manager")
	rec := httptest.NewRecorder()
	s.ReassignBarber(rec, req, UUIDv7(entryID))
	require.Equal(t, 200, rec.Code)

	var assigned uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT assigned_barber_id FROM queue_entries WHERE id=$1`, entryID).Scan(&assigned))
	require.Equal(t, barberBID, assigned)

	var oldStatus, newStatus string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM staff_members WHERE id=$1`, barberAID).Scan(&oldStatus))
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM staff_members WHERE id=$1`, barberBID).Scan(&newStatus))
	require.Equal(t, "idle", oldStatus)
	require.Equal(t, "cutting", newStatus)
}

// Two concurrent reassigns to different barbers: both serialize cleanly (no
// crash, no corrupt version), final assignment is one of the two, and staff
// statuses stay consistent (exactly one barber cutting).
func TestReassignBarber_ConcurrentDifferentTargets(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE queue_entries SET state='in_progress', assigned_barber_id=$2 WHERE id=$1`, entryID, barberAID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE staff_members SET status='cutting' WHERE id=$1`, barberAID)
	require.NoError(t, err)

	targets := []uuid.UUID{barberAID, barberBID}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		target := targets[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := map[string]any{"new_barber_id": target.String()}
			req := newStaffJSONRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/reassign", body, tenantID, locationID, barberAID, "manager")
			rec := httptest.NewRecorder()
			s.ReassignBarber(rec, req, UUIDv7(entryID))
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	require.Equal(t, 200, <-codes)
	require.Equal(t, 200, <-codes)

	var assigned uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT assigned_barber_id FROM queue_entries WHERE id=$1`, entryID).Scan(&assigned))
	require.Contains(t, targets, assigned)

	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, `SELECT queue_version FROM queue_sessions WHERE id=$1`, sessionID).Scan(&queueVersion))
	require.Equal(t, 2, queueVersion, "two serialized mutations = two version bumps")

	// The finally-assigned barber must be cutting.
	var finalStatus string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM staff_members WHERE id=$1`, assigned).Scan(&finalStatus))
	require.Equal(t, "cutting", finalStatus)
}

// An anonymous (no-phone) walk-in must be dispatchable end-to-end via Call Next.
func TestAddWalkIn_AnonymousIsDispatchable(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	variantID := seedVariant(t, pool, tenantID, locationID)

	body := map[string]any{"variant_ids": []string{variantID.String()}}
	req := newStaffJSONRequest(http.MethodPost, "/v1/staff/queue/add-walkin", body, tenantID, locationID, barberAID, "barber")
	rec := httptest.NewRecorder()
	s.AddWalkIn(rec, req)
	require.Equal(t, 200, rec.Code, "body: %s", rec.Body.String())

	callReq := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	callRec := httptest.NewRecorder()
	s.CallNextCustomer(callRec, callReq)
	require.Equal(t, 200, callRec.Code, "anonymous walk-in must be dispatchable, body: %s", callRec.Body.String())

	var state string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT state FROM queue_entries`).Scan(&state))
	require.Equal(t, "called", state)
}
