package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/realtime"

	"github.com/google/uuid"
)

// /q/appointment page contract over HTTP: magic-link token from booking round-
// trips GET /v1/appointments/my and POST /v1/appointments/my/cancel, with the
// reminder outbox swept on cancel and 409 on a second cancel.
func TestMyAppointment_OverHTTP(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()
	s.Manager = realtime.NewManager()
	ctx := context.Background()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)
	for d := 0; d < 7; d++ {
		_, _ = pool.Exec(ctx, `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
	}

	repo := queue.QueueRepository{Pool: pool}
	name := "Page Apt Customer"
	booked, err := repo.BookAppointment(ctx, tenantID, queue.BookAppointmentRequest{
		LocationID:       locationID,
		VariantIDs:       []uuid.UUID{variantID},
		PartySize:        1,
		ScheduledStartAt: time.Now().Add(48 * time.Hour), // far enough out to create a reminder
		PhoneNumber:      "+919876543213",
		CustomerName:     &name,
		InitiatedVia:     "staff_dashboard",
		IdempotencyKey:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("BookAppointment: %v", err)
	}
	token := strings.TrimPrefix(booked.MagicLink, "https://barberbase.in/q/appointment?t=")
	if token == booked.MagicLink || token == "" {
		t.Fatalf("unexpected magic link: %q", booked.MagicLink)
	}

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	defer srv.Close()

	do := func(method, path, tok string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if tok != "" {
			req.Header.Set("X-Session-Token", tok)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return res
	}

	// Missing / garbage token → 401.
	for _, tok := range []string{"", "garbage.token"} {
		res := do(http.MethodGet, "/v1/appointments/my", tok)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: got %d, want 401", tok, res.StatusCode)
		}
		res.Body.Close()
	}

	res := do(http.MethodGet, "/v1/appointments/my", token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET my appointment: got %d, want 200", res.StatusCode)
	}
	var apt struct {
		Status      string `json:"status"`
		ShopName    string `json:"shop_name"`
		Cancellable bool   `json:"cancellable"`
		Services    []struct {
			Name       string `json:"name"`
			PricePaise int    `json:"price_paise"`
		} `json:"services"`
	}
	if err := json.NewDecoder(res.Body).Decode(&apt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	res.Body.Close()
	if apt.Status != "scheduled" || !apt.Cancellable || apt.ShopName != "Test Tenant" ||
		len(apt.Services) != 1 || apt.Services[0].Name != "Haircut" || apt.Services[0].PricePaise != 30000 {
		t.Errorf("unexpected appointment payload: %+v", apt)
	}

	// Cancel → 200; reminder swept; second cancel → 409.
	res = do(http.MethodPost, "/v1/appointments/my/cancel", token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cancel: got %d, want 200", res.StatusCode)
	}
	res.Body.Close()

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM appointments WHERE id=$1`, booked.AppointmentID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("appointment status = %s (err %v), want cancelled", status, err)
	}
	var reminders int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events
		WHERE status='pending' AND payload->>'template_code'='bb_appointment_reminder'
		  AND payload->>'source_id'=$1`, booked.AppointmentID.String()).Scan(&reminders)
	if reminders != 0 {
		t.Errorf("pending reminders after cancel = %d, want 0", reminders)
	}

	res = do(http.MethodPost, "/v1/appointments/my/cancel", token)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second cancel: got %d, want 409", res.StatusCode)
	}
	res.Body.Close()
}
