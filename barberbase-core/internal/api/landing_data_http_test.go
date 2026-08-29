package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// P1 A2-A9. available_modes, audiences and appointments_enabled on the public
// status route — the data the landing page needs on first paint.
//
// Public endpoint: every request here is made with NO auth of any kind, which is
// itself A9.

func landingServer(t *testing.T) (*Server, *pgxpool.Pool, uuid.UUID, uuid.UUID, *httptest.Server) {
	t.Helper()
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	t.Cleanup(pool.Close)
	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	t.Cleanup(srv.Close)
	return s, pool, tenantID, locationID, srv
}

// landingStatus GETs the public status page with no credentials whatsoever.
func landingStatus(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	res, err := http.Get(srv.URL + "/v1/public/locations/test-location/status")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "A9: the public route needs no auth")
	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	return body
}

func strings_(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	require.True(t, ok, "expected an array, got %T (%v)", v, v)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		require.True(t, ok)
		out = append(out, s)
	}
	return out
}

// setVariantModes rewrites the booking flags on every variant at the location.
func setVariantModes(t *testing.T, pool *pgxpool.Pool, locationID uuid.UUID, walkIn, appointment bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE service_variants SET allow_walk_in = $2, allow_appointment = $3 WHERE location_id = $1`,
		locationID, walkIn, appointment)
	require.NoError(t, err)
}

func seedCategory(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, name, gender string, active bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO service_categories
		(tenant_id, location_id, name, gender, is_active) VALUES ($1, $2, $3, $4, $5)`,
		tenantID, locationID, name, gender, active)
	require.NoError(t, err)
}

// A2 + A3 + A4: available_modes tracks what the ACTIVE variants allow.
func TestLandingData_AvailableModes(t *testing.T) {
	_, pool, tenantID, locationID, srv := landingServer(t)

	// A4 first: nothing configured yet. The key must be present and empty —
	// "no bookable service" and "this server does not send the field" are
	// different answers to a client deciding whether to render a CTA at all.
	t.Run("A4 zero active variants is an empty array, not null", func(t *testing.T) {
		body := landingStatus(t, srv)
		v, present := body["available_modes"]
		require.True(t, present, "A4: key must be present")
		require.NotNil(t, v, "A4: must be [] not null")
		require.Empty(t, strings_(t, v))
	})

	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Fade", 30, 35000, true)

	t.Run("A2 walk-in only", func(t *testing.T) {
		setVariantModes(t, pool, locationID, true, false)
		require.Equal(t, []string{"walk_in"}, strings_(t, landingStatus(t, srv)["available_modes"]))
	})

	t.Run("A3 both, in a stable order", func(t *testing.T) {
		setVariantModes(t, pool, locationID, true, true)
		for i := 0; i < 3; i++ {
			require.Equal(t, []string{"walk_in", "appointment"},
				strings_(t, landingStatus(t, srv)["available_modes"]),
				"A3: order must not vary between requests (call %d)", i)
		}
	})

	t.Run("appointment only", func(t *testing.T) {
		setVariantModes(t, pool, locationID, false, true)
		require.Equal(t, []string{"appointment"}, strings_(t, landingStatus(t, srv)["available_modes"]))
	})

	// Derived from ACTIVE variants: a retired service must not keep a mode alive.
	t.Run("an inactive variant does not contribute a mode", func(t *testing.T) {
		setVariantModes(t, pool, locationID, true, true)
		_, err := pool.Exec(context.Background(),
			`UPDATE service_variants SET is_active = false WHERE id = $1`, variantID)
		require.NoError(t, err)
		require.Empty(t, strings_(t, landingStatus(t, srv)["available_modes"]))
	})
}

// A5 + A6: audiences is the distinct gender set over ACTIVE categories.
func TestLandingData_Audiences(t *testing.T) {
	_, pool, tenantID, locationID, srv := landingServer(t)

	t.Run("no categories is an empty array", func(t *testing.T) {
		v, present := landingStatus(t, srv)["audiences"]
		require.True(t, present)
		require.NotNil(t, v)
		require.Empty(t, strings_(t, v))
	})

	// A6: one audience — the client hides the chip row entirely, so a men-only
	// shop does not cost every customer a tap.
	t.Run("A6 single-audience shop returns exactly one element", func(t *testing.T) {
		seedCategory(t, pool, tenantID, locationID, "Hair", "men", true)
		seedCategory(t, pool, tenantID, locationID, "Beard", "men", true)
		require.Equal(t, []string{"men"}, strings_(t, landingStatus(t, srv)["audiences"]),
			"A6: distinct, so two men-only categories are still one element")
	})

	t.Run("A5 an inactive category's gender does not appear", func(t *testing.T) {
		seedCategory(t, pool, tenantID, locationID, "Bridal", "women", false)
		require.Equal(t, []string{"men"}, strings_(t, landingStatus(t, srv)["audiences"]),
			"A5: only active categories count")
	})

	t.Run("multiple audiences come back distinct and ordered", func(t *testing.T) {
		seedCategory(t, pool, tenantID, locationID, "Colour", "women", true)
		seedCategory(t, pool, tenantID, locationID, "Skin", "unisex", true)
		got := strings_(t, landingStatus(t, srv)["audiences"])
		require.Equal(t, []string{"men", "unisex", "women"}, got,
			"alphabetical, so the chip row does not reshuffle between requests")
	})
}

// A7: the dark-out. available_modes may say appointment while the customer path
// to book one is closed — that disagreement is the reason the flag exists.
func TestLandingData_AppointmentsDarkOut(t *testing.T) {
	_, pool, tenantID, locationID, srv := landingServer(t)
	seedServiceVariant(t, pool, tenantID, locationID, "Fade", 30, 35000, true)
	setVariantModes(t, pool, locationID, true, true)

	body := landingStatus(t, srv)
	require.Contains(t, strings_(t, body["available_modes"]), "appointment",
		"the catalogue does permit appointments")
	require.Equal(t, false, body["appointments_enabled"],
		"A7: but the customer booking endpoint is gated to staff")

	// And that is not a lie about the endpoint: prove it still 401s a customer.
	res, err := http.Post(srv.URL+"/v1/appointments/book", "application/json", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"A7: appointments_enabled=false must match reality, not just be a constant")
}

// A8: nothing that was already on the wire moved.
func TestLandingData_ExistingShapeUnchanged(t *testing.T) {
	_, pool, tenantID, locationID, srv := landingServer(t)
	seedServiceVariant(t, pool, tenantID, locationID, "Fade", 30, 35000, true)
	seedCategory(t, pool, tenantID, locationID, "Hair", "unisex", true)

	body := landingStatus(t, srv)
	for _, k := range []string{
		"id", "name", "slug", "tenant_name", "tenant_location_count", "shop_status",
		"queue_open", "queue_length", "estimated_wait_minutes", "queue_routing_mode",
		"next_open_at", "business_hours_today",
	} {
		require.Contains(t, body, k, "A8: pre-P1 key %q must survive", k)
	}
	require.Equal(t, "test-location", body["slug"])
	require.Equal(t, "pooled", body["queue_routing_mode"])
	require.Equal(t, float64(0), body["queue_length"])
	require.NotNil(t, body["business_hours_today"])

	// The exhaustive key-set assertion lives in next_open_at_http_test.go's
	// assertUnchangedShape, which this unit updated to the new baseline.
	require.Len(t, body, 15, "A8: exactly the pre-P1 keys plus the three P1 fields")
}
