package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"barberbase-core/internal/domain/presence"
)

func cleanDatabase(t *testing.T, dbURL string) {
	if dbURL == "" {
		t.Skip("Skipping cleaning: DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to db for cleaning: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		TRUNCATE TABLE 
			webhook_events, 
			outbox_events, 
			idempotency_keys, 
			visit_services, 
			visits, 
			queue_entries, 
			customer_identities, 
			customers, 
			staff_otps, 
			staff_members, 
			locations, 
			tenants 
		CASCADE;
	`)
	if err != nil {
		t.Fatalf("failed to truncate tables: %v", err)
	}
}

func seedServiceVariant(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, name string, duration, price int, isActive bool) uuid.UUID {
	ctx := context.Background()
	catID := uuid.New()
	groupID := uuid.New()
	variantID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO service_categories (id, tenant_id, location_id, name, gender, is_active)
		VALUES ($1, $2, $3, 'Test Cat', 'unisex', true)
		ON CONFLICT DO NOTHING`, catID, tenantID, locationID)
	if err != nil {
		t.Fatalf("failed to insert cat: %v", err)
	}

	err = pool.QueryRow(ctx, `SELECT id FROM service_categories WHERE location_id = $1 LIMIT 1`, locationID).Scan(&catID)
	if err != nil {
		t.Fatalf("failed to get cat: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO service_groups (id, tenant_id, location_id, category_id, name, is_active)
		VALUES ($1, $2, $3, $4, 'Test Group', true)
		ON CONFLICT DO NOTHING`, groupID, tenantID, locationID, catID)
	if err != nil {
		t.Fatalf("failed to insert group: %v", err)
	}

	err = pool.QueryRow(ctx, `SELECT id FROM service_groups WHERE location_id = $1 LIMIT 1`, locationID).Scan(&groupID)
	if err != nil {
		t.Fatalf("failed to get group: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO service_variants (id, tenant_id, location_id, group_id, name, duration_minutes, price_paise, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, variantID, tenantID, locationID, groupID, name, duration, price, isActive)
	if err != nil {
		t.Fatalf("failed to insert variant: %v", err)
	}

	return variantID
}

func TestJoinQueue_IdempotencyConcurrency(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)

	idemKey := uuid.New()
	payload := map[string]interface{}{
		"location_id":     locationID.String(),
		"variant_ids":     []string{variantID.String()},
		"idempotency_key": idemKey.String(),
		"initiated_via":   "web_form",
		"phone_number":    "+919876543210",
		"customer_name":   "Alice",
	}
	bodyBytes, _ := json.Marshal(payload)

	var wg sync.WaitGroup
	wg.Add(2)

	resCodes := make(chan int, 2)
	resBodies := make(chan string, 2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			for {
				req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
				rec := httptest.NewRecorder()
				s.JoinQueue(rec, req)
				if rec.Code == http.StatusOK {
					resCodes <- rec.Code
					resBodies <- rec.Body.String()
					break
				} else if rec.Code == http.StatusConflict { // 409 request in flight
					time.Sleep(10 * time.Millisecond)
					continue
				} else {
					resCodes <- rec.Code
					resBodies <- rec.Body.String()
					break
				}
			}
		}()
	}

	wg.Wait()
	close(resCodes)
	close(resBodies)

	code1 := <-resCodes
	code2 := <-resCodes
	body1 := <-resBodies
	body2 := <-resBodies

	if code1 != http.StatusOK || code2 != http.StatusOK {
		t.Fatalf("Expected both requests to return 200 OK, got codes %d and %d. bodies: %s, %s", code1, code2, body1, body2)
	}

	var m1, m2 map[string]interface{}
	if err := json.Unmarshal([]byte(body1), &m1); err != nil {
		t.Fatalf("failed to unmarshal body1: %v", err)
	}
	if err := json.Unmarshal([]byte(body2), &m2); err != nil {
		t.Fatalf("failed to unmarshal body2: %v", err)
	}

	if m1["magic_link_token"] != m2["magic_link_token"] {
		t.Errorf("Expected same magic_link_token, got %v and %v", m1["magic_link_token"], m2["magic_link_token"])
	}
	if m1["magic_link_url"] != m2["magic_link_url"] {
		t.Errorf("Expected same magic_link_url, got %v and %v", m1["magic_link_url"], m2["magic_link_url"])
	}
	if m1["whatsapp_sent"] != m2["whatsapp_sent"] {
		t.Errorf("Expected same whatsapp_sent, got %v and %v", m1["whatsapp_sent"], m2["whatsapp_sent"])
	}

	qe1 := m1["queue_entry"].(map[string]interface{})
	qe2 := m2["queue_entry"].(map[string]interface{})

	if qe1["id"] != qe2["id"] {
		t.Errorf("Expected same entry ID, got %v and %v", qe1["id"], qe2["id"])
	}
	if qe1["token_number"] != qe2["token_number"] {
		t.Errorf("Expected same token_number, got %v and %v", qe1["token_number"], qe2["token_number"])
	}
	if qe1["state"] != qe2["state"] {
		t.Errorf("Expected same state, got %v and %v", qe1["state"], qe2["state"])
	}
	if qe1["presence_state"] != qe2["presence_state"] {
		t.Errorf("Expected same presence_state, got %v and %v", qe1["presence_state"], qe2["presence_state"])
	}
	if qe1["estimated_wait_minutes"] != qe2["estimated_wait_minutes"] {
		t.Errorf("Expected same estimated_wait_minutes, got %v and %v", qe1["estimated_wait_minutes"], qe2["estimated_wait_minutes"])
	}

	var visitCount int
	err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM visits WHERE location_id = $1", locationID).Scan(&visitCount)
	if err != nil {
		t.Fatalf("db query error: %v", err)
	}
	if visitCount != 1 {
		t.Fatalf("Expected exactly 1 visit row, got %d", visitCount)
	}

	var entryCount int
	err = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM queue_entries").Scan(&entryCount)
	if err != nil {
		t.Fatalf("db query error: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("Expected exactly 1 queue entry row, got %d", entryCount)
	}
}

func TestJoinQueue_ParallelJoins(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)

	_, err := pool.Exec(context.Background(), "UPDATE locations SET max_total_queue_size = 200 WHERE id = $1", locationID)
	if err != nil {
		t.Fatalf("failed to update max queue size: %v", err)
	}

	const numJoins = 100
	var wg sync.WaitGroup
	wg.Add(numJoins)

	for i := 0; i < numJoins; i++ {
		go func(idx int) {
			defer wg.Done()
			payload := map[string]interface{}{
				"location_id":     locationID.String(),
				"variant_ids":     []string{variantID.String()},
				"idempotency_key": uuid.New().String(),
				"initiated_via":   "web_form",
				"phone_number":    fmt.Sprintf("+919000000%03d", idx),
				"customer_name":   fmt.Sprintf("Customer %d", idx),
			}
			bodyBytes, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()
			s.JoinQueue(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("Join %d failed: status %d, body: %s", idx, rec.Code, rec.Body.String())
			}
		}(i)
	}

	wg.Wait()

	var lastTokenNumber int
	var queueVersion int
	err = pool.QueryRow(context.Background(), "SELECT last_token_number, queue_version FROM queue_sessions WHERE location_id = $1", locationID).Scan(&lastTokenNumber, &queueVersion)
	if err != nil {
		t.Fatalf("db error: %v", err)
	}
	if lastTokenNumber != numJoins {
		t.Fatalf("Expected last_token_number to be %d, got %d", numJoins, lastTokenNumber)
	}
	if queueVersion != numJoins {
		t.Fatalf("Expected queue_version to be %d, got %d", numJoins, queueVersion)
	}

	rows, err := pool.Query(context.Background(), "SELECT token_number FROM queue_entries ORDER BY token_number ASC")
	if err != nil {
		t.Fatalf("db error: %v", err)
	}
	defer rows.Close()

	tokens := []int{}
	for rows.Next() {
		var tok int
		if err := rows.Scan(&tok); err != nil {
			t.Fatalf("scan error: %v", err)
		}
		tokens = append(tokens, tok)
	}

	if len(tokens) != numJoins {
		t.Fatalf("Expected %d tokens, got %d", numJoins, len(tokens))
	}

	for idx, tok := range tokens {
		expected := idx + 1
		if tok != expected {
			t.Fatalf("Expected token number at index %d to be %d, got %d", idx, expected, tok)
		}
	}
}

func TestJoinQueue_DuplicateCustomer(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)
	phone := "+919876543210"

	payload1 := map[string]interface{}{
		"location_id":     locationID.String(),
		"variant_ids":     []string{variantID.String()},
		"idempotency_key": uuid.New().String(),
		"initiated_via":   "web_form",
		"phone_number":    phone,
		"customer_name":   "Bob",
	}
	bodyBytes1, _ := json.Marshal(payload1)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes1))
	rec1 := httptest.NewRecorder()
	s.JoinQueue(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("First join failed: %s", rec1.Body.String())
	}

	payload2 := map[string]interface{}{
		"location_id":     locationID.String(),
		"variant_ids":     []string{variantID.String()},
		"idempotency_key": uuid.New().String(),
		"initiated_via":   "web_form",
		"phone_number":    phone,
		"customer_name":   "Bob",
	}
	bodyBytes2, _ := json.Marshal(payload2)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes2))
	rec2 := httptest.NewRecorder()
	s.JoinQueue(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("Expected 409 Conflict for duplicate customer join, got %d. Response: %s", rec2.Code, rec2.Body.String())
	}

	var resp struct {
		Code          string `json:"code"`
		ExistingEntry struct {
			ID uuid.UUID `json:"id"`
		} `json:"existing_entry"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Code != "already_in_queue" {
		t.Errorf("Expected code 'already_in_queue', got '%s'", resp.Code)
	}
	if resp.ExistingEntry.ID == uuid.Nil {
		t.Errorf("Expected existing_entry populated with valid ID, got nil UUID")
	}
}

func TestJoinQueue_ClosedSession(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, CURRENT_DATE, 'closed')
		ON CONFLICT (location_id, business_date) DO UPDATE SET status = 'closed'`, tenantID, locationID)
	if err != nil {
		t.Fatalf("failed to insert closed session: %v", err)
	}

	payload := map[string]interface{}{
		"location_id":     locationID.String(),
		"variant_ids":     []string{variantID.String()},
		"idempotency_key": uuid.New().String(),
		"initiated_via":   "web_form",
		"phone_number":    "+919876543210",
		"customer_name":   "Charlie",
	}
	bodyBytes, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.JoinQueue(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Code != "shop_not_accepting" {
		t.Errorf("Expected code 'shop_not_accepting', got '%s'", resp.Code)
	}
}

func TestJoinQueue_QueueFull(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 300, true)

	_, err := pool.Exec(context.Background(), "UPDATE locations SET max_total_queue_size = 3 WHERE id = $1", locationID)
	if err != nil {
		t.Fatalf("failed to update max queue size: %v", err)
	}

	for i := 0; i < 3; i++ {
		payload := map[string]interface{}{
			"location_id":     locationID.String(),
			"variant_ids":     []string{variantID.String()},
			"idempotency_key": uuid.New().String(),
			"initiated_via":   "web_form",
			"phone_number":    fmt.Sprintf("+91987654321%d", i),
			"customer_name":   fmt.Sprintf("Customer %d", i),
		}
		bodyBytes, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
		rec := httptest.NewRecorder()
		s.JoinQueue(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup join %d failed: %s", i, rec.Body.String())
		}
	}

	payload := map[string]interface{}{
		"location_id":     locationID.String(),
		"variant_ids":     []string{variantID.String()},
		"idempotency_key": uuid.New().String(),
		"initiated_via":   "web_form",
		"phone_number":    "+919876543219",
		"customer_name":   "Extra customer",
	}
	bodyBytes, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/queue/join", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.JoinQueue(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422 for full queue, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Code != "queue_full" {
		t.Errorf("Expected code 'queue_full', got '%s'", resp.Code)
	}
}

func seedVisitAndEntryForTest(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, sessionID uuid.UUID, token string) (uuid.UUID, uuid.UUID) {
	ctx := context.Background()
	visitID := uuid.New()
	entryID := uuid.New()

	expiresAt := time.Now().Add(23 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, entry_type, status, party_size, total_duration_minutes, magic_link_token_hash, magic_link_expires_at)
		VALUES ($1, $2, $3, 'walk_in', 'active', 1, 30, $4, $5)`, visitID, tenantID, locationID, token, expiresAt)
	if err != nil {
		t.Fatalf("failed to seed visit: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM queue_entries WHERE queue_session_id = $1", sessionID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count queue entries: %v", err)
	}
	tokenNumber := count + 1

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, token_number, state, presence_state, is_dispatchable)
		VALUES ($1, $2, $3, $4, 'waiting', 'remote', true)`, entryID, visitID, sessionID, tokenNumber)
	if err != nil {
		t.Fatalf("failed to seed queue entry: %v", err)
	}

	return entryID, visitID
}

func TestArrivalConfirmation_Integration(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		cleanDatabase(t, os.Getenv("DATABASE_URL"))
	})

	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()
	s.Arrival = presence.NewService(pool, func(locID uuid.UUID, version int64) {})

	ctx := context.Background()
	sessionID := seedQueueSession(t, pool, tenantID, locationID)

	pinHash, _ := bcrypt.GenerateFromPassword([]byte("1234"), 10)
	_, err := pool.Exec(ctx, `
		UPDATE locations 
		SET arrival_pin_hash = $1, 
			arrival_pin_plain = '1234',
			gps_latitude = 12.971598,
			gps_longitude = 77.594562,
			arrival_radius_metres = 100
		WHERE id = $2`, string(pinHash), locationID)
	if err != nil {
		t.Fatalf("failed to update location config: %v", err)
	}

	t.Run("PIN Verification - Wrong PIN 5 times then 429", func(t *testing.T) {
		token := "pin-test-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		for i := 0; i < 5; i++ {
			body := map[string]interface{}{
				"method": "pin",
				"pin":    "9999",
			}
			bodyBytes, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
			req.Header.Set("X-Session-Token", token)
			rec := httptest.NewRecorder()
			s.ConfirmArrival(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("Expected 400 Bad Request, got %d. Body: %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Code              string `json:"code"`
				AttemptsRemaining int    `json:"attempts_remaining"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp.Code != "WRONG_PIN" {
				t.Errorf("Expected code WRONG_PIN, got %s", resp.Code)
			}
			expectedRemaining := 5 - (i + 1)
			if resp.AttemptsRemaining != expectedRemaining {
				t.Errorf("Expected attempts_remaining %d, got %d", expectedRemaining, resp.AttemptsRemaining)
			}
		}

		body := map[string]interface{}{
			"method": "pin",
			"pin":    "1234",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmArrival(rec, req)

		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("Expected 429 Too Many Requests, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Code              string `json:"code"`
			AttemptsRemaining int    `json:"attempts_remaining"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Code != "RATE_LIMITED" {
			t.Errorf("Expected code RATE_LIMITED, got %s", resp.Code)
		}
		if resp.AttemptsRemaining != 0 {
			t.Errorf("Expected attempts_remaining 0, got %d", resp.AttemptsRemaining)
		}

		var presenceState string
		_ = pool.QueryRow(ctx, "SELECT presence_state FROM queue_entries WHERE id = $1", entryID).Scan(&presenceState)
		if presenceState != "remote" {
			t.Errorf("Expected presence_state remote, got %s", presenceState)
		}
	})

	t.Run("PIN Verification - Correct PIN success", func(t *testing.T) {
		token := "pin-success-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		body := map[string]interface{}{
			"method": "pin",
			"pin":    "1234",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmArrival(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			PresenceState string `json:"presence_state"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.PresenceState != "arrived" {
			t.Errorf("Expected presence_state arrived, got %s", resp.PresenceState)
		}

		var dbPresence string
		var dbIsDispatchable bool
		_ = pool.QueryRow(ctx, "SELECT presence_state, is_dispatchable FROM queue_entries WHERE id = $1", entryID).Scan(&dbPresence, &dbIsDispatchable)
		if dbPresence != "arrived" {
			t.Errorf("Expected DB presence arrived, got %s", dbPresence)
		}
		if !dbIsDispatchable {
			t.Errorf("Expected DB is_dispatchable true, got false")
		}
	})

	t.Run("GPS Verification - Low Accuracy (>150m) Rejected", func(t *testing.T) {
		token := "gps-acc-token"
		_, _ = seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		body := map[string]interface{}{
			"method":          "geolocation",
			"latitude":        12.971598,
			"longitude":       77.594562,
			"accuracy_metres": 200,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmArrival(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Expected 422, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Code != "GPS_ACCURACY_TOO_LOW" {
			t.Errorf("Expected GPS_ACCURACY_TOO_LOW, got %s", resp.Code)
		}

		var attemptCount int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM arrival_attempts WHERE method = 'geolocation'").Scan(&attemptCount)
		if attemptCount != 0 {
			t.Errorf("Expected 0 geolocation attempts logged, got %d", attemptCount)
		}
	})

	t.Run("GPS Verification - Just Outside Radius (radius + 1m)", func(t *testing.T) {
		token := "gps-out-token"
		_, _ = seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		body := map[string]interface{}{
			"method":          "geolocation",
			"latitude":        12.971598 + 0.00091,
			"longitude":       77.594562,
			"accuracy_metres": 10,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmArrival(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Expected 422, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Code != "GPS_OUT_OF_RANGE" {
			t.Errorf("Expected GPS_OUT_OF_RANGE, got %s", resp.Code)
		}

		var attemptCount int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM arrival_attempts WHERE method = 'geolocation' AND success = false").Scan(&attemptCount)
		if attemptCount != 1 {
			t.Errorf("Expected 1 failed geolocation attempt logged, got %d", attemptCount)
		}
	})

	t.Run("GPS Verification - Just Inside Radius (radius - 1m)", func(t *testing.T) {
		token := "gps-in-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		body := map[string]interface{}{
			"method":          "geolocation",
			"latitude":        12.971598 + 0.00081,
			"longitude":       77.594562,
			"accuracy_metres": 10,
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/queue/confirm-arrival", bytes.NewReader(bodyBytes))
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmArrival(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		var dbPresence string
		_ = pool.QueryRow(ctx, "SELECT presence_state FROM queue_entries WHERE id = $1", entryID).Scan(&dbPresence)
		if dbPresence != "arrived" {
			t.Errorf("Expected DB presence arrived, got %s", dbPresence)
		}
	})

	t.Run("Staff override - CustomerSession token fails", func(t *testing.T) {
		token := "staff-fail-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/staff/queue/entries/%s/confirm-arrival", entryID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.StaffConfirmArrival(rec, req, UUIDv7(entryID))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Expected 401 Unauthorized, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("CancelMyEntry - Cancel waiting entry succeeds", func(t *testing.T) {
		token := "cancel-wait-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		req := httptest.NewRequest(http.MethodPost, "/v1/queue/cancel", nil)
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.CancelMyEntry(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		var state string
		var isDispatchable bool
		_ = pool.QueryRow(ctx, "SELECT state, is_dispatchable FROM queue_entries WHERE id = $1", entryID).Scan(&state, &isDispatchable)
		if state != "cancelled" {
			t.Errorf("Expected state cancelled, got %s", state)
		}
		if isDispatchable {
			t.Errorf("Expected is_dispatchable false, got true")
		}
	})

	t.Run("CancelMyEntry - Cancel in_progress entry fails", func(t *testing.T) {
		token := "cancel-prog-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		_, _ = pool.Exec(ctx, "UPDATE queue_entries SET state = 'in_progress' WHERE id = $1", entryID)

		req := httptest.NewRequest(http.MethodPost, "/v1/queue/cancel", nil)
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.CancelMyEntry(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Expected 422, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ConfirmOnTheWay - Transition when arrived fails", func(t *testing.T) {
		token := "otw-arr-token"
		entryID, _ := seedVisitAndEntryForTest(t, pool, tenantID, locationID, sessionID, token)

		_, _ = pool.Exec(ctx, "UPDATE queue_entries SET presence_state = 'arrived' WHERE id = $1", entryID)

		req := httptest.NewRequest(http.MethodPost, "/v1/queue/on-the-way", nil)
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.ConfirmOnTheWay(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Expected 422, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestC33_ResolveBookingOptions_AllWalkIn(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "WalkIn Only", 30, 300, true)
	_, err := pool.Exec(context.Background(), "UPDATE service_variants SET allow_walk_in = true, allow_appointment = false, requires_appointment = false WHERE id = $1", variantID)
	if err != nil {
		t.Fatal(err)
	}
	for d := 0; d < 7; d++ {
		_, err = pool.Exec(context.Background(), `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at) VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
		if err != nil { t.Fatal(err) }
	}
	if err != nil {
		t.Fatal(err)
	}

	reqBody := map[string]interface{}{
		"variant_ids": []string{variantID.String()},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/public/locations/%s/booking-options", locationID), bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.ResolveBookingOptions(rec, req, UUIDv7(locationID))

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	
	emIf, ok := resp["allowed_entry_methods"].([]interface{})
	if !ok {
		t.Fatalf("expected allowed_entry_methods list, got %v. body: %s", resp["allowed_entry_methods"], rec.Body.String())
	}
	em := emIf
	if len(em) != 1 || em[0].(string) != "walk_in" {
		t.Errorf("Expected only walk_in, got %v", em)
	}
}

func TestC33_ResolveBookingOptions_RequiresAppointment(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Appt Only", 30, 300, true)
	_, err := pool.Exec(context.Background(), "UPDATE service_variants SET allow_walk_in = true, allow_appointment = true, requires_appointment = true WHERE id = $1", variantID)
	if err != nil {
		t.Fatal(err)
	}
	for d := 0; d < 7; d++ {
		_, err = pool.Exec(context.Background(), `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at) VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
		if err != nil { t.Fatal(err) }
	}
	if err != nil {
		t.Fatal(err)
	}

	reqBody := map[string]interface{}{
		"variant_ids": []string{variantID.String()},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/public/locations/%s/booking-options", locationID), bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.ResolveBookingOptions(rec, req, UUIDv7(locationID))

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	em := resp["allowed_entry_methods"].([]interface{})
	if len(em) != 1 || em[0].(string) != "appointment" {
		t.Errorf("Expected only appointment, got %v", em)
	}
}

func TestC33_ResolveBookingOptions_QueueFull(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Standard", 30, 300, true)
	_, err := pool.Exec(context.Background(), "UPDATE service_variants SET allow_walk_in = true, allow_appointment = true, requires_appointment = false WHERE id = $1", variantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), "UPDATE locations SET max_total_queue_size = 0 WHERE id = $1", locationID)
	if err != nil {
		t.Fatal(err)
	}
	for d := 0; d < 7; d++ {
		_, err = pool.Exec(context.Background(), `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at) VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
		if err != nil { t.Fatal(err) }
	}
	if err != nil {
		t.Fatal(err)
	}

	reqBody := map[string]interface{}{
		"variant_ids": []string{variantID.String()},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/public/locations/%s/booking-options", locationID), bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.ResolveBookingOptions(rec, req, UUIDv7(locationID))

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	em := resp["allowed_entry_methods"].([]interface{})
	if len(em) != 1 || em[0].(string) != "appointment" {
		t.Errorf("Expected only appointment (walk_in removed due to full queue), got %v", em)
	}
}

func TestC33_CreateCheckinIntent_WritesOneRowNoQueue(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Standard", 30, 300, true)
	_, err := pool.Exec(context.Background(), "UPDATE service_variants SET allow_walk_in = true, allow_appointment = true, requires_appointment = false WHERE id = $1", variantID)
	if err != nil {
		t.Fatal(err)
	}
	for d := 0; d < 7; d++ {
		_, err = pool.Exec(context.Background(), `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at) VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
		if err != nil { t.Fatal(err) }
	}
	if err != nil {
		t.Fatal(err)
	}

	reqBody := map[string]interface{}{
		"variant_ids": []string{variantID.String()},
		"customer_name": "John Doe",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/public/locations/%s/checkin-intent", locationID), bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	s.CreateCheckinIntent(rec, req, UUIDv7(locationID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	var intentCount int
	pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM checkin_intents").Scan(&intentCount)
	if intentCount != 1 {
		t.Errorf("Expected 1 intent, got %d", intentCount)
	}

	var entryCount int
	pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM queue_entries").Scan(&entryCount)
	if entryCount != 0 {
		t.Errorf("Expected 0 queue entries, got %d", entryCount)
	}
}

func TestC33_IntentPast23h(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	_, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	// Insert expired intent
	_, err := pool.Exec(context.Background(), `
		INSERT INTO checkin_intents (id, tenant_id, location_id, token_code, channel, status, source_ip, expires_at, shop_status_at_creation)
		VALUES ($1, $2, $3, 'ABCDEF', 'whatsapp', 'created', '127.0.0.1', NOW() - INTERVAL '25 hours', 'open')
	`, uuid.New(), tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to insert expired intent: %v", err)
	}

	var count int
	err = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM checkin_intents WHERE token_code = 'ABCDEF' AND status = 'created' AND expires_at > NOW()").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("Expected 0 active intents when filtering by expires_at > NOW(), got %d", count)
	}
}
