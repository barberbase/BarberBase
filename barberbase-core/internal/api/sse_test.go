package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"barberbase-core/internal/realtime"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSSE_ConcurrentSubscribers(t *testing.T) {
	mgr := realtime.NewManager()
	locationID := uuid.New().String()

	ch1 := mgr.Subscribe(locationID)
	ch2 := mgr.Subscribe(locationID)

	defer mgr.Unsubscribe(locationID, ch1)
	defer mgr.Unsubscribe(locationID, ch2)

	event := realtime.SSEEvent{
		Type:         "queue_changed",
		LocationID:   locationID,
		QueueVersion: 42,
	}

	mgr.Broadcast(locationID, event)

	select {
	case e := <-ch1:
		require.Equal(t, "queue_changed", e.Type)
		require.Equal(t, 42, e.QueueVersion)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for ch1 event")
	}

	select {
	case e := <-ch2:
		require.Equal(t, "queue_changed", e.Type)
		require.Equal(t, 42, e.QueueVersion)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for ch2 event")
	}
}

func TestSSE_DisconnectCleanup(t *testing.T) {
	mgr := realtime.NewManager()
	locationID := uuid.New().String()

	ch := mgr.Subscribe(locationID)

	// Simulate client disconnect context cancellation by calling Unsubscribe
	mgr.Unsubscribe(locationID, ch)

	// Channel should be closed
	select {
	case _, ok := <-ch:
		require.False(t, ok, "Expected channel to be closed")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for channel closure indication")
	}
}

func TestSSE_RollbackZeroBroadcast(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	defer pool.Close()

	mgr := realtime.NewManager()
	s.Manager = mgr

	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	// Seed a remote customer (so dispatch will fail and trigger rollback in step 6)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "remote", nil)

	ch := mgr.Subscribe(locationID.String())
	defer mgr.Unsubscribe(locationID.String(), ch)

	req := newStaffRequest(http.MethodPost, "/v1/staff/queue/call-next", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.CallNextCustomer(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	// Assert no event was received since transaction rolled back
	select {
	case e := <-ch:
		t.Fatalf("Received unexpected event due to transaction rollback: %v", e)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event received
	}
}

func TestSSE_MutationAndSnapshotTruth(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	defer pool.Close()

	mgr := realtime.NewManager()
	s.Manager = mgr

	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	// 1. Subscribe to SSE
	ch := mgr.Subscribe(locationID.String())

	// 2. Kill connection (Unsubscribe)
	mgr.Unsubscribe(locationID.String(), ch)

	// 3. Perform REST mutation (Start service)
	reqStart := newStaffRequest(http.MethodPost, "/v1/staff/queue/entries/"+entryID.String()+"/start", tenantID, locationID, barberAID)
	recStart := httptest.NewRecorder()
	
	s.StartService(recStart, reqStart, UUIDv7(entryID))
	require.Equal(t, http.StatusOK, recStart.Code)

	// 4. Request snapshot
	reqSnap := newStaffRequest(http.MethodGet, "/v1/staff/queue/snapshot", tenantID, locationID, barberAID)
	recSnap := httptest.NewRecorder()
	s.GetQueueSnapshot(recSnap, reqSnap)
	require.Equal(t, http.StatusOK, recSnap.Code)

	var snap QueueSnapshot
	err := json.Unmarshal(recSnap.Body.Bytes(), &snap)
	require.NoError(t, err)

	require.Len(t, snap.Entries, 1)
	require.Equal(t, string(InProgress), string(snap.Entries[0].State))
}

func TestSSE_SnapshotNoActiveSession(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	defer pool.Close()

	req := newStaffRequest(http.MethodGet, "/v1/staff/queue/snapshot", tenantID, locationID, barberAID)
	rec := httptest.NewRecorder()
	s.GetQueueSnapshot(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var snap QueueSnapshot
	err := json.Unmarshal(rec.Body.Bytes(), &snap)
	require.NoError(t, err)

	require.Equal(t, uuid.Nil, snap.QueueSessionId)
	require.Equal(t, 0, snap.QueueVersion)
	require.Equal(t, "closed", string(snap.SessionStatus))
	require.Empty(t, snap.Entries)
}
