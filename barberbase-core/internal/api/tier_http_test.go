package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// O4 A2-A11 and A15-A19. The nine tier routes over the production router.

type tierFix struct {
	s          *Server
	pool       *pgxpool.Pool
	srv        *httptest.Server
	tenantID   uuid.UUID
	locationID uuid.UUID
	staffID    uuid.UUID
	jwt        string
}

func newTierFix(t *testing.T) tierFix {
	t.Helper()
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	t.Cleanup(pool.Close)
	s.Tiers = &repository.TierRepository{Pool: pool}

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	t.Cleanup(srv.Close)
	jwt, _, err := auth.GenerateAccessAndRefreshTokens(
		[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "owner")
	require.NoError(t, err)
	return tierFix{s, pool, srv, tenantID, locationID, staffID, jwt}
}

func (f tierFix) req(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	return do(t, method, f.srv.URL+path, f.jwt, body)
}

// createTier posts a tier and returns the decoded generated struct (A2).
func (f tierFix) createTier(t *testing.T, name string, rank int) Tier {
	t.Helper()
	res := f.req(t, http.MethodPost, "/v1/admin/tiers", map[string]any{"name": name, "rank": rank})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var out Tier
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func (f tierFix) matrix(t *testing.T, tierID uuid.UUID) []ResolvedTierPrice {
	t.Helper()
	res := f.req(t, http.MethodGet, "/v1/admin/tiers/"+tierID.String()+"/prices", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out []ResolvedTierPrice
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func (f tierFix) overrideCount(t *testing.T, tierID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM service_variant_tier_prices WHERE tier_id = $1`, tierID).Scan(&n))
	return n
}

// A2 + A3: CRUD over the wire, and every error the table can produce here.
func TestTierRoutes_CRUDAndErrors(t *testing.T) {
	f := newTierFix(t)

	senior := f.createTier(t, "Senior", 20)
	require.Equal(t, "Senior", senior.Name)
	require.Equal(t, 20, senior.Rank)
	require.False(t, senior.IsDefault)
	require.True(t, senior.IsActive)

	t.Run("duplicate name is 409", func(t *testing.T) {
		res := f.req(t, http.MethodPost, "/v1/admin/tiers", map[string]any{"name": "Senior", "rank": 99})
		require.Equal(t, http.StatusConflict, res.StatusCode)
		require.Equal(t, "DUPLICATE_NAME", decodeCode(t, res))
	})

	t.Run("duplicate rank is 409", func(t *testing.T) {
		res := f.req(t, http.MethodPost, "/v1/admin/tiers", map[string]any{"name": "Master", "rank": 20})
		require.Equal(t, http.StatusConflict, res.StatusCode)
		require.Equal(t, "DUPLICATE_RANK", decodeCode(t, res))
	})

	t.Run("patch renames, omitted fields untouched", func(t *testing.T) {
		res := f.req(t, http.MethodPatch, "/v1/admin/tiers/"+senior.Id.String(),
			map[string]any{"name": "Senior Stylist"})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var out Tier
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		require.Equal(t, "Senior Stylist", out.Name)
		require.Equal(t, 20, out.Rank, "rank must survive a name-only patch")
	})

	t.Run("patch of a missing tier is 404", func(t *testing.T) {
		res := f.req(t, http.MethodPatch, "/v1/admin/tiers/"+uuid.NewString(), map[string]any{"name": "Ghost"})
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("include_inactive controls what list returns", func(t *testing.T) {
		junior := f.createTier(t, "Junior", 10)
		require.Equal(t, http.StatusNoContent,
			f.req(t, http.MethodDelete, "/v1/admin/tiers/"+junior.Id.String(), nil).StatusCode)

		res := f.req(t, http.MethodGet, "/v1/admin/tiers", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var active []Tier
		require.NoError(t, json.NewDecoder(res.Body).Decode(&active))
		for _, tr := range active {
			require.NotEqual(t, junior.Id, tr.Id, "retired tier must be hidden by default")
		}

		res = f.req(t, http.MethodGet, "/v1/admin/tiers?include_inactive=true", nil)
		var all []Tier
		require.NoError(t, json.NewDecoder(res.Body).Decode(&all))
		found := false
		for _, tr := range all {
			if tr.Id == junior.Id {
				found = true
				require.False(t, tr.IsActive, "soft delete, not removal")
			}
		}
		require.True(t, found, "include_inactive must surface it")

		// The row is still there — a hard delete would break queue history.
		var n int
		require.NoError(t, f.pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM staff_tiers WHERE id = $1`, junior.Id).Scan(&n))
		require.Equal(t, 1, n)
	})

	t.Run("negative price is 400", func(t *testing.T) {
		variantID := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)
		res := f.req(t, http.MethodPut,
			fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", senior.Id, variantID),
			map[string]any{"price_paise": -1})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, "NEGATIVE_PRICE", decodeCode(t, res))
	})

	t.Run("bulk needs exactly one of delta_paise and percent", func(t *testing.T) {
		for _, body := range []map[string]any{{}, {"delta_paise": 100, "percent": 10}} {
			res := f.req(t, http.MethodPost, "/v1/admin/tiers/"+senior.Id.String()+"/prices/bulk", body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
		}
	})
}

// A4: the 409 has to name who is blocking the delete.
func TestTierRoutes_DeactivateNamesBlockingBarbers(t *testing.T) {
	f := newTierFix(t)
	tier := f.createTier(t, "Senior", 20)
	ctx := context.Background()

	for _, name := range []string{"Asha", "Bilal"} {
		_, err := f.pool.Exec(ctx, `INSERT INTO staff_members
			(tenant_id, location_id, name, phone_number, role, is_active, tier_id)
			VALUES ($1, $2, $3, $4, 'barber', true, $5)`,
			f.tenantID, f.locationID, name, "+9196"+uuid.NewString()[:9], tier.Id)
		require.NoError(t, err)
	}

	res := f.req(t, http.MethodDelete, "/v1/admin/tiers/"+tier.Id.String(), nil)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	var body TierInUseError
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "TIER_IN_USE", body.Code)
	require.ElementsMatch(t, []string{"Asha", "Bilal"}, body.BlockingBarbers,
		"A4: an owner needs to know who to reassign")

	// Still active — a refused delete must not half-apply.
	var isActive bool
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT is_active FROM staff_tiers WHERE id = $1`, tier.Id).Scan(&isActive))
	require.True(t, isActive)
}

// A5: Law 11 on all nine, including the two-ID routes.
func TestTierRoutes_TenantIsolation(t *testing.T) {
	f := newTierFix(t)
	ctx := context.Background()
	mine := f.createTier(t, "Senior", 20)
	myVariant := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)

	otherTenant, otherLocation := uuid.New(), uuid.New()
	_, err := f.pool.Exec(ctx, `INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Other', 'other-tier-tenant', '+919000000003')`, otherTenant)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO locations (id, tenant_id, name, slug)
		VALUES ($1, $2, 'Other Loc', 'other-tier-loc')`, otherLocation, otherTenant)
	require.NoError(t, err)

	var theirTier uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO staff_tiers
		(tenant_id, location_id, name, rank) VALUES ($1, $2, 'Their Senior', 20) RETURNING id`,
		otherTenant, otherLocation).Scan(&theirTier))
	theirVariant := seedServiceVariant(t, f.pool, otherTenant, otherLocation, "Their Cut", 30, 35000, true)
	var theirStaff uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO staff_members
		(tenant_id, location_id, name, phone_number, role, is_active)
		VALUES ($1, $2, 'Their Barber', $3, 'barber', true) RETURNING id`,
		otherTenant, otherLocation, "+9195"+uuid.NewString()[:9]).Scan(&theirStaff))

	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"patch their tier", http.MethodPatch, "/v1/admin/tiers/" + theirTier.String(), map[string]any{"name": "Hijacked"}},
		{"delete their tier", http.MethodDelete, "/v1/admin/tiers/" + theirTier.String(), nil},
		{"default their tier", http.MethodPost, "/v1/admin/tiers/" + theirTier.String() + "/default", nil},
		{"read their matrix", http.MethodGet, "/v1/admin/tiers/" + theirTier.String() + "/prices", nil},
		{"bulk their tier", http.MethodPost, "/v1/admin/tiers/" + theirTier.String() + "/prices/bulk", map[string]any{"percent": 10}},
		{"price their variant at my tier", http.MethodPut,
			fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", mine.Id, theirVariant), map[string]any{"price_paise": 1}},
		{"price my variant at their tier", http.MethodPut,
			fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", theirTier, myVariant), map[string]any{"price_paise": 1}},
		{"assign their barber", http.MethodPut, "/v1/admin/staff/" + theirStaff.String() + "/tier",
			map[string]any{"tier_id": mine.Id.String()}},
		{"assign my barber to their tier", http.MethodPut, "/v1/admin/staff/" + f.staffID.String() + "/tier",
			map[string]any{"tier_id": theirTier.String()}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := f.req(t, c.method, c.path, c.body)
			require.Equal(t, http.StatusNotFound, res.StatusCode, "A5: 404, never 403")
		})
	}

	// Nothing crossed over.
	var name string
	var active bool
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT name, is_active FROM staff_tiers WHERE id = $1`, theirTier).Scan(&name, &active))
	require.Equal(t, "Their Senior", name)
	require.True(t, active)

	var rows int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM service_variant_tier_prices WHERE tier_id = $1 OR service_variant_id = $2`,
		theirTier, theirVariant).Scan(&rows))
	require.Equal(t, 0, rows, "A5: no override row may reference another shop's tier or variant")

	var theirTierID *uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT tier_id FROM staff_members WHERE id = $1`, theirStaff).Scan(&theirTierID))
	require.Nil(t, theirTierID, "A5: their barber must not have been assigned my tier")
}

// A6: one live default, even under concurrency.
func TestTierRoutes_SetDefaultSerialises(t *testing.T) {
	f := newTierFix(t)
	ctx := context.Background()
	junior := f.createTier(t, "Junior", 10)
	senior := f.createTier(t, "Senior", 20)

	require.Equal(t, http.StatusNoContent,
		f.req(t, http.MethodPost, "/v1/admin/tiers/"+junior.Id.String()+"/default", nil).StatusCode)
	require.Equal(t, http.StatusNoContent,
		f.req(t, http.MethodPost, "/v1/admin/tiers/"+senior.Id.String()+"/default", nil).StatusCode)

	var defaultID uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT id FROM staff_tiers WHERE location_id = $1 AND is_default AND is_active`,
		f.locationID).Scan(&defaultID))
	require.Equal(t, senior.Id, defaultID, "the new default replaces the old one")

	// Concurrent set-default must queue on the lock, not collide on
	// idx_staff_tiers_one_default. Any 500 here is that index rejecting a write.
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := junior.Id
			if i%2 == 0 {
				target = senior.Id
			}
			res := do(t, http.MethodPost, f.srv.URL+"/v1/admin/tiers/"+target.String()+"/default", f.jwt, nil)
			codes[i] = res.StatusCode
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		require.Equal(t, http.StatusNoContent, c, "A6: concurrent call %d must serialise, not fail", i)
	}

	var n int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM staff_tiers WHERE location_id = $1 AND is_default AND is_active`,
		f.locationID).Scan(&n))
	require.Equal(t, 1, n, "A6: exactly one live default survives")
}

// A7 + A15-A19: the sparse matrix and its read side.
func TestTierRoutes_SparseMatrixAndResolvedRead(t *testing.T) {
	f := newTierFix(t)
	tier := f.createTier(t, "Senior", 20)
	cut := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)
	colour := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Colour", 60, 90000, true)
	retired := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Retired Service", 20, 10000, false)

	// A19: nothing set yet — everything inherited, at base.
	t.Run("A19 zero overrides is all inherited at base", func(t *testing.T) {
		rows := f.matrix(t, tier.Id)
		require.Len(t, rows, 2, "A17: the inactive variant must not appear")
		for _, r := range rows {
			require.True(t, r.Inherited)
			require.Equal(t, r.BasePricePaise, r.PricePaise)
			require.Equal(t, r.BaseDurationMinutes, r.DurationMinutes)
			require.True(t, r.IsOffered)
			require.NotEqual(t, retired, r.ServiceVariantId)
		}
	})

	t.Run("A15 a set override shows alongside inherited rows", func(t *testing.T) {
		res := f.req(t, http.MethodPut, fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", tier.Id, cut),
			map[string]any{"price_paise": 45000, "duration_minutes": 40})
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		rows := f.matrix(t, tier.Id)
		require.Len(t, rows, 2, "A15: one row per active variant, override or not")
		byID := map[uuid.UUID]ResolvedTierPrice{}
		for _, r := range rows {
			byID[r.ServiceVariantId] = r
		}
		require.Equal(t, 45000, byID[cut].PricePaise)
		require.Equal(t, 35000, byID[cut].BasePricePaise, "base must still be reported")
		require.Equal(t, 40, byID[cut].DurationMinutes)
		require.Equal(t, 30, byID[cut].BaseDurationMinutes)
		require.False(t, byID[cut].Inherited)
		require.True(t, byID[colour].Inherited, "an untouched variant stays inherited")
	})

	t.Run("A7 an override equal to base is deleted, not stored", func(t *testing.T) {
		before := f.overrideCount(t, tier.Id)
		require.Equal(t, 1, before)

		res := f.req(t, http.MethodPut, fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", tier.Id, cut),
			map[string]any{"price_paise": 35000, "duration_minutes": nil, "is_offered": true})
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.Equal(t, 0, f.overrideCount(t, tier.Id), "A7: a redundant row must not be stored")

		rows := f.matrix(t, tier.Id)
		for _, r := range rows {
			if r.ServiceVariantId == cut {
				require.True(t, r.Inherited, "A7: reads back as inherited, which is correct not lost")
				require.Equal(t, 35000, r.PricePaise)
			}
		}
	})

	// A16: is_offered=false carries information even at base price, so it is
	// stored and must report inherited=false.
	t.Run("A16 not-offered at base price survives and is not inherited", func(t *testing.T) {
		res := f.req(t, http.MethodPut, fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", tier.Id, colour),
			map[string]any{"price_paise": 90000, "is_offered": false})
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.Equal(t, 1, f.overrideCount(t, tier.Id), "A16: an is_offered=false row is never redundant")

		for _, r := range f.matrix(t, tier.Id) {
			if r.ServiceVariantId == colour {
				require.False(t, r.IsOffered)
				require.False(t, r.Inherited, "A16: an override exists, so it is not inherited")
				require.Equal(t, r.BasePricePaise, r.PricePaise)
			}
		}
	})

	// A18: another shop's variants must never appear in my matrix.
	t.Run("A18 scoped to the caller", func(t *testing.T) {
		ctx := context.Background()
		otherTenant, otherLocation := uuid.New(), uuid.New()
		_, err := f.pool.Exec(ctx, `INSERT INTO tenants (id, name, slug, owner_phone_number)
			VALUES ($1, 'Matrix Other', 'matrix-other', '+919000000004')`, otherTenant)
		require.NoError(t, err)
		_, err = f.pool.Exec(ctx, `INSERT INTO locations (id, tenant_id, name, slug)
			VALUES ($1, $2, 'Matrix Other Loc', 'matrix-other-loc')`, otherLocation, otherTenant)
		require.NoError(t, err)
		theirs := seedServiceVariant(t, f.pool, otherTenant, otherLocation, "Their Service", 30, 50000, true)

		for _, r := range f.matrix(t, tier.Id) {
			require.NotEqual(t, theirs, r.ServiceVariantId, "A18: another shop's variant leaked in")
		}
	})
}

// A8: the rounding table, one shop per case.
//
// Each case needs its own fixture, and newTierFix truncates — so this cannot
// share a function with the tests below, or it would wipe their fixture midway.
func TestTierRoutes_BulkApplyRounding(t *testing.T) {
	// Every expectation recomputed from the stated rule: percent is half-up to
	// the whole rupee (x.50 rounds UP), delta is exact.
	for _, c := range []struct {
		name       string
		basePaise  int
		body       map[string]any
		wantPaise  int
		wantRupees string
	}{
		{"350 +10%", 35000, map[string]any{"percent": 10}, 38500, "385.00 exactly"},
		{"355 +10%", 35500, map[string]any{"percent": 10}, 39100, "390.50 rounds up"},
		{"345 +10%", 34500, map[string]any{"percent": 10}, 38000, "379.50 rounds up"},
		{"299 +10%", 29900, map[string]any{"percent": 10}, 32900, "328.90 rounds up"},
		{"350 -Rs50", 35000, map[string]any{"delta_paise": -5000}, 30000, "exact, never rounded"},
		{"350 -10%", 35000, map[string]any{"percent": -10}, 31500, "315.00 exactly"},
		{"355 -10%", 35500, map[string]any{"percent": -10}, 32000, "319.50 rounds up"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sub := newTierFix(t)
			subTier := sub.createTier(t, "Senior", 20)
			v := seedServiceVariant(t, sub.pool, sub.tenantID, sub.locationID, "Cut", 30, c.basePaise, true)

			res := sub.req(t, http.MethodPost, "/v1/admin/tiers/"+subTier.Id.String()+"/prices/bulk", c.body)
			require.Equal(t, http.StatusOK, res.StatusCode)

			var got int
			for _, r := range sub.matrix(t, subTier.Id) {
				if r.ServiceVariantId == v {
					got = r.PricePaise
				}
			}
			require.Equal(t, c.wantPaise, got, "A8 %s: %s", c.name, c.wantRupees)
		})
	}
}

// A9 + A10: idempotence, and what bulk apply must not touch.
func TestTierRoutes_BulkApply(t *testing.T) {
	f := newTierFix(t)
	tier := f.createTier(t, "Senior", 20)

	bulk := func(t *testing.T, body map[string]any) int {
		t.Helper()
		res := f.req(t, http.MethodPost, "/v1/admin/tiers/"+tier.Id.String()+"/prices/bulk", body)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var out struct {
			Affected int `json:"affected"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out.Affected
	}
	priceOf := func(t *testing.T, variantID uuid.UUID) int {
		t.Helper()
		for _, r := range f.matrix(t, tier.Id) {
			if r.ServiceVariantId == variantID {
				return r.PricePaise
			}
		}
		t.Fatalf("variant %s missing from matrix", variantID)
		return 0
	}

	cut := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)

	t.Run("A9 a repeated apply does not compound", func(t *testing.T) {
		bulk(t, map[string]any{"delta_paise": 10000})
		require.Equal(t, 45000, priceOf(t, cut))
		bulk(t, map[string]any{"delta_paise": 10000})
		require.Equal(t, 45000, priceOf(t, cut), "A9: computed from base, never from the current override")
	})

	t.Run("A10 duration and is_offered on an existing row survive", func(t *testing.T) {
		res := f.req(t, http.MethodPut, fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", tier.Id, cut),
			map[string]any{"price_paise": 50000, "duration_minutes": 45, "is_offered": false})
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		bulk(t, map[string]any{"percent": 10})

		var price, duration int
		var offered bool
		require.NoError(t, f.pool.QueryRow(context.Background(),
			`SELECT price_paise, duration_minutes, is_offered FROM service_variant_tier_prices
			 WHERE tier_id = $1 AND service_variant_id = $2`, tier.Id, cut).Scan(&price, &duration, &offered))
		require.Equal(t, 38500, price, "price recomputed from base 350 +10%")
		require.Equal(t, 45, duration, "A10: duration preserved")
		require.False(t, offered, "A10: a price change must not re-enable a service")
	})

	t.Run("negative result is refused, not clamped", func(t *testing.T) {
		res := f.req(t, http.MethodPost, "/v1/admin/tiers/"+tier.Id.String()+"/prices/bulk",
			map[string]any{"delta_paise": -999999})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, "NEGATIVE_PRICE", decodeCode(t, res))
	})
}

// A11: the gate, on all nine.
func TestTierRoutes_RoleGate(t *testing.T) {
	f := newTierFix(t)
	tier := f.createTier(t, "Senior", 20)
	variant := seedServiceVariant(t, f.pool, f.tenantID, f.locationID, "Cut", 30, 35000, true)

	barberJWT, _, err := auth.GenerateAccessAndRefreshTokens(
		[]byte(f.s.Config.JWTSecret), f.tenantID.String(), f.locationID.String(), f.staffID.String(), "barber")
	require.NoError(t, err)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/tiers"},
		{http.MethodPost, "/v1/admin/tiers"},
		{http.MethodPatch, "/v1/admin/tiers/" + tier.Id.String()},
		{http.MethodDelete, "/v1/admin/tiers/" + tier.Id.String()},
		{http.MethodPost, "/v1/admin/tiers/" + tier.Id.String() + "/default"},
		{http.MethodPut, "/v1/admin/staff/" + f.staffID.String() + "/tier"},
		{http.MethodGet, "/v1/admin/tiers/" + tier.Id.String() + "/prices"},
		{http.MethodPut, fmt.Sprintf("/v1/admin/tiers/%s/prices/%s", tier.Id, variant)},
		{http.MethodPost, "/v1/admin/tiers/" + tier.Id.String() + "/prices/bulk"},
	}
	require.Len(t, routes, 9, "all nine routes must be gated")

	body := map[string]any{"name": "X", "rank": 1, "price_paise": 1, "percent": 10, "tier_id": nil}
	for _, r := range routes {
		t.Run("barber 403 "+r.method+" "+r.path, func(t *testing.T) {
			res := do(t, r.method, f.srv.URL+r.path, barberJWT, body)
			require.Equal(t, http.StatusForbidden, res.StatusCode)
		})
		t.Run("no jwt 401 "+r.method+" "+r.path, func(t *testing.T) {
			res := do(t, r.method, f.srv.URL+r.path, "", body)
			require.Equal(t, http.StatusUnauthorized, res.StatusCode)
		})
	}
}

func decodeCode(t *testing.T, res *http.Response) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	return body.Code
}
