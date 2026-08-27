package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/config"
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

	// The channel must NOT be closed: closing it races with an in-flight
	// Broadcast that already copied the subscriber list, and panics the sender.
	// Unregistration alone is the cleanup — the map entry is gone.
	select {
	case e := <-ch:
		t.Fatalf("unsubscribed channel delivered %v; it must be unregistered and open", e)
	default:
	}
	require.Zero(t, mgr.LocationCount(), "location entry must be removed on unsubscribe")
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

// --- B2b hardening -----------------------------------------------------------

// sseTestServer stands up the real SubscribeToQueueStream handler behind httptest
// with a valid stream token, mirroring the setup in sse_stream_token_test.go.
func sseTestServer(t *testing.T) (*httptest.Server, *realtime.Manager, uuid.UUID, string) {
	t.Helper()
	const secret = "test-jwt-secret-key-that-is-long-enough"
	tenantID := uuid.New().String()
	locID := uuid.New()
	staffID := uuid.New().String()

	token, err := auth.GenerateStreamToken([]byte(secret), tenantID, locID.String(), staffID, "barber")
	require.NoError(t, err)

	mgr := realtime.NewManager()
	s := &Server{
		Config:  &config.Config{JWTSecret: secret, HMACSecret: "test-hmac-secret"},
		Manager: mgr,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.SubscribeToQueueStream(w, r, locID, SubscribeToQueueStreamParams{Token: r.URL.Query().Get("token")})
	}))
	t.Cleanup(srv.Close)
	return srv, mgr, locID, srv.URL + "/stream/" + locID.String() + "?token=" + token
}

// TestSSE_ConnectSignalIsImmediate is A3: the ":ok" comment must arrive on
// connect, not a heartbeat interval later. Heartbeats are pinned to 30s here so
// nothing else can produce the first byte.
func TestSSE_ConnectSignalIsImmediate(t *testing.T) {
	t.Setenv("SSE_HEARTBEAT_SECONDS", "30")
	_, _, _, url := sseTestServer(t)

	start := time.Now()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	buf := make([]byte, len(":ok\n\n"))
	_, err = io.ReadFull(resp.Body, buf)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Equal(t, ":ok\n\n", string(buf), "connect signal must be an SSE comment, not an event")
	require.Less(t, elapsed, 100*time.Millisecond, "first byte took %s", elapsed)
	t.Logf("A3 first-byte latency: %s", elapsed)
}

// TestSSE_ClientDisconnectUnregisters is A2: closing the client end runs the
// handler's deferred Unsubscribe, and no subscriber map entry survives.
func TestSSE_ClientDisconnectUnregisters(t *testing.T) {
	_, mgr, _, url := sseTestServer(t)
	require.Zero(t, mgr.LocationCount())

	resp, err := http.Get(url)
	require.NoError(t, err)
	_, err = io.ReadFull(resp.Body, make([]byte, len(":ok\n\n")))
	require.NoError(t, err)
	require.Equal(t, 1, mgr.LocationCount(), "connection must be registered while open")

	resp.Body.Close()
	require.Eventually(t, func() bool { return mgr.LocationCount() == 0 },
		5*time.Second, 20*time.Millisecond, "ctx.Done() must run the deferred Unsubscribe")
}

// TestSSE_LifetimeBound is A5: the server closes the stream at
// SSE_MAX_CONNECTION_SECONDS and the body ends cleanly (io.EOF, not a reset).
func TestSSE_LifetimeBound(t *testing.T) {
	t.Setenv("SSE_MAX_CONNECTION_SECONDS", "2")
	t.Setenv("SSE_HEARTBEAT_SECONDS", "30")
	_, mgr, _, url := sseTestServer(t)

	start := time.Now()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body) // returns when the server closes the stream
	elapsed := time.Since(start)
	require.NoError(t, err, "the close must be clean, not a connection reset")
	require.Equal(t, ":ok\n\n", string(body))
	require.Greater(t, elapsed, 1500*time.Millisecond, "closed too early: %s", elapsed)
	require.Less(t, elapsed, 6*time.Second, "closed too late: %s", elapsed)
	t.Logf("A5 lifetime bound closed after %s (configured 2s)", elapsed)
	require.Eventually(t, func() bool { return mgr.LocationCount() == 0 },
		5*time.Second, 20*time.Millisecond, "the bound must also unregister")
}

// TestSSE_ConnectionCap is A6: over the per-location cap the handler answers
// 503 + Retry-After, and a slot frees when a client goes away.
func TestSSE_ConnectionCap(t *testing.T) {
	t.Setenv("SSE_MAX_CONNS_PER_LOCATION", "2")
	t.Setenv("SSE_HEARTBEAT_SECONDS", "30")
	_, mgr, _, url := sseTestServer(t)

	var open []*http.Response
	for i := 0; i < 2; i++ {
		resp, err := http.Get(url)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_, err = io.ReadFull(resp.Body, make([]byte, len(":ok\n\n")))
		require.NoError(t, err)
		open = append(open, resp)
	}

	over, err := http.Get(url)
	require.NoError(t, err)
	defer over.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, over.StatusCode)
	require.Equal(t, "5", over.Header.Get("Retry-After"))
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(over.Body).Decode(&errBody))
	require.Equal(t, "SSE_CAPACITY", errBody["code"])

	// Free a slot; the next client is admitted.
	open[0].Body.Close()
	require.Eventually(t, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "a freed slot must be reusable")

	for _, r := range open[1:] {
		r.Body.Close()
	}
	require.Eventually(t, func() bool { return mgr.LocationCount() == 0 },
		5*time.Second, 20*time.Millisecond)
}

// TestSSE_MutationDeliversCommittedVersion is A7 over a live stream: a committed
// queue mutation reaches an open connection, and the queue_version in the event
// equals the version in the committed row. Law 8 — broadcast after commit.
func TestSSE_MutationDeliversCommittedVersion(t *testing.T) {
	t.Setenv("SSE_HEARTBEAT_SECONDS", "30")
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	defer pool.Close()

	mgr := realtime.NewManager()
	s.Manager = mgr

	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.SubscribeToQueueStream(w, r, locationID, SubscribeToQueueStreamParams{Token: r.URL.Query().Get("token")})
	}))
	defer srv.Close()

	streamToken, err := auth.GenerateStreamToken([]byte(s.Config.JWTSecret),
		tenantID.String(), locationID.String(), barberAID.String(), "barber")
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/stream/" + locationID.String() + "?token=" + streamToken)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, err = io.ReadFull(resp.Body, make([]byte, len(":ok\n\n")))
	require.NoError(t, err)

	// Commit a mutation while the stream is open.
	rec := httptest.NewRecorder()
	s.StartService(rec, newStaffRequest(http.MethodPost,
		"/v1/staff/queue/entries/"+entryID.String()+"/start", tenantID, locationID, barberAID), UUIDv7(entryID))
	require.Equal(t, http.StatusOK, rec.Code)

	var committed int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT queue_version FROM queue_sessions WHERE id = $1`, sessionID).Scan(&committed))

	// Read frames until the queue_changed event arrives.
	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	var got realtime.SSEEvent
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &got))
		if got.Type == "queue_changed" {
			break
		}
	}
	require.Equal(t, "queue_changed", got.Type, "mutation event never arrived on the open stream")
	require.Equal(t, committed, got.QueueVersion, "event version must match the committed row")
}
