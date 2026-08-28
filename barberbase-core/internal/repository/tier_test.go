package repository

import (
	"context"
	"strings"
	"sync"
	"testing"

	"barberbase-core/internal/domain/pricing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// tierShop is an isolated tenant + location with three variants and a barber.
type tierShop struct {
	tenantID uuid.UUID
	locID    uuid.UUID
	staffID  uuid.UUID
	variants []uuid.UUID // in creation order; base prices 25000 / 15000 / 120000
	repo     *TierRepository
}

func seedTierShop(t *testing.T, pool *pgxpool.Pool, label string) tierShop {
	t.Helper()
	ctx := context.Background()
	s := tierShop{repo: &TierRepository{Pool: pool}}
	suffix := uuid.NewString()[:8]

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ($1, $2, '+919999900000') RETURNING id`,
		"T2 "+label, "t2-"+suffix).Scan(&s.tenantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, 'T2 Location') RETURNING id`,
		s.tenantID, "t2loc-"+suffix).Scan(&s.locID))

	var categoryID, groupID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_categories (tenant_id, location_id, name, gender)
		VALUES ($1, $2, 'Hair', 'men') RETURNING id`, s.tenantID, s.locID).Scan(&categoryID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_groups (tenant_id, location_id, category_id, name)
		VALUES ($1, $2, $3, 'Fade') RETURNING id`, s.tenantID, s.locID, categoryID).Scan(&groupID))

	for _, v := range []struct {
		name     string
		price    int
		duration int
	}{{"Mid Fade", 25000, 30}, {"Beard Trim", 15000, 15}, {"Colour", 120000, 90}} {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO service_variants (tenant_id, location_id, group_id, name, duration_minutes, price_paise)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			s.tenantID, s.locID, groupID, v.name, v.duration, v.price).Scan(&id))
		s.variants = append(s.variants, id)
	}

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role)
		VALUES ($1, $2, 'Raju', $3, 'barber') RETURNING id`,
		s.tenantID, s.locID, "+9198"+suffix).Scan(&s.staffID))
	return s
}

func (s tierShop) overrideCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM service_variant_tier_prices WHERE location_id = $1`, s.locID).Scan(&n))
	return n
}

// TestTierSparseMatrix is A1 and A4 at the repository level: what is stored,
// and what is deliberately not stored.
func TestTierSparseMatrix(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	s := seedTierShop(t, pool, "sparse")

	tier, err := s.repo.CreateTier(ctx, s.tenantID, s.locID, "Senior", 20, nil)
	require.NoError(t, err)
	require.Zero(t, s.overrideCount(t, pool), "a new tier stores no override rows")

	// A1: absent row → base price and duration.
	variants, err := s.repo.GetVariantsForPricing(ctx, s.tenantID, s.locID, s.variants)
	require.NoError(t, err)
	require.Len(t, variants, 3)
	overrides, err := s.repo.GetOverrides(ctx, s.tenantID, s.locID, tier.ID, s.variants)
	require.NoError(t, err)
	require.Empty(t, overrides)

	sel := pricing.ResolveSelection(variants, overrides)
	require.True(t, sel.Available)
	require.Equal(t, 160000, sel.TotalPricePaise, "A1: 25000+15000+120000 from base")
	require.Equal(t, 135, sel.TotalDurationMinutes)

	// A4: an override equal to base is not stored.
	require.NoError(t, s.repo.UpsertOverride(ctx, s.tenantID, s.locID, tier.ID, s.variants[0],
		25000, nil, true))
	require.Zero(t, s.overrideCount(t, pool), "A4: a redundant override must be deleted, not stored")

	// A real override is stored.
	require.NoError(t, s.repo.UpsertOverride(ctx, s.tenantID, s.locID, tier.ID, s.variants[0],
		45000, nil, true))
	require.Equal(t, 1, s.overrideCount(t, pool))

	// A1: override price, inherited duration.
	overrides, err = s.repo.GetOverrides(ctx, s.tenantID, s.locID, tier.ID, s.variants)
	require.NoError(t, err)
	got := pricing.Resolve(variants[findVariant(t, variants, s.variants[0])], ptrOverride(overrides, s.variants[0]))
	require.Equal(t, 45000, got.PricePaise)
	require.Equal(t, 30, got.DurationMinutes, "A1: NULL duration inherits")

	// A4: editing it back to base removes the row again.
	require.NoError(t, s.repo.UpsertOverride(ctx, s.tenantID, s.locID, tier.ID, s.variants[0],
		25000, nil, true))
	require.Zero(t, s.overrideCount(t, pool), "A4: the matrix returns to sparse")

	// A row carrying is_offered=false is never redundant, even at base price.
	require.NoError(t, s.repo.UpsertOverride(ctx, s.tenantID, s.locID, tier.ID, s.variants[2],
		120000, nil, false))
	require.Equal(t, 1, s.overrideCount(t, pool), "A4: a not-offered row carries information")
}

func findVariant(t *testing.T, vs []pricing.Variant, id uuid.UUID) int {
	t.Helper()
	for i, v := range vs {
		if v.ID == id {
			return i
		}
	}
	t.Fatalf("variant %s not found", id)
	return -1
}

func ptrOverride(m map[uuid.UUID]pricing.Override, id uuid.UUID) *pricing.Override {
	if o, ok := m[id]; ok {
		return &o
	}
	return nil
}

// TestTierBulkApply is A5, A11 and A12 against real rows.
func TestTierBulkApply(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	s := seedTierShop(t, pool, "bulk")
	tier, err := s.repo.CreateTier(ctx, s.tenantID, s.locID, "Senior", 20, nil)
	require.NoError(t, err)

	delta := 10000 // +₹100
	n, err := s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{DeltaPaise: &delta})
	require.NoError(t, err)
	require.Equal(t, 3, n, "A5: one row per active variant, no more")
	require.Equal(t, 3, s.overrideCount(t, pool))

	prices := map[uuid.UUID]int{}
	rows, err := pool.Query(ctx, `
		SELECT service_variant_id, price_paise FROM service_variant_tier_prices
		WHERE tier_id = $1`, tier.ID)
	require.NoError(t, err)
	for rows.Next() {
		var id uuid.UUID
		var p int
		require.NoError(t, rows.Scan(&id, &p))
		prices[id] = p
	}
	rows.Close()
	require.Equal(t, 35000, prices[s.variants[0]], "25000 + 10000")
	require.Equal(t, 25000, prices[s.variants[1]], "15000 + 10000")
	require.Equal(t, 130000, prices[s.variants[2]], "120000 + 10000")

	// A5: idempotent, because the adjustment is computed from the BASE price,
	// not from whatever the last apply left behind.
	n2, err := s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{DeltaPaise: &delta})
	require.NoError(t, err)
	require.Equal(t, 3, n2)
	require.Equal(t, 3, s.overrideCount(t, pool))
	var after int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT price_paise FROM service_variant_tier_prices
		WHERE tier_id = $1 AND service_variant_id = $2`, tier.ID, s.variants[0]).Scan(&after))
	require.Equal(t, 35000, after, "A5: a second identical apply changes nothing")

	// A zero adjustment makes every row redundant, so the matrix empties itself.
	zero := 0
	n3, err := s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{DeltaPaise: &zero})
	require.NoError(t, err)
	require.Zero(t, n3)
	require.Zero(t, s.overrideCount(t, pool), "A4/A5: base-equal rows are removed, matrix stays sparse")

	// A11 over real rows: +10% on 250/150/1200 rupees.
	pct := 10
	_, err = s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{Percent: &pct})
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT price_paise FROM service_variant_tier_prices
		WHERE tier_id = $1 AND service_variant_id = $2`, tier.ID, s.variants[1]).Scan(&after))
	require.Equal(t, 16500, after, "A11: ₹150 +10% = ₹165")

	// A12: a decrease past zero is refused, and nothing is written.
	before := s.overrideCount(t, pool)
	tooMuch := -500000
	_, err = s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{DeltaPaise: &tooMuch})
	require.ErrorIs(t, err, pricing.ErrNegativePrice, "A12: refused with a domain error")
	require.Equal(t, before, s.overrideCount(t, pool), "A12: the transaction rolled back")

	// Exactly one of the two modes is required.
	_, err = s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID, BulkAdjustment{})
	require.Error(t, err)
	_, err = s.repo.BulkApplyTierPrices(ctx, s.tenantID, s.locID, tier.ID,
		BulkAdjustment{DeltaPaise: &delta, Percent: &pct})
	require.Error(t, err)
}

// TestTierDefaultAndDeactivate is A6 and A7.
func TestTierDefaultAndDeactivate(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	s := seedTierShop(t, pool, "default")

	junior, err := s.repo.CreateTier(ctx, s.tenantID, s.locID, "Junior", 10, nil)
	require.NoError(t, err)
	senior, err := s.repo.CreateTier(ctx, s.tenantID, s.locID, "Senior", 20, nil)
	require.NoError(t, err)

	t.Run("A6_setting_default_clears_the_previous_one", func(t *testing.T) {
		require.NoError(t, s.repo.SetDefaultTier(ctx, s.tenantID, s.locID, junior.ID))
		require.NoError(t, s.repo.SetDefaultTier(ctx, s.tenantID, s.locID, senior.ID))

		tiers, err := s.repo.ListTiers(ctx, s.tenantID, s.locID, true)
		require.NoError(t, err)
		var defaults []string
		for _, tr := range tiers {
			if tr.IsDefault {
				defaults = append(defaults, tr.Name)
			}
		}
		require.Equal(t, []string{"Senior"}, defaults, "A6: exactly one default, and it moved")
	})

	t.Run("A6_concurrent_set_default_serialises", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				target := junior.ID
				if n%2 == 0 {
					target = senior.ID
				}
				errs[n] = s.repo.SetDefaultTier(context.Background(), s.tenantID, s.locID, target)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			require.NoError(t, err, "A6: attempt %d must serialise, not error", i)
		}
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM staff_tiers
			WHERE location_id = $1 AND is_default AND is_active`, s.locID).Scan(&n))
		require.Equal(t, 1, n, "A6: the partial unique index invariant holds")
	})

	t.Run("A7_deactivating_an_occupied_tier_is_refused_by_name", func(t *testing.T) {
		require.NoError(t, s.repo.AssignStaffTier(ctx, s.tenantID, s.locID, s.staffID, &junior.ID))

		err := s.repo.DeactivateTier(ctx, s.tenantID, s.locID, junior.ID)
		require.ErrorIs(t, err, ErrTierInUse, "A7: a domain error, never a raw 23503")
		require.Contains(t, err.Error(), "Raju", "A7: the owner needs to know which barber")
		require.NotContains(t, err.Error(), "23503")
		require.NotContains(t, strings.ToLower(err.Error()), "constraint")

		var stillActive bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT is_active FROM staff_tiers WHERE id = $1`, junior.ID).Scan(&stillActive))
		require.True(t, stillActive, "A7: nothing changed")
	})

	t.Run("A7_deactivation_succeeds_once_vacated", func(t *testing.T) {
		require.NoError(t, s.repo.AssignStaffTier(ctx, s.tenantID, s.locID, s.staffID, nil))
		require.NoError(t, s.repo.DeactivateTier(ctx, s.tenantID, s.locID, junior.ID))

		active, err := s.repo.ListTiers(ctx, s.tenantID, s.locID, false)
		require.NoError(t, err)
		require.Len(t, active, 1)
		require.Equal(t, "Senior", active[0].Name)

		all, err := s.repo.ListTiers(ctx, s.tenantID, s.locID, true)
		require.NoError(t, err)
		require.Len(t, all, 2, "soft delete keeps the row")
	})
}

// TestTierTenantIsolation is A8 (Law 11). Tenant and location come from the
// caller, and another shop's rows are invisible and unmodifiable.
func TestTierTenantIsolation(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	mine := seedTierShop(t, pool, "mine")
	theirs := seedTierShop(t, pool, "theirs")

	theirTier, err := theirs.repo.CreateTier(ctx, theirs.tenantID, theirs.locID, "Expert", 30, nil)
	require.NoError(t, err)
	require.NoError(t, theirs.repo.UpsertOverride(ctx, theirs.tenantID, theirs.locID,
		theirTier.ID, theirs.variants[0], 99000, nil, true))

	// Invisible.
	tiers, err := mine.repo.ListTiers(ctx, mine.tenantID, mine.locID, true)
	require.NoError(t, err)
	require.Empty(t, tiers, "A8: another tenant's tiers must not be listed")

	overrides, err := mine.repo.GetOverrides(ctx, mine.tenantID, mine.locID, theirTier.ID, theirs.variants)
	require.NoError(t, err)
	require.Empty(t, overrides, "A8: another tenant's overrides must not be readable")

	variants, err := mine.repo.GetVariantsForPricing(ctx, mine.tenantID, mine.locID, theirs.variants)
	require.NoError(t, err)
	require.Empty(t, variants, "A8: another tenant's variants must not be priceable")

	// Unmodifiable — every write is scoped by the caller's tenant and location.
	_, err = mine.repo.UpdateTier(ctx, mine.tenantID, mine.locID, theirTier.ID, nil, nil, nil)
	require.ErrorIs(t, err, ErrTierNotFound, "A8: cross-tenant update must not find the row")
	require.ErrorIs(t, mine.repo.SetDefaultTier(ctx, mine.tenantID, mine.locID, theirTier.ID), ErrTierNotFound)
	require.ErrorIs(t, mine.repo.DeactivateTier(ctx, mine.tenantID, mine.locID, theirTier.ID), ErrTierNotFound)
	require.ErrorIs(t, mine.repo.AssignStaffTier(ctx, mine.tenantID, mine.locID, theirs.staffID, nil), ErrTierNotFound)

	delta := 10000
	_, err = mine.repo.BulkApplyTierPrices(ctx, mine.tenantID, mine.locID, theirTier.ID,
		BulkAdjustment{DeltaPaise: &delta})
	require.ErrorIs(t, err, ErrTierNotFound, "A8: bulk apply cannot reach another tenant's tier")

	// Their row is untouched throughout.
	var price int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT price_paise FROM service_variant_tier_prices WHERE tier_id = $1`,
		theirTier.ID).Scan(&price))
	require.Equal(t, 99000, price, "A8: nothing of theirs changed")
}

// TestTierNoTiersConfigured is A10: a shop with no tiers and no overrides prices
// exactly as it did before this unit existed.
func TestTierNoTiersConfigured(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	s := seedTierShop(t, pool, "untiered")

	require.Zero(t, s.overrideCount(t, pool))
	tiers, err := s.repo.ListTiers(ctx, s.tenantID, s.locID, true)
	require.NoError(t, err)
	require.Empty(t, tiers)

	variants, err := s.repo.GetVariantsForPricing(ctx, s.tenantID, s.locID, s.variants)
	require.NoError(t, err)
	sel := pricing.ResolveSelection(variants, nil)
	require.True(t, sel.Available)
	require.Equal(t, 160000, sel.TotalPricePaise, "A10: identical to the pre-T2 base sum")
	require.Equal(t, 135, sel.TotalDurationMinutes)
	for _, item := range sel.Items {
		require.True(t, item.Inherited, "A10: everything inherits when no tier exists")
	}
}
