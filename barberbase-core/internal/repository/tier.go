package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"barberbase-core/internal/domain/pricing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tier domain errors. Handlers map these to responses; a raw SQLSTATE must
// never reach a caller.
var (
	// ErrTierInUse is returned when deactivating a tier that active barbers
	// still sit in. The message names them so the owner knows what to fix.
	ErrTierInUse = errors.New("tier is still assigned to active staff")
	// ErrTierNotFound covers both "no such tier" and "not yours" — a tier from
	// another tenant is indistinguishable from one that does not exist (Law 11).
	ErrTierNotFound = errors.New("tier not found")
)

// TierRow is a staff_tiers row.
type TierRow struct {
	ID          uuid.UUID
	Name        string
	Rank        int
	Description *string
	IsDefault   bool
	IsActive    bool
	SortOrder   int
}

// TierRepository owns staff_tiers and the sparse service_variant_tier_prices
// matrix. Every method takes tenantID and locationID from the caller's JWT and
// filters on both — Law 11: never trust a request body for either.
type TierRepository struct {
	Pool *pgxpool.Pool
}

// CreateTier inserts a tier. rank orders tiers against each other; 10/20/30
// spacing leaves room to slot one in later.
func (r *TierRepository) CreateTier(ctx context.Context, tenantID, locationID uuid.UUID,
	name string, rank int, description *string) (TierRow, error) {
	var t TierRow
	err := r.Pool.QueryRow(ctx, `
		INSERT INTO staff_tiers (tenant_id, location_id, name, rank, description, sort_order)
		VALUES ($1, $2, $3, $4, $5, $4)
		RETURNING id, name, rank, description, is_default, is_active, sort_order`,
		tenantID, locationID, name, rank, description,
	).Scan(&t.ID, &t.Name, &t.Rank, &t.Description, &t.IsDefault, &t.IsActive, &t.SortOrder)
	return t, err
}

// ListTiers returns a location's tiers in rank order. includeInactive is false
// for pickers and true for the admin screen.
func (r *TierRepository) ListTiers(ctx context.Context, tenantID, locationID uuid.UUID,
	includeInactive bool) ([]TierRow, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, name, rank, description, is_default, is_active, sort_order
		FROM staff_tiers
		WHERE tenant_id = $1 AND location_id = $2
		  AND ($3 OR is_active)
		ORDER BY rank`, tenantID, locationID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TierRow
	for rows.Next() {
		var t TierRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Rank, &t.Description,
			&t.IsDefault, &t.IsActive, &t.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTier renames or re-ranks a tier. Nil fields are left alone.
func (r *TierRepository) UpdateTier(ctx context.Context, tenantID, locationID, tierID uuid.UUID,
	name *string, rank *int, description *string) (TierRow, error) {
	var t TierRow
	err := r.Pool.QueryRow(ctx, `
		UPDATE staff_tiers SET
			name        = COALESCE($4, name),
			rank        = COALESCE($5, rank),
			description = COALESCE($6, description),
			updated_at  = NOW()
		WHERE id = $3 AND tenant_id = $1 AND location_id = $2
		RETURNING id, name, rank, description, is_default, is_active, sort_order`,
		tenantID, locationID, tierID, name, rank, description,
	).Scan(&t.ID, &t.Name, &t.Rank, &t.Description, &t.IsDefault, &t.IsActive, &t.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return TierRow{}, ErrTierNotFound
	}
	return t, err
}

// DeactivateTier soft-deletes. There is no hard delete: staff_members.tier_id
// and queue_entries.required_tier_id reference staff_tiers with no ON DELETE
// clause, so a DELETE returns a raw 23503 that no owner should ever see.
//
// Refused while active barbers still sit in the tier, naming them.
func (r *TierRepository) DeactivateTier(ctx context.Context, tenantID, locationID, tierID uuid.UUID) error {
	return WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT name FROM staff_members
			WHERE tenant_id = $1 AND location_id = $2 AND tier_id = $3 AND is_active
			ORDER BY name`, tenantID, locationID, tierID)
		if err != nil {
			return err
		}
		var holders []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			holders = append(holders, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(holders) > 0 {
			return fmt.Errorf("%w: %s", ErrTierInUse, strings.Join(holders, ", "))
		}

		tag, err := tx.Exec(ctx, `
			UPDATE staff_tiers SET is_active = false, is_default = false, updated_at = NOW()
			WHERE id = $3 AND tenant_id = $1 AND location_id = $2`,
			tenantID, locationID, tierID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTierNotFound
		}
		return nil
	})
}

// SetDefaultTier makes one tier the location's default, clearing the previous
// one in the same transaction. The partial unique index rejects any other
// ordering, so this is the only safe way to do it.
//
// Concurrent callers serialise on the FOR UPDATE rather than racing the index.
func (r *TierRepository) SetDefaultTier(ctx context.Context, tenantID, locationID, tierID uuid.UUID) error {
	return WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Lock the location's tiers first: two concurrent set-default calls must
		// queue, not collide on idx_staff_tiers_one_default.
		if _, err := tx.Exec(ctx, `
			SELECT id FROM staff_tiers
			WHERE tenant_id = $1 AND location_id = $2 AND is_active
			FOR UPDATE`, tenantID, locationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE staff_tiers SET is_default = false, updated_at = NOW()
			WHERE tenant_id = $1 AND location_id = $2 AND is_default AND id <> $3`,
			tenantID, locationID, tierID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE staff_tiers SET is_default = true, updated_at = NOW()
			WHERE id = $3 AND tenant_id = $1 AND location_id = $2 AND is_active`,
			tenantID, locationID, tierID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTierNotFound
		}
		return nil
	})
}

// AssignStaffTier sets or clears a barber's tier. tierID nil clears it, which
// puts the barber back on base pricing.
func (r *TierRepository) AssignStaffTier(ctx context.Context, tenantID, locationID,
	staffID uuid.UUID, tierID *uuid.UUID) error {
	tag, err := r.Pool.Exec(ctx, `
		UPDATE staff_members SET tier_id = $4, updated_at = NOW()
		WHERE id = $3 AND tenant_id = $1 AND location_id = $2
		  AND ($4::uuid IS NULL OR EXISTS (
			SELECT 1 FROM staff_tiers
			WHERE id = $4 AND tenant_id = $1 AND location_id = $2 AND is_active))`,
		tenantID, locationID, staffID, tierID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTierNotFound
	}
	return nil
}

// GetVariantsForPricing loads base pricing for the given variants, scoped to the
// caller's tenant and location. Variants that do not belong are simply absent.
func (r *TierRepository) GetVariantsForPricing(ctx context.Context, tenantID, locationID uuid.UUID,
	variantIDs []uuid.UUID) ([]pricing.Variant, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, name, price_paise, duration_minutes
		FROM service_variants
		WHERE tenant_id = $1 AND location_id = $2 AND id = ANY($3) AND is_active
		ORDER BY name`, tenantID, locationID, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pricing.Variant
	for rows.Next() {
		var v pricing.Variant
		if err := rows.Scan(&v.ID, &v.Name, &v.PricePaise, &v.DurationMinutes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetOverrides loads the sparse matrix entries for the given variants at one
// tier. Only rows that exist are returned; a missing key means inherit.
func (r *TierRepository) GetOverrides(ctx context.Context, tenantID, locationID, tierID uuid.UUID,
	variantIDs []uuid.UUID) (map[uuid.UUID]pricing.Override, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT service_variant_id, price_paise, duration_minutes, is_offered
		FROM service_variant_tier_prices
		WHERE tenant_id = $1 AND location_id = $2 AND tier_id = $3
		  AND service_variant_id = ANY($4)`, tenantID, locationID, tierID, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]pricing.Override{}
	for rows.Next() {
		var id uuid.UUID
		var o pricing.Override
		if err := rows.Scan(&id, &o.PricePaise, &o.DurationMinutes, &o.IsOffered); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

// UpsertOverride writes one entry in the sparse matrix — or removes it.
//
// An override that says exactly what the variant already says carries no
// information, so it is DELETED rather than stored. That is what keeps a
// 40-variant, 4-tier shop at ~30 rows instead of 160.
func (r *TierRepository) UpsertOverride(ctx context.Context, tenantID, locationID, tierID,
	variantID uuid.UUID, pricePaise int, durationMinutes *int, isOffered bool) error {
	return WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		var basePrice, baseDuration int
		err := tx.QueryRow(ctx, `
			SELECT price_paise, duration_minutes FROM service_variants
			WHERE id = $3 AND tenant_id = $1 AND location_id = $2`,
			tenantID, locationID, variantID).Scan(&basePrice, &baseDuration)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTierNotFound
		}
		if err != nil {
			return err
		}

		redundant := pricePaise == basePrice && isOffered &&
			(durationMinutes == nil || *durationMinutes == baseDuration)
		if redundant {
			_, err := tx.Exec(ctx, `
				DELETE FROM service_variant_tier_prices
				WHERE tenant_id = $1 AND location_id = $2 AND tier_id = $3 AND service_variant_id = $4`,
				tenantID, locationID, tierID, variantID)
			return err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO service_variant_tier_prices
				(tenant_id, location_id, service_variant_id, tier_id,
				 price_paise, duration_minutes, is_offered)
			VALUES ($1, $2, $4, $3, $5, $6, $7)
			ON CONFLICT (service_variant_id, tier_id) DO UPDATE SET
				price_paise      = EXCLUDED.price_paise,
				duration_minutes = EXCLUDED.duration_minutes,
				is_offered       = EXCLUDED.is_offered,
				updated_at       = NOW()`,
			tenantID, locationID, tierID, variantID, pricePaise, durationMinutes, isOffered)
		return err
	})
}

// BulkAdjustment is how an owner says "seniors are ₹100 more" or "juniors are
// 10% less" without editing forty rows by hand.
type BulkAdjustment struct {
	DeltaPaise *int // fixed amount, exact
	Percent    *int // whole-number percent, half-up to the nearest rupee
}

// BulkApplyTierPrices sets every active variant's price at one tier, computed
// from the variant's BASE price.
//
// Computing from base rather than from the current override is what makes a
// repeated apply idempotent: "+₹100 at Senior" run twice still means base+₹100,
// not base+₹200. It is also what an owner means by the phrase.
//
// Existing duration_minutes and is_offered on a row are preserved — bulk apply
// is about price. Rows that end up saying nothing the variant does not already
// say are deleted, so the matrix stays sparse.
func (r *TierRepository) BulkApplyTierPrices(ctx context.Context, tenantID, locationID,
	tierID uuid.UUID, adj BulkAdjustment) (int, error) {
	if (adj.DeltaPaise == nil) == (adj.Percent == nil) {
		return 0, errors.New("bulk apply needs exactly one of DeltaPaise or Percent")
	}

	var affected int
	err := WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM staff_tiers
			WHERE id = $3 AND tenant_id = $1 AND location_id = $2 AND is_active)`,
			tenantID, locationID, tierID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrTierNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, price_paise FROM service_variants
			WHERE tenant_id = $1 AND location_id = $2 AND is_active`, tenantID, locationID)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		var prices []int
		for rows.Next() {
			var id uuid.UUID
			var base int
			if err := rows.Scan(&id, &base); err != nil {
				rows.Close()
				return err
			}
			// Rounding lives in the pure resolver, not in SQL.
			var out int
			if adj.DeltaPaise != nil {
				out, err = pricing.ApplyDelta(base, *adj.DeltaPaise)
			} else {
				out, err = pricing.ApplyPercent(base, *adj.Percent)
			}
			if err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			prices = append(prices, out)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO service_variant_tier_prices
				(tenant_id, location_id, service_variant_id, tier_id, price_paise)
			SELECT $1, $2, v.id, $3, v.price
			FROM unnest($4::uuid[], $5::int[]) AS v(id, price)
			ON CONFLICT (service_variant_id, tier_id) DO UPDATE SET
				price_paise = EXCLUDED.price_paise,
				updated_at  = NOW()`,
			tenantID, locationID, tierID, ids, prices); err != nil {
			return err
		}

		// Drop rows that now say nothing the variant does not already say.
		tag, err := tx.Exec(ctx, `
			DELETE FROM service_variant_tier_prices p
			USING service_variants sv
			WHERE p.service_variant_id = sv.id
			  AND p.tenant_id = $1 AND p.location_id = $2 AND p.tier_id = $3
			  AND p.price_paise = sv.price_paise
			  AND p.duration_minutes IS NULL
			  AND p.is_offered`, tenantID, locationID, tierID)
		if err != nil {
			return err
		}
		affected = len(ids) - int(tag.RowsAffected())
		return nil
	})
	return affected, err
}
