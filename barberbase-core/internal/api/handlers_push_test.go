package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"barberbase-core/internal/push"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribePush_Success(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	_ = locationID

	// Call SubscribePush
	body := SubscribePushJSONBody{
		Endpoint: "https://fcm.googleapis.com/fcm/send/some-token",
		P256dh:   "some-p256dh-key",
		Auth:     "some-auth-secret",
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := newStaffRequest(http.MethodPost, "/v1/staff/push/subscribe", tenantID, locationID, barberAID)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	s.SubscribePush(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)

	// Verify database was updated
	var endpoint, p256dh, authSecret string
	var enabled bool
	err = pool.QueryRow(context.Background(), `
		SELECT push_endpoint, push_p256dh, push_auth, push_enabled
		FROM staff_members
		WHERE id = $1`, barberAID).Scan(&endpoint, &p256dh, &authSecret, &enabled)
	require.NoError(t, err)

	assert.Equal(t, body.Endpoint, endpoint)
	assert.Equal(t, body.P256dh, p256dh)
	assert.Equal(t, body.Auth, authSecret)
	assert.True(t, enabled)
}

func TestSubscribePush_MissingFields(t *testing.T) {
	s, _, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)

	// Missing endpoint
	body := SubscribePushJSONBody{
		P256dh: "some-p256dh-key",
		Auth:   "some-auth-secret",
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := newStaffRequest(http.MethodPost, "/v1/staff/push/subscribe", tenantID, locationID, barberAID)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	s.SubscribePush(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSubscribePush_DeactivatedStaff(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)

	// Deactivate barber AID
	_, err := pool.Exec(context.Background(), "UPDATE staff_members SET is_active=false WHERE id=$1", barberAID)
	require.NoError(t, err)

	body := SubscribePushJSONBody{
		Endpoint: "https://endpoint",
		P256dh:   "dh",
		Auth:     "auth",
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := newStaffRequest(http.MethodPost, "/v1/staff/push/subscribe", tenantID, locationID, barberAID)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	s.SubscribePush(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestPushCallNext_ForgedMAC(t *testing.T) {
	s, _, _, _, _, _ := setupCallNextTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/staff/push/call-next", nil)
	req.Header.Set("X-Push-Action-Token", "invalid-token-format")

	rr := httptest.NewRecorder()
	s.PushCallNext(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestPushCallNext_WrongCommand(t *testing.T) {
	s, _, _, _, barberAID, barberBID := setupCallNextTestServer(t)
	_ = barberBID

	// Generate PAT with wrong command manually or by tampering
	secret := []byte(s.Config.HMACSecret)
	
	// Create token with wrong command
	raw := fmt.Sprintf("%s:%s:%s:%d",
		barberAID.String(), barberAID.String(), "wrong_cmd",
		time.Now().Add(1*time.Hour).Unix(),
	)

	// Let's manually build a wrong command token
	payloadB64 := base64RawURLEncode([]byte(raw))
	macB64 := base64RawURLEncode(computeMAC(secret, payloadB64))
	tamperedToken := payloadB64 + "." + macB64

	req := httptest.NewRequest(http.MethodPost, "/v1/staff/push/call-next", nil)
	req.Header.Set("X-Push-Action-Token", tamperedToken)

	rr := httptest.NewRecorder()
	s.PushCallNext(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestPushCallNext_SuccessAndRateLimit(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	sessionID := seedQueueSession(t, pool, tenantID, locationID)
	
	// Seed a customer queue entry
	entryID, _ := seedQueueEntry(t, pool, tenantID, locationID, sessionID, nil, "arrived", nil)
	_ = entryID

	// Generate a valid PAT
	secret := []byte(s.Config.HMACSecret)
	token, err := push.GeneratePAT(secret, barberAID.String(), locationID.String())
	require.NoError(t, err)

	// Call PushCallNext (First tap)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/staff/push/call-next", nil)
	req1.Header.Set("X-Push-Action-Token", token)

	rr1 := httptest.NewRecorder()
	s.PushCallNext(rr1, req1)

	assert.Equal(t, http.StatusOK, rr1.Code)

	var resp struct {
		CalledEntry          interface{} `json:"called_entry"`
		WaitingArrivedCount  int         `json:"waiting_arrived_count"`
		EstimatedWaitMinutes int         `json:"estimated_wait_minutes"`
	}
	err = json.NewDecoder(rr1.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, 0, resp.WaitingArrivedCount)
	assert.Equal(t, 0, resp.EstimatedWaitMinutes)

	// Verify queue session version incremented in PG
	var qv int
	err = pool.QueryRow(context.Background(), "SELECT queue_version FROM queue_sessions WHERE id=$1", sessionID).Scan(&qv)
	require.NoError(t, err)
	assert.True(t, qv > 0)

	// Call PushCallNext again immediately (Second tap within 3s)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/staff/push/call-next", nil)
	req2.Header.Set("X-Push-Action-Token", token)

	rr2 := httptest.NewRecorder()
	s.PushCallNext(rr2, req2)

	assert.Equal(t, http.StatusTooManyRequests, rr2.Code) // 429
}

func TestPushCallNext_DeactivatedStaff(t *testing.T) {
	s, pool, tenantID, locationID, barberAID, _ := setupCallNextTestServer(t)
	_ = tenantID

	// Generate valid token first
	secret := []byte(s.Config.HMACSecret)
	token, err := push.GeneratePAT(secret, barberAID.String(), locationID.String())
	require.NoError(t, err)

	// Deactivate staff
	_, err = pool.Exec(context.Background(), "UPDATE staff_members SET is_active=false WHERE id=$1", barberAID)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/staff/push/call-next", nil)
	req.Header.Set("X-Push-Action-Token", token)

	rr := httptest.NewRecorder()
	s.PushCallNext(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code) // 401
}

func base64RawURLEncode(in []byte) string {
	return base64.RawURLEncoding.EncodeToString(in)
}

func computeMAC(key []byte, message string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return h.Sum(nil)
}
