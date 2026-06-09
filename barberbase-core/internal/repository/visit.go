package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type VisitRow struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	LocationID           uuid.UUID
	CustomerID           *uuid.UUID
	EntryType            string
	Status               string
	InitiatedVia         string
	PartySize            int
	TotalDurationMinutes int
	MagicLinkTokenHash   *string
	MagicLinkExpiresAt   *time.Time
	IdempotencyKey       *string
}

type VisitServiceRow struct {
	ServiceVariantID *uuid.UUID
	VariantName      string
	GroupName        string
	CategoryName     string
	DurationMinutes  int
	PricePaise       int
	SortOrder        int
}

func InsertVisit(ctx context.Context, tx pgx.Tx, v *VisitRow) error {
	const query = `
		INSERT INTO visits (
			id, tenant_id, location_id, customer_id, entry_type,
			status, initiated_via, party_size, total_duration_minutes,
			magic_link_token_hash, magic_link_expires_at, idempotency_key,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())`
	_, err := tx.Exec(ctx, query,
		v.ID, v.TenantID, v.LocationID, v.CustomerID, v.EntryType,
		v.Status, v.InitiatedVia, v.PartySize, v.TotalDurationMinutes,
		v.MagicLinkTokenHash, v.MagicLinkExpiresAt, v.IdempotencyKey,
	)
	return err
}

func InsertVisitServices(ctx context.Context, tx pgx.Tx, visitID uuid.UUID, services []VisitServiceRow) error {
	for _, s := range services {
		const query = `
			INSERT INTO visit_services (
				visit_id, service_variant_id,
				variant_name_snapshot, group_name_snapshot, category_name_snapshot,
				duration_minutes_snapshot, price_paise_snapshot, sort_order,
				created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())`
		_, err := tx.Exec(ctx, query,
			visitID, s.ServiceVariantID,
			s.VariantName, s.GroupName, s.CategoryName,
			s.DurationMinutes, s.PricePaise, s.SortOrder,
		)
		if err != nil {
			return err
		}
	}
	return nil
}
