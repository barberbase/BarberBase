package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/realtime"

	"github.com/google/uuid"
)

// Check-in exercised OVER HTTP through the production router (NewRouter), not
// via the repository — the route previously shipped unreachable (404) while
// repo-level tests stayed green. This test fails if the manual route is ever
// unwired again, and proves the staff-JWT guard on it.
func TestCheckInAppointment_OverHTTP(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	defer pool.Close()
	s.Manager = realtime.NewManager()
	ctx := context.Background()

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)
	for d := 0; d < 7; d++ {
		_, _ = pool.Exec(ctx, `INSERT INTO location_hours (id, tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, '00:00:00', '23:59:59')`, tenantID, locationID, d)
	}

	repo := queue.QueueRepository{Pool: pool}
	name := "HTTP Apt Customer"
	booked, err := repo.BookAppointment(ctx, tenantID, queue.BookAppointmentRequest{
		LocationID:       locationID,
		VariantIDs:       []uuid.UUID{variantID},
		PartySize:        1,
		ScheduledStartAt: time.Now().Add(30 * time.Minute),
		PhoneNumber:      "+919876543212",
		CustomerName:     &name,
		InitiatedVia:     "staff_dashboard",
		IdempotencyKey:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("BookAppointment: %v", err)
	}

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	defer srv.Close()
	url := fmt.Sprintf("%s/v1/staff/appointments/%s/checkin", srv.URL, booked.AppointmentID)

	post := func(token string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(nil))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST checkin: %v", err)
		}
		return res
	}

	// No JWT → 401 (the manual route must enforce staff auth, not pass through).
	res := post("")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated check-in: got %d, want 401", res.StatusCode)
	}
	res.Body.Close()

	jwt, _, err := auth.GenerateAccessAndRefreshTokens(
		[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "barber")
	if err != nil {
		t.Fatalf("mint JWT: %v", err)
	}

	// Authenticated → 200 with the grace-window appointment priority.
	res = post(jwt)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("check-in over HTTP: got %d, want 200", res.StatusCode)
	}
	var body struct {
		QueueEntryID  string `json:"queue_entry_id"`
		PriorityGroup int    `json:"priority_group"`
		TokenNumber   int    `json:"token_number"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	res.Body.Close()
	if body.PriorityGroup != 50 {
		t.Errorf("priority_group = %d, want 50 (on-time appointment grace priority)", body.PriorityGroup)
	}
	if body.QueueEntryID == "" || body.TokenNumber == 0 {
		t.Errorf("incomplete response: %+v", body)
	}

	// Second check-in → 409 (no longer 'scheduled').
	res = post(jwt)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("double check-in: got %d, want 409", res.StatusCode)
	}
	res.Body.Close()
}
