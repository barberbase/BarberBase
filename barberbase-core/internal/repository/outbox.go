package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	Pool *pgxpool.Pool
}

type QuotaCheckResult struct {
	PeriodID      uuid.UUID
	UsedCount     int
	IncludedLimit int
}

// UpsertAndLockQuotaPeriod ensures that the quota period row exists for the current calendar month
// and then locks the row for update, returning the state.
func (r *Repository) UpsertAndLockQuotaPeriod(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, quotaType string) (QuotaCheckResult, error) {
	upsertQuery := `
INSERT INTO tenant_quota_periods
  (tenant_id, quota_type, period_start, period_end, included_limit)
SELECT $1, $2::text,
       date_trunc('month', NOW())::DATE,
       (date_trunc('month', NOW()) + INTERVAL '1 month' - INTERVAL '1 day')::DATE,
       CASE $2::text
         WHEN 'whatsapp_marketing'     THEN t.monthly_marketing_quota
         WHEN 'whatsapp_transactional' THEN t.monthly_transactional_quota
         ELSE 0
       END
FROM tenants t
WHERE t.id = $1 AND t.is_active = true
ON CONFLICT (tenant_id, quota_type, period_start) DO NOTHING;`

	if _, err := tx.Exec(ctx, upsertQuery, tenantID, quotaType); err != nil {
		return QuotaCheckResult{}, err
	}

	lockQuery := `
SELECT id, used_count, included_limit
FROM tenant_quota_periods
WHERE tenant_id      = $1
  AND quota_type     = $2
  AND period_start   = date_trunc('month', NOW())::DATE
FOR UPDATE;`

	var res QuotaCheckResult
	err := tx.QueryRow(ctx, lockQuery, tenantID, quotaType).Scan(&res.PeriodID, &res.UsedCount, &res.IncludedLimit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QuotaCheckResult{}, fmt.Errorf("quota period not found for tenant %s", tenantID)
		}
		return QuotaCheckResult{}, err
	}

	return res, nil
}

// InsertQuotaLedgerIdempotent inserts a ledger entry for the quota usage, returning true if inserted.
func (r *Repository) InsertQuotaLedgerIdempotent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, quotaType string, periodID uuid.UUID, outboxEventID uuid.UUID) (inserted bool, err error) {
	insertQuery := `
INSERT INTO quota_usage_ledger
  (tenant_id, quota_type, quota_period_id, usage_count, source_type, source_id, idempotency_key)
VALUES ($1, $2, $3, 1, 'outbox_event', $4, $5)
ON CONFLICT (idempotency_key) DO NOTHING;`

	tag, err := tx.Exec(ctx, insertQuery, tenantID, quotaType, periodID, outboxEventID, outboxEventID.String())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// IncrementQuotaPeriodUsed increments the used count for the quota period.
func (r *Repository) IncrementQuotaPeriodUsed(ctx context.Context, tx pgx.Tx, periodID uuid.UUID) error {
	updateQuery := `
UPDATE tenant_quota_periods
SET used_count = used_count + 1
WHERE id = $1;`

	_, err := tx.Exec(ctx, updateQuery, periodID)
	return err
}
