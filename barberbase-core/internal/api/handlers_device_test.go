package api

// Device call-next layer tests. Replay-safety under concurrent presses is
// covered by TestCallNext_Concurrency: all three transports (JWT, PAT, device)
// funnel into queue.CallNext, which serializes on the queue_sessions FOR UPDATE
// lock — a device press races a dashboard tap exactly like two dashboard taps.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"barberbase-core/internal/device"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedStationDevice(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, active bool) (uuid.UUID, string) {
	t.Helper()
	secret, err := device.NewSecret()
	require.NoError(t, err)
	deviceID := uuid.New()
	_, err = pool.Exec(context.Background(), `
		INSERT INTO station_devices (id, tenant_id, location_id, label, token_hash, is_active)
		VALUES ($1, $2, $3, 'Test Device', $4, $5)`,
		deviceID, tenantID, locationID, device.Hash(secret), active)
	require.NoError(t, err)
	return deviceID, secret
}

func seedStationButton(t *testing.T, pool *pgxpool.Pool, deviceID uuid.UUID, code string, staffID *uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO station_buttons (device_id, button_code, staff_member_id, label)
		VALUES ($1, $2, $3, 'Test Button')`, deviceID, code, staffID)
	require.NoError(t, err)
}

func deviceCallNextReq(token, buttonCode string, pressedAt int64) *http.Request {
	body := map[string]any{"button_code": buttonCode}
	if pressedAt != 0 {
		body["pressed_at"] = pressedAt
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/device/call-next", bytes.NewReader(b))
	if token != "" {
		req.Header.Set("X-Device-Token", token)
	}
	return req
}

func TestDeviceCallNext_TokenAuth(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)

	// Garbage token → 401
	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq("bbd_garbage-token-value", "B1", 0))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Missing token → 401
	rec = httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq("", "B1", 0))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Revoked device → 401
	_, revokedSecret := seedStationDevice(t, pool, tenantID, locationID, false)
	rec = httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(revokedSecret, "B1", 0))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeviceCallNext_StaleDiscarded(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil)

	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(secret, "B1", time.Now().Add(-2*time.Minute).Unix()))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "stale_discarded", resp.Result)

	// No mutation — entry untouched.
	var state string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT state FROM queue_entries WHERE id = $1`, entryID).Scan(&state))
	require.Equal(t, "waiting", state)

	// Liveness still recorded.
	var lastSeen *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM station_devices WHERE id = $1`, deviceID).Scan(&lastSeen))
	require.NotNil(t, lastSeen)
}

func TestDeviceCallNext_UnknownButton(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil)

	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(secret, "B9", 0))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeviceCallNext_RateLimit(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil)

	rec1 := httptest.NewRecorder()
	s.DeviceCallNext(rec1, deviceCallNextReq(secret, "B1", 0))
	require.Equal(t, http.StatusOK, rec1.Code)

	rec2 := httptest.NewRecorder()
	s.DeviceCallNext(rec2, deviceCallNextReq(secret, "B1", 0))
	require.Equal(t, http.StatusTooManyRequests, rec2.Code)
}

func TestDeviceCallNext_BarberBoundAdvance(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", &barberAID)

	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(secret, "B1", time.Now().Unix()))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Result            string `json:"result"`
		CalledTokenNumber int    `json:"called_token_number"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "advanced", resp.Result)
	require.Equal(t, 1, resp.CalledTokenNumber)

	// Domain fn received the button's staff_member_id: entry assigned to Barber A.
	var state string
	var assigned *uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT state, assigned_barber_id FROM queue_entries WHERE id = $1`, entryID).Scan(&state, &assigned))
	assert.Equal(t, "called", state)
	require.NotNil(t, assigned)
	assert.Equal(t, barberAID, *assigned)
}

func TestDeviceCallNext_PooledButtonAdvance(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil) // NULL staff = pooled/shop-wide

	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(secret, "B1", 0))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "advanced", resp.Result)

	// Pooled press: entry called with no barber assignment.
	var state string
	var assigned *uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT state, assigned_barber_id FROM queue_entries WHERE id = $1`, entryID).Scan(&state, &assigned))
	assert.Equal(t, "called", state)
	assert.Nil(t, assigned)
}

func TestDeviceCallNext_NoWaiting(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	seedQueueSession(t, pool, tenantID, locationID)

	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil)

	rec := httptest.NewRecorder()
	s.DeviceCallNext(rec, deviceCallNextReq(secret, "B1", 0))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "no_waiting", resp.Result)
}

func TestCreateStationDevice_SecretShownOnceHashStored(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"tenant_id":   tenantID,
		"location_id": locationID,
		"label":       "Front Desk Gateway",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/devices", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.CreateStationDevice(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		ID     uuid.UUID `json:"id"`
		Secret string    `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.Secret, "bbd_")

	// Only the hash is stored, and it matches the returned secret.
	var storedHash []byte
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT token_hash FROM station_devices WHERE id = $1`, resp.ID).Scan(&storedHash))
	require.Equal(t, device.Hash(resp.Secret), storedHash)
}

// Router-level: the explicit device/admin routes must shadow the generated /v1
// mount — the generated wrapper enforces no apiKey scheme, so if shadowing broke,
// admin device CRUD would be reachable unauthenticated. This test pins that down.
func TestDeviceRoutes_FullRouterAuth(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	router := NewRouter(s, []byte(s.Config.JWTSecret))

	// Unauthenticated admin device create → 401 (PlatformAdminKeyMiddleware).
	body, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "location_id": locationID, "label": "x"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/devices", bytes.NewReader(body)))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Wrong platform key → 401.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/devices", bytes.NewReader(body))
	req.Header.Set("X-Platform-Admin-Key", "wrong-key")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Device call-next reachable without StaffJWT — its auth is the device token (401 here, not a JWT error).
	deviceID, secret := seedStationDevice(t, pool, tenantID, locationID, true)
	seedStationButton(t, pool, deviceID, "B1", nil)
	seedQueueSession(t, pool, tenantID, locationID)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, deviceCallNextReq(secret, "B1", 0))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "no_waiting", resp.Result)
}

// D1.2: negative-path pins over the FULL router (NewRouter). These exist to
// fail loudly if a future make gen-api regen ever re-exposes the dead
// unauthenticated generated-mount wrappers that the manual routes in
// RegisterManualRoutes shadow. Each bad-auth case must produce 401 with ZERO
// queue mutation and ZERO last_seen_at update.
func TestDeviceCallNext_NegativeAuthPins(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	router := NewRouter(s, []byte(s.Config.JWTSecret))
	ctx := context.Background()

	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)

	revokedID, revokedSecret := seedStationDevice(t, pool, tenantID, locationID, false)
	seedStationButton(t, pool, revokedID, "B1", nil)

	send := func(token string) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, deviceCallNextReq(token, "B1", 0))
		return rec.Code
	}

	require.Equal(t, http.StatusUnauthorized, send(""), "no token")
	require.Equal(t, http.StatusUnauthorized, send("bbd_garbage"), "garbage token")
	require.Equal(t, http.StatusUnauthorized, send(revokedSecret), "revoked device token")

	// Zero queue mutation.
	var state string
	var queueVersion int
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM queue_entries WHERE id = $1", entryID).Scan(&state))
	require.Equal(t, "waiting", state)
	require.NoError(t, pool.QueryRow(ctx, "SELECT queue_version FROM queue_sessions WHERE id = $1", sessionID).Scan(&queueVersion))
	require.Equal(t, 0, queueVersion)

	// Zero liveness update for the revoked device.
	var lastSeen *time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT last_seen_at FROM station_devices WHERE id = $1", revokedID).Scan(&lastSeen))
	require.Nil(t, lastSeen, "revoked device must not get last_seen_at on rejected auth")
}

// D1.2: PLATFORM_ADMIN_KEY-gated device CRUD — bad key trio over the full
// router, asserting zero rows created (TestDeviceRoutes_FullRouterAuth pins
// the status codes; this pins the no-mutation contract).
func TestCreateStationDevice_NegativeAuthPins(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupCallNextTestServer(t)
	router := NewRouter(s, []byte(s.Config.JWTSecret))
	ctx := context.Background()

	body, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "location_id": locationID, "label": "pin"})
	send := func(key string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/devices", bytes.NewReader(body))
		if key != "" {
			req.Header.Set("X-Platform-Admin-Key", key)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusUnauthorized, send(""), "no key")
	require.Equal(t, http.StatusUnauthorized, send("wrong-key"), "garbage key")

	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT COUNT(*) FROM station_devices WHERE tenant_id = $1", tenantID).Scan(&count))
	require.Equal(t, 0, count, "rejected auth must create zero devices")
}
