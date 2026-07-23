package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/config"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupCallNextTestServer(t *testing.T) (*Server, *pgxpool.Pool, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	// Ensure VAPID vars are present for config.Load() validation.
	if _, ok := os.LookupEnv("VAPID_PUBLIC_KEY"); !ok {
		os.Setenv("VAPID_PUBLIC_KEY", "BNhSTbMpAHFWBkBYWMjmFPuMYSoXqPuPmPqCelgQrhs9ZITAbBuznEzGm9ZfFlm-m8jkLBm4J1P7H2RqCOhFhJo")
	}
	if _, ok := os.LookupEnv("VAPID_PRIVATE_KEY"); !ok {
		os.Setenv("VAPID_PRIVATE_KEY", "tLd5AVFH6m5Y3IjUcw5hR4bTmw6RtMXRVfcQaEd9kDo")
	}
	if _, ok := os.LookupEnv("VAPID_SUBJECT"); !ok {
		os.Setenv("VAPID_SUBJECT", "mailto:ops@barberbase.in")
	}

	cfg, err := config.Load()
	require.NoError(t, err)

	pool, err := repository.InitPool(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	err = repository.Migrate(ctx, pool, "../../migrations/001_complete_schema.sql")
	require.NoError(t, err)

	// Clean tables using TRUNCATE CASCADE to prevent foreign key constraint failures
	_, err = pool.Exec(ctx, "TRUNCATE tenants, locations, staff_members, queue_sessions, queue_entries, customers, visits, outbox_events, webhook_events, staff_otps, visit_charges CASCADE")
	require.NoError(t, err)

	// Seed tenant, location, and staff members (Barber A and Barber B)
	tenantID := uuid.New()
	locationID := uuid.New()
	barberAID := uuid.New()
	barberBID := uuid.New()

	_, err = pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Test Tenant', 'test-tenant', '+919999999999')`, tenantID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, queue_routing_mode, timezone)
		VALUES ($1, $2, 'Test Location', 'test-location', 'pooled', 'Asia/Kolkata')`, locationID, tenantID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active)
		VALUES ($1, $2, $3, 'Barber A', '+919000000001', 'barber', 'idle', true)`, barberAID, tenantID, locationID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active)
		VALUES ($1, $2, $3, 'Barber B', '+919000000002', 'barber', 'idle', true)`, barberBID, tenantID, locationID)
	require.NoError(t, err)

	s := &Server{
		Pool:   pool,
		Bhejna: mockBhejna{},
		Config: cfg,
	}

	return s, pool, tenantID, locationID, barberAID, barberBID
}

// helper to create request with StaffJWT context injected
func newStaffRequest(method, url string, tenantID, locationID, staffID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
	ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
	ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
	ctx = context.WithValue(ctx, auth.CtxRole, "barber")
	return req.WithContext(ctx)
}

func seedQueueSession(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID) uuid.UUID {
	ctx := context.Background()
	sessionID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status, queue_version, last_token_number)
		VALUES ($1, $2, $3, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active', 0, 0)`, sessionID, tenantID, locationID)
	require.NoError(t, err)
	return sessionID
}

func seedQueueEntry(t *testing.T, pool *pgxpool.Pool, tenantID, locationID, sessionID uuid.UUID, customerID *uuid.UUID, presence string, requestedBarber *uuid.UUID) (uuid.UUID, uuid.UUID) {
	ctx := context.Background()
	visitID := uuid.New()
	entryID := uuid.New()

	// Seed customer if needed
	var actualCustomerID *uuid.UUID = customerID
	if customerID == nil {
		cID := uuid.New()
		_, err := pool.Exec(ctx, `
			INSERT INTO customers (id, tenant_id, phone_number, name)
			VALUES ($1, $2, $3, 'Customer')`, cID, tenantID, "+91"+uuid.New().String()[:10])
		require.NoError(t, err)
		actualCustomerID = &cID
	}

	// Seed visit
	_, err := pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 30)`, visitID, tenantID, locationID, actualCustomerID)
	require.NoError(t, err)

	// Seed queue entry
	var token int
	err = pool.QueryRow(ctx, `
		UPDATE queue_sessions
		SET last_token_number = last_token_number + 1
		WHERE id = $1
		RETURNING last_token_number`, sessionID).Scan(&token)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, requested_barber_id, priority_group, sort_key, remote_joined_at)
		VALUES ($1, $2, $3, $4, $5, 'waiting', $6, true, $7, 100, EXTRACT(EPOCH FROM NOW())::BIGINT, NOW())`,
		entryID, visitID, sessionID, actualCustomerID, token, presence, requestedBarber)
	require.NoError(t, err)

	return entryID, visitID
}

func TestCallNext_Concurrency(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	var wg sync.WaitGroup
	wg.Add(2)

	codes := make(chan int, 2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
			rec := httptest.NewRecorder()
			s.CallNextCustomer(rec, req)
			codes <- rec.Code
		}()
	}

	wg.Wait()
	close(codes)

	c1 := <-codes
	c2 := <-codes

	// Exactly one 200, one 404
	if (c1 == 200 && c2 == 404) || (c1 == 404 && c2 == 200) {
		// pass
	} else {
		t.Fatalf("Expected exactly one 200 and one 404, got %d and %d", c1, c2)
	}

	// Verify database changes
	ctx := context.Background()
	var state string
	var assignedBarber *uuid.UUID
	err := pool.QueryRow(ctx, "SELECT state, assigned_barber_id FROM queue_entries WHERE queue_session_id = $1", sessionID).Scan(&state, &assignedBarber)
	require.NoError(t, err)
	require.Equal(t, "called", state)
	require.Equal(t, barberAID, *assignedBarber)

	var queueVersion int
	err = pool.QueryRow(ctx, "SELECT queue_version FROM queue_sessions WHERE id = $1", sessionID).Scan(&queueVersion)
	require.NoError(t, err)
	require.Equal(t, 1, queueVersion)
}

func TestCallNext_BarberSpecificRoutingExclusion(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)

	// Set routing mode to barber_specific
	_, err := pool.Exec(context.Background(), "UPDATE locations SET queue_routing_mode = 'barber_specific' WHERE id = $1", locationID)
	require.NoError(t, err)

	sessionID := seedQueueSession(t, pool, tenantID, locationID)

	// Entry requested for Barber B
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", &barberBID)

	// Barber A calls next customer -> should return 404 since no customers requested Barber A
	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp struct {
		Error              string `json:"error"`
		WaitingRemoteCount int    `json:"waiting_remote_count"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "no_dispatchable_customers", resp.Error)
	require.Equal(t, 0, resp.WaitingRemoteCount)

	// Barber B calls next customer -> should succeed (200)
	req2 := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberBID)
	rec2 := httptest.NewRecorder()
	s.CallNextCustomer(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
}

func TestCallNext_HybridRouting(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, barberBID := setupCallNextTestServer(t)

	// Set routing mode to hybrid
	_, err := pool.Exec(context.Background(), "UPDATE locations SET queue_routing_mode = 'hybrid' WHERE id = $1", locationID)
	require.NoError(t, err)

	sessionID := seedQueueSession(t, pool, tenantID, locationID)

	// Customer 1: requests Barber B (arrived)
	// Customer 2: requests no barber (NULL) (arrived)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", &barberBID)
	entry2ID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	// Barber A calls next customer:
	// Negative case: Barber A cannot fetch Customer 1 (requested Barber B).
	// Positive case: Barber A should fetch Customer 2 (no requested barber).
	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var fetchedEntry QueueEntryStaff
	err = json.Unmarshal(rec.Body.Bytes(), &fetchedEntry)
	require.NoError(t, err)
	require.Equal(t, entry2ID, fetchedEntry.Id)
}

func TestCallNext_WaitingRemoteCount(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)

	// Seed 3 remote (presence != arrived) entries
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp struct {
		Error              string `json:"error"`
		WaitingRemoteCount int    `json:"waiting_remote_count"`
	}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "no_dispatchable_customers", resp.Error)
	require.Equal(t, 3, resp.WaitingRemoteCount)
}

func TestCallNext_NoSession(t *testing.T) {
	s, _, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)

	// Do not seed queue session. Call next customer immediately.
	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp struct {
		Error              string `json:"error"`
		WaitingRemoteCount int    `json:"waiting_remote_count"`
	}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "no_active_session", resp.Error)
	require.Equal(t, 0, resp.WaitingRemoteCount)
}

// CN.FIX: 404 bodies must satisfy the ErrorResponse contract (required code, message).
func TestCallNext_404ContractFields(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)

	assert404Contract := func(wantCode string) {
		req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
		rec := httptest.NewRecorder()
		s.CallNextCustomer(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)

		var resp struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, wantCode, resp.Code)
		require.NotEmpty(t, resp.Message)
	}

	// No session yet
	assert404Contract("NO_ACTIVE_SESSION")

	// Session with no dispatchable entries
	seedQueueSession(t, pool, tenantID, locationID)
	assert404Contract("NO_DISPATCHABLE_CUSTOMERS")
}

func TestCallNext_TxRollbackInsertsZeroOutbox(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	// Seed a remote customer (so dispatch will fail and trigger rollback in step 6)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	// Verify that 0 outbox events exist
	var outboxCount int
	err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM outbox_events").Scan(&outboxCount)
	require.NoError(t, err)
	require.Equal(t, 0, outboxCount)
}

// H4: an auto-snoozed web-channel entry (snoozed + is_dispatchable=false, as
// the watchdog leaves it — see TestWatchdog_WebChannelMarkAndSnooze) must be
// skipped by call-next, and ReactivateEntry must restore it to dispatchable.
func TestCallNext_SkipsSnoozedWebEntry_ReactivateRestores(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	ctx := context.Background()
	sessionID := seedQueueSession(t, pool, tenantID, locationID)

	// Entry 1 (front): web-channel, auto-snoozed by the watchdog.
	snoozedID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)
	_, err := pool.Exec(ctx, `
		UPDATE queue_entries
		SET session_channel = 'web', presence_state = 'snoozed', is_dispatchable = false,
		    near_turn_notified_at = NOW() - INTERVAL '10 minutes', snoozed_at = NOW(),
		    sort_key = 1
		WHERE id = $1`, snoozedID)
	require.NoError(t, err)

	// Entry 2 (behind): arrived and dispatchable.
	arrivedID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	_, err = pool.Exec(ctx, "UPDATE queue_entries SET sort_key = 2 WHERE id = $1", arrivedID)
	require.NoError(t, err)

	// Call-next must skip the snoozed web entry and pick the arrived one.
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID))
	require.Equal(t, http.StatusOK, rec.Code)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM queue_entries WHERE id = $1", arrivedID).Scan(&state))
	require.Equal(t, "called", state)
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM queue_entries WHERE id = $1", snoozedID).Scan(&state))
	require.Equal(t, "waiting", state, "snoozed web entry must not be called")

	// Reactivate restores the snoozed entry to waiting+arrived+dispatchable.
	rec = httptest.NewRecorder()
	s.ReactivateEntry(rec, newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/reactivate", tenantID, locationID, barberAID), UUIDv7(snoozedID))
	require.Equal(t, http.StatusOK, rec.Code, "reactivate: %s", rec.Body.String())

	var presence string
	var dispatchable bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT state, presence_state, is_dispatchable FROM queue_entries WHERE id = $1", snoozedID).Scan(&state, &presence, &dispatchable))
	require.Equal(t, "waiting", state)
	require.Equal(t, "arrived", presence)
	require.True(t, dispatchable)

	// And call-next now picks it up.
	rec = httptest.NewRecorder()
	s.CallNextCustomer(rec, newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM queue_entries WHERE id = $1", snoozedID).Scan(&state))
	require.Equal(t, "called", state)
}
