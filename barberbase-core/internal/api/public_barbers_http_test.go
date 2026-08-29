package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// P2 A1-A11. Both parts over the production router, always without auth.

type barberFix struct {
	s          *Server
	pool       *pgxpool.Pool
	srv        *httptest.Server
	tenantID   uuid.UUID
	locationID uuid.UUID
}

func newBarberFix(t *testing.T) barberFix {
	t.Helper()
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	t.Cleanup(pool.Close)
	// setupTestServer's own staff member defaults to offline; park it there so
	// each test states its roster explicitly.
	_, err := pool.Exec(context.Background(),
		`UPDATE staff_members SET status = 'offline' WHERE id = $1`, staffID)
	require.NoError(t, err)

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	t.Cleanup(srv.Close)
	return barberFix{s, pool, srv, tenantID, locationID}
}

func (f barberFix) seedBarber(t *testing.T, name, status string, tierID *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(), `INSERT INTO staff_members
		(tenant_id, location_id, name, phone_number, role, status, is_active, tier_id)
		VALUES ($1, $2, $3, $4, 'barber', $5, true, $6) RETURNING id`,
		f.tenantID, f.locationID, name, "+9194"+uuid.NewString()[:9], status, tierID).Scan(&id))
	return id
}

func (f barberFix) seedTier(t *testing.T, name string, rank int, isDefault bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(), `INSERT INTO staff_tiers
		(tenant_id, location_id, name, rank, is_default) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		f.tenantID, f.locationID, name, rank, isDefault).Scan(&id))
	return id
}

func (f barberFix) setTierPrice(t *testing.T, tierID, variantID uuid.UUID, pricePaise int) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `INSERT INTO service_variant_tier_prices
		(tenant_id, location_id, service_variant_id, tier_id, price_paise)
		VALUES ($1, $2, $3, $4, $5)`, f.tenantID, f.locationID, variantID, tierID, pricePaise)
	require.NoError(t, err)
}

// getBarbers calls the public route with NO auth of any kind (A4).
func (f barberFix) getBarbers(t *testing.T, variantIDs ...uuid.UUID) []PublicBarber {
	t.Helper()
	url := f.srv.URL + "/v1/public/locations/test-location/barbers"
	if len(variantIDs) > 0 {
		ids := make([]string, 0, len(variantIDs))
		for _, v := range variantIDs {
			ids = append(ids, v.String())
		}
		url += "?variant_ids=" + strings.Join(ids, ",")
	}
	res, err := http.Get(url)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "A4: public, no auth")
	var out []PublicBarber
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

// A1 + A2 + A3: the mask, on both public surfaces.
func TestPublicBarbers_StatusIsMasked(t *testing.T) {
	f := newBarberFix(t)
	_, err := f.pool.Exec(context.Background(),
		`UPDATE locations SET queue_routing_mode = 'barber_specific' WHERE id = $1`, f.locationID)
	require.NoError(t, err)

	f.seedBarber(t, "Asha Idle", "idle", nil)
	f.seedBarber(t, "Bilal Cutting", "cutting", nil)
	f.seedBarber(t, "Chandra Break", "break", nil)
	f.seedBarber(t, "Dev Offline", "offline", nil)

	// Surface 1: /status's barbers array — the one that shipped raw.
	t.Run("A1 /status masks the internal status", func(t *testing.T) {
		res, err := http.Get(f.srv.URL + "/v1/public/locations/test-location/status")
		require.NoError(t, err)
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		body := string(raw)

		require.NotContains(t, body, `"break"`, "A1: 'break' must never reach a customer")
		require.NotContains(t, body, `"idle"`, "A1: raw internal status must not ship")
		require.NotContains(t, body, `"cutting"`)
		require.NotContains(t, body, "Dev Offline", "A2: an offline barber is omitted entirely")
		require.Contains(t, body, `"available"`)
		require.Contains(t, body, `"busy"`)
	})

	// Surface 2: the new route.
	t.Run("A2/A3 the barbers route masks and omits", func(t *testing.T) {
		got := f.getBarbers(t)
		require.Len(t, got, 3, "A2: the offline barber must be absent")

		byName := map[string]PublicBarber{}
		for _, b := range got {
			byName[b.DisplayName] = b
			require.NotEqual(t, "Dev Offline", b.DisplayName)
		}
		require.Equal(t, PublicBarberAvailability("available"), byName["Asha Idle"].Availability)
		require.Equal(t, PublicBarberAvailability("busy"), byName["Bilal Cutting"].Availability)
		require.Equal(t, PublicBarberAvailability("busy"), byName["Chandra Break"].Availability,
			"A3: on break reads as busy")
		require.Equal(t, byName["Bilal Cutting"].Availability, byName["Chandra Break"].Availability,
			"A3: a customer cannot tell cutting from break")
	})
}

// A5 + A6 + A8 + A9 + A10: the payload.
func TestPublicBarbers_PriceAndWait(t *testing.T) {
	f := newBarberFix(t)
	variantID := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)

	junior := f.seedTier(t, "Junior", 10, false)
	standard := f.seedTier(t, "Standard", 20, true) // the default
	senior := f.seedTier(t, "Senior", 30, false)
	f.setTierPrice(t, junior, variantID, 25000)
	f.setTierPrice(t, senior, variantID, 50000)
	// Standard deliberately has no override: it inherits base 35000.

	f.seedBarber(t, "Junior Jo", "idle", &junior)
	f.seedBarber(t, "Standard Sam", "idle", &standard)
	f.seedBarber(t, "Senior Sri", "idle", &senior)

	t.Run("A6 without variant_ids price and wait are omitted", func(t *testing.T) {
		for _, b := range f.getBarbers(t) {
			require.Nil(t, b.PricePaise, "A6: omitted, not zero")
			require.Nil(t, b.PriceDeltaPaise)
			require.Nil(t, b.EstWaitMinutes)
			require.NotEmpty(t, b.DisplayName, "A6: the list still returns")
			require.NotNil(t, b.Tier)
		}
	})

	t.Run("A5 with variant_ids, delta is against the DEFAULT tier", func(t *testing.T) {
		byName := map[string]PublicBarber{}
		for _, b := range f.getBarbers(t, variantID) {
			byName[b.DisplayName] = b
		}
		require.Equal(t, 25000, *byName["Junior Jo"].PricePaise)
		require.Equal(t, 35000, *byName["Standard Sam"].PricePaise, "inherits base")
		require.Equal(t, 50000, *byName["Senior Sri"].PricePaise)

		require.Equal(t, -10000, *byName["Junior Jo"].PriceDeltaPaise,
			"A5: negative — cheaper than the default is the case this screen exists for")
		require.Equal(t, 0, *byName["Standard Sam"].PriceDeltaPaise)
		require.Equal(t, 15000, *byName["Senior Sri"].PriceDeltaPaise, "A5: positive")

		for _, b := range byName {
			require.NotNil(t, b.EstWaitMinutes)
			require.GreaterOrEqual(t, *b.EstWaitMinutes, 0)
		}
	})

	// A8: the partial unique index permits zero defaults, so this is reachable.
	t.Run("A8 no default tier omits the delta but keeps the price", func(t *testing.T) {
		_, err := f.pool.Exec(context.Background(),
			`UPDATE staff_tiers SET is_default = false WHERE location_id = $1`, f.locationID)
		require.NoError(t, err)

		got := f.getBarbers(t, variantID)
		require.NotEmpty(t, got)
		for _, b := range got {
			require.NotNil(t, b.PricePaise, "A8: price still ships")
			require.Nil(t, b.PriceDeltaPaise, "A8: no anchor, so no delta")
		}
	})

	// A9 + A10: what must not be there.
	t.Run("A9 avatar_url is null with no avatar and media unconfigured", func(t *testing.T) {
		for _, b := range f.getBarbers(t, variantID) {
			require.Nil(t, b.AvatarUrl, "A9: null, never a placeholder")
		}
	})

	t.Run("A10 no invented fields anywhere in the response", func(t *testing.T) {
		// A8 above cleared the default tier; restore it so the full key set —
		// price_delta_paise included — is what gets asserted here.
		_, err := f.pool.Exec(context.Background(),
			`UPDATE staff_tiers SET is_default = true WHERE id = $1`, standard)
		require.NoError(t, err)

		res, err := http.Get(f.srv.URL + "/v1/public/locations/test-location/barbers?variant_ids=" + variantID.String())
		require.NoError(t, err)
		defer res.Body.Close()
		var raw []map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
		require.NotEmpty(t, raw)
		for _, b := range raw {
			for _, forbidden := range []string{
				"years_experience", "specialities", "specialties", "rating",
				"review_count", "score", "phone_number", "status", "role", "revenue",
			} {
				require.NotContains(t, b, forbidden, "A10: %q must not be on a public surface", forbidden)
			}
			require.Equal(t, []string{
				"availability", "avatar_url", "display_name", "est_wait_minutes",
				"price_delta_paise", "price_paise", "staff_member_id", "tier",
			}, sortedKeys(b))
		}
	})
}

// A7 over HTTP: a tier of one reads materially longer than a pooled barber.
func TestPublicBarbers_WaitIsTierScoped(t *testing.T) {
	f := newBarberFix(t)
	variantID := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)
	senior := f.seedTier(t, "Senior", 30, false)
	f.seedBarber(t, "Sole Senior", "idle", &senior)
	for _, n := range []string{"Pool A", "Pool B", "Pool C"} {
		f.seedBarber(t, n, "idle", nil)
	}

	ctx := context.Background()
	sessionID := seedQueueSession(t, f.pool, f.tenantID, f.locationID)
	// Two hours of senior-only work, two hours unconstrained.
	for i, tier := range []*uuid.UUID{&senior, &senior, nil, nil} {
		var visitID uuid.UUID
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO visits
			(tenant_id, location_id, entry_type, status, party_size, total_duration_minutes)
			VALUES ($1, $2, 'walk_in', 'active', 1, 60) RETURNING id`,
			f.tenantID, f.locationID).Scan(&visitID))
		_, err := f.pool.Exec(ctx, `INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state,
			 is_dispatchable, priority_group, sort_key, required_tier_id)
			VALUES ($1, $2, $3, 'waiting', 'arrived', true, 100, $4, $5)`,
			visitID, sessionID, i+1, int64(i+1), tier)
		require.NoError(t, err)
	}

	byName := map[string]PublicBarber{}
	for _, b := range f.getBarbers(t, variantID) {
		byName[b.DisplayName] = b
	}
	sole := *byName["Sole Senior"].EstWaitMinutes
	pooled := *byName["Pool A"].EstWaitMinutes
	require.Equal(t, 240, sole, "120 senior-only + 120 unconstrained, all on one barber")
	require.Equal(t, 40, pooled, "120 unconstrained split three ways")
	require.Greater(t, sole, pooled*2,
		"A7: the same queue depth must read materially longer for a tier of one")
}

// A11: tenant isolation on a route with no auth to lean on.
func TestPublicBarbers_TenantIsolation(t *testing.T) {
	f := newBarberFix(t)
	f.seedBarber(t, "Mine", "idle", nil)

	ctx := context.Background()
	otherTenant, otherLocation := uuid.New(), uuid.New()
	_, err := f.pool.Exec(ctx, `INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Barber Other', 'barber-other', '+919000000005')`, otherTenant)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO locations (id, tenant_id, name, slug)
		VALUES ($1, $2, 'Other Loc', 'other-barber-loc')`, otherLocation, otherTenant)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO staff_members
		(tenant_id, location_id, name, phone_number, role, status, is_active)
		VALUES ($1, $2, 'Theirs', '+919000000006', 'barber', 'idle', true)`, otherTenant, otherLocation)
	require.NoError(t, err)

	for _, b := range f.getBarbers(t) {
		require.NotEqual(t, "Theirs", b.DisplayName, "A11: another tenant's barber must never appear")
	}

	res, err := http.Get(f.srv.URL + "/v1/public/locations/no-such-shop/barbers")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
