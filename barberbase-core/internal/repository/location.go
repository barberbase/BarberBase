package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LocationRow struct {
	ID                   string    // UUID
	TenantID             string    // UUID
	Name                 string
	Slug                 string
	Timezone             string
	OperationMode        string
	MaxTotalQueueSize    int
	AllowOvertimeMinutes int
	ServiceDisplayMode   string
	WhatsAppMode         string
	BusinessWhatsAppNumber *string // nullable
	IsActive             bool
}

type TenantSlugRow struct {
	TenantSlug string
}

type LocationWithTenantSlug struct {
	LocationRow
	TenantSlug string
}

type LocationHoursRow struct {
	IsOpen   bool
	OpensAt  *time.Time  // nil if is_open=false
	ClosesAt *time.Time
}

type LocationOverrideRow struct {
	Status    string
	ExpiresAt *time.Time // nil = manual reopen required
}

type QueueStats struct {
	SessionStatus        string // 'active'|'paused'|'ending'|'closed'|"" if no session today
	QueueLength          int    // waiting + called entries
	EstimatedWaitMinutes int    // SUM(total_duration_minutes) for waiting+called+in_progress
	SessionExists        bool
}

// GetLocationBySlug resolves a location by its compound slug.
// slug is the decoded slug e.g. "star-salon/koramangala".
// Returns nil, nil if not found (caller returns 404).
func GetLocationBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*LocationRow, error) {
	query := `
		SELECT id, tenant_id, name, slug, timezone, operation_mode,
		       max_total_queue_size, allow_overtime_minutes, service_display_mode,
		       whatsapp_mode, business_whatsapp_number, is_active
		FROM locations
		WHERE slug = $1 AND is_active = true
	`
	row := pool.QueryRow(ctx, query, slug)
	var l LocationRow
	err := row.Scan(
		&l.ID, &l.TenantID, &l.Name, &l.Slug, &l.Timezone, &l.OperationMode,
		&l.MaxTotalQueueSize, &l.AllowOvertimeMinutes, &l.ServiceDisplayMode,
		&l.WhatsAppMode, &l.BusinessWhatsAppNumber, &l.IsActive,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetLocationByID resolves a location by UUID.
// Returns nil, nil if not found.
func GetLocationByID(ctx context.Context, pool *pgxpool.Pool, locationID string) (*LocationRow, error) {
	query := `
		SELECT id, tenant_id, name, slug, timezone, operation_mode,
		       max_total_queue_size, allow_overtime_minutes, service_display_mode,
		       whatsapp_mode, business_whatsapp_number, is_active
		FROM locations
		WHERE id = $1::UUID AND is_active = true
	`
	row := pool.QueryRow(ctx, query, locationID)
	var l LocationRow
	err := row.Scan(
		&l.ID, &l.TenantID, &l.Name, &l.Slug, &l.Timezone, &l.OperationMode,
		&l.MaxTotalQueueSize, &l.AllowOvertimeMinutes, &l.ServiceDisplayMode,
		&l.WhatsAppMode, &l.BusinessWhatsAppNumber, &l.IsActive,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetLocationWithTenantSlug resolves location + tenant slug in one query.
// Used by createCheckinIntent to build the deep_link.
func GetLocationWithTenantSlug(ctx context.Context, pool *pgxpool.Pool, locationID string) (*LocationWithTenantSlug, error) {
	query := `
		SELECT l.id, l.tenant_id, l.name, l.slug, l.timezone, l.operation_mode,
		       l.max_total_queue_size, l.allow_overtime_minutes, l.service_display_mode,
		       l.whatsapp_mode, l.business_whatsapp_number, l.is_active,
		       t.slug AS tenant_slug
		FROM locations l
		JOIN tenants t ON t.id = l.tenant_id
		WHERE l.id = $1::UUID AND l.is_active = true AND t.is_active = true
	`
	row := pool.QueryRow(ctx, query, locationID)
	var l LocationWithTenantSlug
	err := row.Scan(
		&l.ID, &l.TenantID, &l.Name, &l.Slug, &l.Timezone, &l.OperationMode,
		&l.MaxTotalQueueSize, &l.AllowOvertimeMinutes, &l.ServiceDisplayMode,
		&l.WhatsAppMode, &l.BusinessWhatsAppNumber, &l.IsActive,
		&l.TenantSlug,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetLocationHoursForDay returns hours for a specific day_of_week (0=Sun..6=Sat).
// Returns nil, nil if no row for that day (treat as closed).
func GetLocationHoursForDay(ctx context.Context, pool *pgxpool.Pool, locationID string, dayOfWeek int) (*LocationHoursRow, error) {
	query := `
		SELECT is_open, opens_at, closes_at
		FROM location_hours
		WHERE location_id = $1::UUID AND day_of_week = $2
	`
	row := pool.QueryRow(ctx, query, locationID, dayOfWeek)
	var h LocationHoursRow
	err := row.Scan(&h.IsOpen, &h.OpensAt, &h.ClosesAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

// GetActiveLocationOverride returns the currently active manual override, if any.
// Returns nil, nil if no active override.
func GetActiveLocationOverride(ctx context.Context, pool *pgxpool.Pool, locationID string) (*LocationOverrideRow, error) {
	query := `
		SELECT status, expires_at
		FROM location_status_overrides
		WHERE location_id = $1::UUID
		  AND cleared_at IS NULL
		  AND starts_at <= NOW()
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY starts_at DESC
		LIMIT 1
	`
	row := pool.QueryRow(ctx, query, locationID)
	var o LocationOverrideRow
	err := row.Scan(&o.Status, &o.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

// GetQueueStats returns live queue metrics for today's session.
// businessDate is the local date string "YYYY-MM-DD" in the location's timezone.
// If no session exists today, returns QueueStats{SessionExists: false}.
func GetQueueStats(ctx context.Context, pool *pgxpool.Pool, locationID string, businessDate string) (*QueueStats, error) {
	query := `
		SELECT qs.status,
		       COUNT(qe.id) FILTER (WHERE qe.state IN ('waiting','called')) AS queue_length,
		       COALESCE(SUM(v.total_duration_minutes)
		                FILTER (WHERE qe.state IN ('waiting','called','in_progress')), 0)
		                AS estimated_wait_minutes
		FROM queue_sessions qs
		LEFT JOIN queue_entries qe ON qe.queue_session_id = qs.id
		LEFT JOIN visits v ON v.id = qe.visit_id
		WHERE qs.location_id = $1::UUID AND qs.business_date = $2::DATE
		GROUP BY qs.id, qs.status
	`
	row := pool.QueryRow(ctx, query, locationID, businessDate)
	var s QueueStats
	err := row.Scan(&s.SessionStatus, &s.QueueLength, &s.EstimatedWaitMinutes)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &QueueStats{SessionExists: false}, nil
		}
		return nil, err
	}
	s.SessionExists = true
	return &s, nil
}
