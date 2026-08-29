package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// API1 A3-A6. next_open_at over HTTP through the production router, because the
// value is computed by computeShopStatus (covered by the B3 parity table) but the
// thing under test here is that GetLocationStatus actually serialises it — the
// generated LocationStatus struct is not used by the hand-built response map, so
// nothing but this test holds the handler to the spec.
//
// The handler reads time.Now(); there is no injectable clock and adding one is out
// of scope. Every expectation is derived from the same wall clock the handler sees.
// Known ceiling: a run that straddles IST midnight between the expectation and the
// request will fail, same property as the existing night-window tests.
func TestNextOpenAtOverHTTP(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	defer srv.Close()

	get := func(t *testing.T) map[string]any {
		t.Helper()
		res, err := http.Get(srv.URL + "/v1/public/locations/test-location/status")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var body map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		return body
	}

	// A3: past today's closes_at. Today opens and closes at 00:00 (shut since
	// 00:00:01), tomorrow opens at 10:00 — so the answer must be tomorrow's
	// opening, in the location's offset.
	t.Run("A3 past closes_at reports the next open day", func(t *testing.T) {
		now := time.Now().In(istZone)
		tomorrow := now.AddDate(0, 0, 1)
		seedHours(t, pool, tenantID, locationID, map[int][2]string{
			int(now.Weekday()):      {"00:00:00", "00:00:00"},
			int(tomorrow.Weekday()): {"10:00:00", "20:00:00"},
		})

		body := get(t)
		require.Equal(t, "closed", body["shop_status"])
		want := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, istZone)
		require.Equal(t, want.Format(time.RFC3339), body["next_open_at"])
		assertUnchangedShape(t, body)
	})

	// A4: 24/7 shop (the star-salon shape). Open right now, and next_open_at is
	// the NEXT opening — tomorrow midnight — never the one already in progress.
	t.Run("A4 24/7 shop reports the next opening not the current one", func(t *testing.T) {
		now := time.Now().In(istZone)
		week := map[int][2]string{}
		for d := 0; d < 7; d++ {
			week[d] = [2]string{"00:00:00", "23:59:59"}
		}
		seedHours(t, pool, tenantID, locationID, week)

		body := get(t)
		require.Equal(t, "open", body["shop_status"])
		tomorrow := now.AddDate(0, 0, 1)
		want := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, istZone)
		require.Equal(t, want.Format(time.RFC3339), body["next_open_at"],
			"a 24/7 shop must report tomorrow's opening, not today's")
		assertUnchangedShape(t, body)
	})

	// A5: no open day inside the eight-day lookahead. The key must be PRESENT and
	// null — not omitted, not a zero time.
	t.Run("A5 no open day in eight days is null", func(t *testing.T) {
		seedHours(t, pool, tenantID, locationID, nil)

		body := get(t)
		require.Equal(t, "closed", body["shop_status"])
		v, present := body["next_open_at"]
		require.True(t, present, "A5: key must be present, not omitted")
		require.Nil(t, v, "A5: must be null, not a zero time")
		assertUnchangedShape(t, body)
	})
}

// seedHours replaces the week. Days absent from open are written is_open = false,
// so every case starts from a fully known week rather than leftovers.
func seedHours(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, open map[int][2]string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DELETE FROM location_hours WHERE location_id = $1`, locationID)
	require.NoError(t, err)
	for d := 0; d < 7; d++ {
		span, isOpen := open[d]
		var opens, closes *string
		if isOpen {
			o, c := span[0], span[1]
			opens, closes = &o, &c
		}
		_, err := pool.Exec(ctx, `INSERT INTO location_hours
			(tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
			VALUES ($1, $2, $3, $4, $5::time, $6::time)`,
			tenantID, locationID, d, isOpen, opens, closes)
		require.NoError(t, err)
	}
}

// assertUnchangedShape is A6: an existing client that knows nothing about
// next_open_at must be unaffected. The response carries exactly the keys it
// carried before, plus the new one, and nothing else moved.
func assertUnchangedShape(t *testing.T, body map[string]any) {
	t.Helper()
	got := make([]string, 0, len(body))
	for k := range body {
		got = append(got, k)
	}
	sort.Strings(got)
	// The full key set for a pooled location with no active override: pre-API1,
	// plus next_open_at (API1), plus the three landing fields (P1). barbers is
	// absent under pooled; temporary_closure_ends_at only appears while
	// temporarily_closed.
	//
	// This list is the contract guard for the whole response, so a unit that
	// adds a key must come here and say so deliberately.
	require.Equal(t, []string{
		"appointments_enabled", "audiences", "available_modes",
		"business_hours_today", "estimated_wait_minutes", "id", "name",
		"next_open_at", "queue_length", "queue_open", "queue_routing_mode",
		"shop_status", "slug", "tenant_location_count", "tenant_name",
	}, got)
	require.Equal(t, "test-location", body["slug"])
	require.Equal(t, "pooled", body["queue_routing_mode"])
	require.Equal(t, false, body["queue_open"])
	require.Equal(t, float64(0), body["queue_length"])
	require.Equal(t, float64(0), body["estimated_wait_minutes"])
	require.NotNil(t, body["business_hours_today"])
}
