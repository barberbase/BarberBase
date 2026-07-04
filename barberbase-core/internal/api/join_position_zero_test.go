package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
)

// A customer joining an empty queue (position 0) never crosses the watchdog's
// near-turn threshold, so JoinQueue must select bb_you_are_next at insert time.
// A second customer (position 1) still gets the normal bb_queue_joined.
func TestJoinQueue_PositionZeroGetsYouAreNext(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)

	join := func(phone, name string) {
		payload := map[string]interface{}{
			"location_id":     locationID.String(),
			"variant_ids":     []string{variantID.String()},
			"idempotency_key": uuid.New().String(),
			"initiated_via":   "web_form",
			"phone_number":    phone,
			"customer_name":   name,
		}
		bodyBytes, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
		rec := httptest.NewRecorder()
		s.JoinQueue(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("join for %s: expected 200, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	countTemplate := func(code string) int {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM outbox_events
			WHERE type='notification.send' AND payload->>'template_code'=$1`, code).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", code, err)
		}
		return n
	}

	// First join: empty queue → position 0 → bb_you_are_next, not bb_queue_joined.
	join("+919876543210", "Alice")
	if got := countTemplate("bb_you_are_next"); got != 1 {
		t.Fatalf("expected 1 bb_you_are_next after position-0 join, got %d", got)
	}
	if got := countTemplate("bb_queue_joined"); got != 0 {
		t.Fatalf("expected 0 bb_queue_joined after position-0 join, got %d", got)
	}

	// bb_you_are_next payload shape: 2 body params (shop_name, token_number), single URL button.
	var payloadRaw []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT payload FROM outbox_events
		WHERE type='notification.send' AND payload->>'template_code'='bb_you_are_next'`).Scan(&payloadRaw); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		NotificationType string `json:"notification_type"`
		Components       []struct {
			Type       string                   `json:"type"`
			SubType    string                   `json:"sub_type"`
			Parameters []map[string]interface{} `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(payloadRaw, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.NotificationType != "you_are_next" {
		t.Errorf("notification_type: expected you_are_next, got %s", p.NotificationType)
	}
	if len(p.Components) != 2 {
		t.Fatalf("expected 2 components (body + url button), got %d", len(p.Components))
	}
	if p.Components[0].Type != "body" || len(p.Components[0].Parameters) != 2 {
		t.Errorf("body: expected 2 params (shop_name, token_number), got %d", len(p.Components[0].Parameters))
	}
	if p.Components[1].SubType != "url" {
		t.Errorf("button: expected url sub_type, got %s", p.Components[1].SubType)
	}

	// Second join: 1 person ahead → normal bb_queue_joined.
	join("+919876543211", "Bob")
	if got := countTemplate("bb_queue_joined"); got != 1 {
		t.Fatalf("expected 1 bb_queue_joined after position-1 join, got %d", got)
	}
	if got := countTemplate("bb_you_are_next"); got != 1 {
		t.Fatalf("expected still 1 bb_you_are_next after position-1 join, got %d", got)
	}
}
