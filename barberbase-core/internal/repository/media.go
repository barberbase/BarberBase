package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Media domain errors. Handlers map these to responses; a raw SQLSTATE must
// never reach a caller.
var (
	// ErrAssetNotFound covers both "no such asset" and "not yours" — an asset
	// from another tenant is indistinguishable from one that does not exist
	// (Law 11).
	ErrAssetNotFound = errors.New("media asset not found")
	// ErrVariantFull means the per-variant image cap is reached.
	ErrVariantFull = errors.New("this service already has the maximum number of images")
	// ErrBadContentHash is a malformed client-supplied hash.
	ErrBadContentHash = errors.New("content_hash must be 64 lowercase hex characters")
)

// contentHashRe validates the SHAPE of a client-supplied hash, which is all we
// can do. See MediaRepository.CreatePending for why the VALUE is untrusted.
var contentHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateContentHash rejects anything that is not 64 lowercase hex. A malformed
// hash produces a malformed R2 key, and that is worth catching even though the
// value itself is unverifiable.
func ValidateContentHash(h string) error {
	if !contentHashRe.MatchString(h) {
		return ErrBadContentHash
	}
	return nil
}

type MediaRepository struct{ Pool *pgxpool.Pool }

// MediaAsset is one row of media_assets.
type MediaAsset struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	LocationID       uuid.UUID
	Purpose          string
	ServiceVariantID *uuid.UUID
	StaffMemberID    *uuid.UUID
	R2Key            string
	ContentHash      string
	Bytes            *int
	AltText          *string
	SortOrder        int
	IsPrimary        bool
	Status           string
	CreatedAt        time.Time
	CommittedAt      *time.Time
}

// CreatePending inserts a pending row, or returns the existing one when the same
// bytes are presigned twice for the same variant.
//
// CONTENT HASH IS CLIENT-ASSERTED AND UNVERIFIABLE. The droplet never reads
// object bytes — that is this pipeline's central constraint — so it cannot
// confirm the hash describes what was actually uploaded. It is a cache key and a
// dedup key, never an integrity proof. Keys are scoped to location_id (and
// variant_id or staff_id below that), so a forged or colliding hash can only
// collide with objects that same shop already owns. Nothing security-relevant
// may ever depend on this value.
func (r *MediaRepository) CreatePending(ctx context.Context, tenantID, locationID uuid.UUID,
	purpose, r2Key, contentHash string, variantID, staffID *uuid.UUID) (MediaAsset, error) {

	var a MediaAsset
	err := r.Pool.QueryRow(ctx, `
		INSERT INTO media_assets
			(tenant_id, location_id, purpose, service_variant_id, staff_member_id,
			 r2_key, content_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
		ON CONFLICT (r2_key) DO UPDATE SET r2_key = EXCLUDED.r2_key
		RETURNING id, tenant_id, location_id, purpose, service_variant_id, staff_member_id,
		          r2_key, content_hash, bytes, alt_text, sort_order, is_primary, status,
		          created_at, committed_at`,
		tenantID, locationID, purpose, variantID, staffID, r2Key, contentHash,
	).Scan(&a.ID, &a.TenantID, &a.LocationID, &a.Purpose, &a.ServiceVariantID, &a.StaffMemberID,
		&a.R2Key, &a.ContentHash, &a.Bytes, &a.AltText, &a.SortOrder, &a.IsPrimary, &a.Status,
		&a.CreatedAt, &a.CommittedAt)
	if err != nil {
		return MediaAsset{}, fmt.Errorf("create pending asset: %w", err)
	}
	return a, nil
}

// GetForCommit reads one asset, scoped to the caller's tenant and location.
func (r *MediaRepository) GetForCommit(ctx context.Context, tenantID, locationID, assetID uuid.UUID) (MediaAsset, error) {
	var a MediaAsset
	err := r.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, location_id, purpose, service_variant_id, staff_member_id,
		       r2_key, content_hash, bytes, alt_text, sort_order, is_primary, status,
		       created_at, committed_at
		FROM media_assets
		WHERE id = $1 AND tenant_id = $2 AND location_id = $3`,
		assetID, tenantID, locationID,
	).Scan(&a.ID, &a.TenantID, &a.LocationID, &a.Purpose, &a.ServiceVariantID, &a.StaffMemberID,
		&a.R2Key, &a.ContentHash, &a.Bytes, &a.AltText, &a.SortOrder, &a.IsPrimary, &a.Status,
		&a.CreatedAt, &a.CommittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaAsset{}, ErrAssetNotFound
	}
	if err != nil {
		return MediaAsset{}, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

// MarkReady flips a pending asset to ready, enforcing the per-variant cap.
//
// The cap needs the service_variants row locked FOR UPDATE: a partial unique
// index can express "at most one primary" but not "at most six rows", so
// concurrent commits would otherwise each count five and each insert a sixth.
// The lock serialises the count-then-write. Non-variant purposes have no row to
// lock and no cap — their uniqueness is already enforced by the partial indexes
// in 003.
//
// Idempotent: committing an already-ready asset returns it unchanged, with
// committed_at untouched.
func (r *MediaRepository) MarkReady(ctx context.Context, tenantID, locationID, assetID uuid.UUID,
	sizeBytes, maxPerVariant int, altText *string) (MediaAsset, error) {

	var out MediaAsset
	err := WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		var a MediaAsset
		err := tx.QueryRow(ctx, `
			SELECT id, service_variant_id, r2_key, status
			FROM media_assets WHERE id = $1 AND tenant_id = $2 AND location_id = $3`,
			assetID, tenantID, locationID).Scan(&a.ID, &a.ServiceVariantID, &a.R2Key, &a.Status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAssetNotFound
		}
		if err != nil {
			return err
		}

		if a.Status == "ready" {
			return r.scanFull(ctx, tx, assetID, &out)
		}

		if a.ServiceVariantID != nil {
			var lockID uuid.UUID
			if err := tx.QueryRow(ctx,
				`SELECT id FROM service_variants WHERE id = $1 FOR UPDATE`,
				*a.ServiceVariantID).Scan(&lockID); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM media_assets
				WHERE service_variant_id = $1 AND purpose = 'service_ref' AND status = 'ready'`,
				*a.ServiceVariantID).Scan(&n); err != nil {
				return err
			}
			if n >= maxPerVariant {
				return ErrVariantFull
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE media_assets
			SET status = 'ready', committed_at = NOW(), bytes = $2,
			    alt_text = COALESCE($3, alt_text)
			WHERE id = $1`, assetID, sizeBytes, altText); err != nil {
			return err
		}
		return r.scanFull(ctx, tx, assetID, &out)
	})
	return out, err
}

func (r *MediaRepository) scanFull(ctx context.Context, tx pgx.Tx, id uuid.UUID, a *MediaAsset) error {
	return tx.QueryRow(ctx, `
		SELECT id, tenant_id, location_id, purpose, service_variant_id, staff_member_id,
		       r2_key, content_hash, bytes, alt_text, sort_order, is_primary, status,
		       created_at, committed_at
		FROM media_assets WHERE id = $1`, id,
	).Scan(&a.ID, &a.TenantID, &a.LocationID, &a.Purpose, &a.ServiceVariantID, &a.StaffMemberID,
		&a.R2Key, &a.ContentHash, &a.Bytes, &a.AltText, &a.SortOrder, &a.IsPrimary, &a.Status,
		&a.CreatedAt, &a.CommittedAt)
}

// ArchiveWithPurge soft-deletes an asset and schedules the R2 object's removal,
// both in ONE transaction (Law 7). The object survives for the grace window so a
// mistaken delete is recoverable; after that the outbox handler removes it.
func (r *MediaRepository) ArchiveWithPurge(ctx context.Context, tenantID, locationID, assetID uuid.UUID,
	grace time.Duration) error {

	return WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		var key string
		err := tx.QueryRow(ctx, `
			UPDATE media_assets
			SET status = 'archived', archived_at = NOW()
			WHERE id = $1 AND tenant_id = $2 AND location_id = $3 AND status <> 'archived'
			RETURNING r2_key`, assetID, tenantID, locationID).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAssetNotFound
		}
		if err != nil {
			return err
		}

		// Law 7: the outbox row lands in the same transaction as the state
		// change. If this rolls back, no purge is scheduled and the asset is
		// still live — the two can never disagree.
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (tenant_id, type, payload, process_after)
			VALUES ($1, 'media.purge', $2, NOW() + $3::interval)`,
			tenantID,
			[]byte(fmt.Sprintf(`{"media_asset_id":%q,"r2_key":%q}`, assetID, key)),
			fmt.Sprintf("%d seconds", int(grace.Seconds())))
		return err
	})
}

// ReapCandidate is a presigned upload the browser never confirmed.
type ReapCandidate struct {
	ID    uuid.UUID
	R2Key string
}

// ReapCandidates lists pending assets older than age, bounded by limit so one
// tick cannot hold the reaper goroutine for minutes on a 1GB droplet. Served by
// idx_media_assets_reap (003:82).
func (r *MediaRepository) ReapCandidates(ctx context.Context, age time.Duration, limit int) ([]ReapCandidate, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, r2_key FROM media_assets
		WHERE status = 'pending' AND created_at < NOW() - $1::interval
		ORDER BY created_at
		LIMIT $2`, fmt.Sprintf("%d seconds", int(age.Seconds())), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReapCandidate
	for rows.Next() {
		var c ReapCandidate
		if err := rows.Scan(&c.ID, &c.R2Key); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteRow removes a reaped row. Called only AFTER the R2 object is gone: the
// row is the only record that the object exists, so deleting it first would
// orphan the object permanently.
func (r *MediaRepository) DeleteRow(ctx context.Context, id uuid.UUID) error {
	_, err := r.Pool.Exec(ctx, `DELETE FROM media_assets WHERE id = $1 AND status = 'pending'`, id)
	return err
}
