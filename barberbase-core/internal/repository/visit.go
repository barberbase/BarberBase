// package repository contains methods for interacting with visits and other entities
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

type CheckoutEntry struct {
	EntryID          uuid.UUID
	VisitID          uuid.UUID
	CustomerID       *uuid.UUID
	AssignedBarberID *uuid.UUID
	State            string
}

type CheckoutServiceRow struct {
	ServiceVariantID    *uuid.UUID
	VariantNameSnapshot string
	UnitAmountPaise     int
}

type CheckoutProductRow struct {
	ID         uuid.UUID
	Name       string
	PricePaise int
}

func LockSessionForCheckout(ctx context.Context, tx pgx.Tx, locationID uuid.UUID, businessDate string) (uuid.UUID, int, error) {
	var id uuid.UUID
	var version int
	err := tx.QueryRow(ctx, `
		SELECT id, queue_version
		FROM queue_sessions
		WHERE location_id = $1 AND business_date = $2
		FOR UPDATE`, locationID, businessDate).Scan(&id, &version)
	return id, version, err
}

func LockEntryForCheckout(ctx context.Context, tx pgx.Tx, entryID, tenantID uuid.UUID) (*CheckoutEntry, error) {
	var e CheckoutEntry
	err := tx.QueryRow(ctx, `
		SELECT q.id, q.visit_id, q.customer_id, q.assigned_barber_id, q.state
		FROM queue_entries q
		JOIN visits v ON q.visit_id = v.id
		WHERE q.id = $1 AND v.tenant_id = $2
		FOR UPDATE OF q`, entryID, tenantID).Scan(&e.EntryID, &e.VisitID, &e.CustomerID, &e.AssignedBarberID, &e.State)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func LockVisitForCheckout(ctx context.Context, tx pgx.Tx, visitID, tenantID uuid.UUID) error {
	var dummy uuid.UUID
	return tx.QueryRow(ctx, `
		SELECT id
		FROM visits
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE`, visitID, tenantID).Scan(&dummy)
}

func GetVisitServicesForCheckout(ctx context.Context, tx pgx.Tx, visitID uuid.UUID) ([]CheckoutServiceRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT service_variant_id, variant_name_snapshot, price_paise_snapshot
		FROM visit_services
		WHERE visit_id = $1
		ORDER BY sort_order ASC`, visitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []CheckoutServiceRow
	for rows.Next() {
		var s CheckoutServiceRow
		if err := rows.Scan(&s.ServiceVariantID, &s.VariantNameSnapshot, &s.UnitAmountPaise); err != nil {
			return nil, err
		}
		res = append(res, s)
	}
	return res, rows.Err()
}

func GetProductsForCheckout(ctx context.Context, tx pgx.Tx, productIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID]CheckoutProductRow, error) {
	m := make(map[uuid.UUID]CheckoutProductRow)
	if len(productIDs) == 0 {
		return m, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, name, price_paise
		FROM products
		WHERE id = ANY($1) AND tenant_id = $2 AND is_active = true`, productIDs, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p CheckoutProductRow
		if err := rows.Scan(&p.ID, &p.Name, &p.PricePaise); err != nil {
			return nil, err
		}
		m[p.ID] = p
	}
	return m, rows.Err()
}

func InsertVisitCharge(ctx context.Context, tx pgx.Tx, tenantID, locationID, visitID uuid.UUID, subtotal, discount, total int, discountReason *string, staffID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO visit_charges (
			tenant_id, location_id, visit_id,
			subtotal_amount_paise, discount_amount_paise, total_amount_paise,
			discount_reason, status, finalized_at, finalized_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, 'finalized', NOW(), $8
		) RETURNING id`,
		tenantID, locationID, visitID, subtotal, discount, total, discountReason, staffID).Scan(&id)
	return id, err
}

func InsertVisitChargeLineItems(ctx context.Context, tx pgx.Tx, tenantID, chargeID uuid.UUID, rows [][]any) error {
	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"visit_charge_line_items"},
		[]string{"tenant_id", "visit_charge_id", "line_type", "service_variant_id", "product_id", "name_snapshot", "quantity", "unit_amount_paise", "total_amount_paise", "staff_member_id"},
		pgx.CopyFromRows(rows),
	)
	return err
}

func InsertVisitPayments(ctx context.Context, tx pgx.Tx, tenantID, locationID, chargeID uuid.UUID, staffID uuid.UUID, payments [][]any) error {
	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"visit_payments"},
		[]string{"tenant_id", "location_id", "visit_charge_id", "method", "amount_paise", "provider_reference_id", "collected_by", "collected_at"},
		pgx.CopyFromRows(payments),
	)
	return err
}

func MarkEntryCompleted(ctx context.Context, tx pgx.Tx, entryID, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE queue_entries SET state = 'completed', is_dispatchable = false, completed_at = NOW()
		WHERE id = $1`, entryID)
	return err
}

func MarkVisitCompleted(ctx context.Context, tx pgx.Tx, visitID, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE visits
		SET status = 'completed', completed_at = NOW()
		WHERE id = $1 AND tenant_id = $2`, visitID, tenantID)
	return err
}

func UpdateCustomerMetrics(ctx context.Context, tx pgx.Tx, customerID, tenantID uuid.UUID, totalPaise int) error {
	_, err := tx.Exec(ctx, `
		UPDATE customers
		SET last_visit_at = NOW(),
		    visit_count = visit_count + 1,
		    lifetime_value_paise = lifetime_value_paise + $1
		WHERE id = $2 AND tenant_id = $3`, totalPaise, customerID, tenantID)
	return err
}

func UpdateStaffIdle(ctx context.Context, tx pgx.Tx, staffID, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE staff_members
		SET status = 'idle'
		WHERE id = $1 AND tenant_id = $2`, staffID, tenantID)
	return err
}

func InsertFeedbackOutboxEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, payloadBytes []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (tenant_id, type, payload, status, process_after)
		VALUES ($1, 'feedback_request.schedule', $2, 'pending', NOW() + INTERVAL '30 minutes')`,
		tenantID, payloadBytes)
	return err
}

func IncrementQueueVersion(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (int, error) {
	var version int
	err := tx.QueryRow(ctx, `
		UPDATE queue_sessions
		SET queue_version = queue_version + 1
		WHERE id = $1
		RETURNING queue_version`, sessionID).Scan(&version)
	return version, err
}
