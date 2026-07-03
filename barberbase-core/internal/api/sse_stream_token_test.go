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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TestSSEStreamToken_OutlivesAccessToken proves the C8.4 acceptance criterion:
// an SSE connection opened with only the stream_token stays live past the
// access-token expiry boundary. The production access TTL is 15 minutes and the
// spec boundary is the 16-minute mark; the test compresses the access TTL to 2s
// (signing the same auth.StaffClaims shape) so the boundary is crossed in real
// wall-clock time without a 16-minute test run. What matters — SSE auth happens
// only at handshake, the stream token verifies statelessly, and the expired
// access token is rejected everywhere — is identical at both timescales.
func TestSSEStreamToken_OutlivesAccessToken(t *testing.T) {
	secret := "test-jwt-secret-key-that-is-long-enough"
	tenantID := uuid.New().String()
	locID := uuid.New()
	staffID := uuid.New().String()

	// Access token with a 2s TTL — the compressed stand-in for the 15-min token.
	now := time.Now()
	accessClaims := auth.StaffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		TenantID:      tenantID,
		LocationID:    locID.String(),
		StaffMemberID: staffID,
		Role:          "barber",
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}

	streamToken, err := auth.GenerateStreamToken([]byte(secret), tenantID, locID.String(), staffID, "barber")
	if err != nil {
		t.Fatalf("generate stream token: %v", err)
	}

	s := &Server{
		Config:  &config.Config{JWTSecret: secret, HMACSecret: "test-hmac-secret"},
		Manager: realtime.NewManager(),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.SubscribeToQueueStream(w, r, locID, SubscribeToQueueStreamParams{Token: r.URL.Query().Get("token")})
	}))
	defer srv.Close()

	// 1. Open the SSE connection using ONLY the stream token.
	resp, err := http.Get(srv.URL + "/stream/" + locID.String() + "?token=" + streamToken)
	if err != nil {
		t.Fatalf("open SSE with stream token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE handshake with stream token: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// 2. Cross the access-token expiry boundary while the connection is open.
	time.Sleep(3 * time.Second)
	if _, err := auth.ParseAndVerifyToken(accessToken, []byte(secret)); !auth.IsTokenExpired(err) {
		t.Fatalf("access token should be expired by now, got err=%v", err)
	}

	// 3. The expired access token is rejected on a fresh SSE handshake with a
	//    distinguishable code the frontend can act on.
	resp2, err := http.Get(srv.URL + "/stream/" + locID.String() + "?token=" + accessToken)
	if err != nil {
		t.Fatalf("SSE connect with expired access token: %v", err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired access token handshake: got %d, want 401", resp2.StatusCode)
	}
	var errBody map[string]string
	if err := json.Unmarshal(body, &errBody); err != nil || errBody["code"] != "TOKEN_EXPIRED" {
		t.Fatalf("expired access token handshake body = %s, want code TOKEN_EXPIRED", body)
	}

	// 4. The original connection — authenticated only by the stream token — is
	//    still live: a broadcast after the expiry boundary reaches it.
	s.Manager.Broadcast(locID.String(), realtime.SSEEvent{
		Type:         "queue_changed",
		LocationID:   locID.String(),
		QueueVersion: 42,
	})

	got := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, "queue_changed") {
				got <- line
				return
			}
		}
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, `"queue_version":42`) {
			t.Fatalf("unexpected event payload: %s", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSE connection did not receive broadcast after access-token expiry — connection is not live")
	}
}

// TestSSEStreamToken_RejectedOutsideStream proves Law 20: a valid stream token
// presented to any RequireStaffJWT-protected endpoint is rejected with 403 and
// a distinguishable code, never accepted and never a generic 401.
func TestSSEStreamToken_RejectedOutsideStream(t *testing.T) {
	secret := "test-jwt-secret-key-that-is-long-enough"
	streamToken, err := auth.GenerateStreamToken([]byte(secret), "t", "l", "s", "barber")
	if err != nil {
		t.Fatalf("generate stream token: %v", err)
	}

	mw := auth.RequireStaffJWT([]byte(secret), StaffJWTScopes)
	protected := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protected handler must not run with a stream token")
	}))

	req := httptest.NewRequest("POST", "/v1/staff/queue/call-next", nil)
	req = req.WithContext(context.WithValue(req.Context(), StaffJWTScopes, []string{}))
	req.Header.Set("Authorization", "Bearer "+streamToken)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stream token on protected endpoint: got %d, want 403 (Law 20)", rec.Code)
	}
	var errBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil || errBody["code"] != "WRONG_TOKEN_SCOPE" {
		t.Fatalf("body = %s, want code WRONG_TOKEN_SCOPE", rec.Body.String())
	}
}
