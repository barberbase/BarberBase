package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
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

var ErrActiveEntriesExist = errors.New("active_entries_require_action")

// ShopStatusResult is the shared effective-status type.
type ShopStatusResult struct {
	Status               string     // "open" | "closing_soon" | "temporarily_closed" | "closed"
	ManualOverrideActive bool
	OverrideExpiresAt    *time.Time // nil when no active override or expires_at IS NULL
}

// StaffShopStatus is the full response payload for GET /staff/shop/status.
type StaffShopStatus struct {
	ShopStatus           string     // from ShopStatusResult.Status
	QueueSessionStatus   string     // from queue_sessions.status; "active" when no session today
	ManualOverrideActive bool
	OverrideExpiresAt    *time.Time
	ArrivalPin           *string    // arrival_pin_plain from locations; nil if not set
}

// SetShopStatusParams carries parsed, validated input for the PATCH handler.
type SetShopStatusParams struct {
	TenantID       uuid.UUID
	LocationID     uuid.UUID
	SetBy          uuid.UUID   // staff_member_id from JWT
	Status         string
	ExpiresAt      *time.Time  // nil means manual reopen required (expires_at IS NULL)
	Reason         *string
	ModalAction    *string     // "finish_remaining" | "expire_remaining" | nil
}

func GetEffectiveShopStatus(ctx context.Context, db *pgxpool.Pool, tenantID, locationID uuid.UUID) (ShopStatusResult, error) {
	query := `
		SELECT status, expires_at
		FROM location_status_overrides
		WHERE tenant_id = $1
		  AND location_id = $2
		  AND cleared_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY starts_at DESC
		LIMIT 1
	`
	row := db.QueryRow(ctx, query, tenantID, locationID)
	var o LocationOverrideRow
	err := row.Scan(&o.Status, &o.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ShopStatusResult{Status: "open", ManualOverrideActive: false}, nil
		}
		return ShopStatusResult{}, err
	}
	return ShopStatusResult{
		Status:               o.Status,
		ManualOverrideActive: true,
		OverrideExpiresAt:    o.ExpiresAt,
	}, nil
}

func GetStaffShopStatus(ctx context.Context, db *pgxpool.Pool, tenantID, locationID uuid.UUID) (StaffShopStatus, error) {
	effStatus, err := GetEffectiveShopStatus(ctx, db, tenantID, locationID)
	if err != nil {
		return StaffShopStatus{}, err
	}

	queueQuery := `
		SELECT qs.status
		FROM queue_sessions qs
		JOIN locations l ON l.id = qs.location_id
		WHERE qs.tenant_id = $1
		  AND qs.location_id = $2
		  AND qs.business_date = (NOW() AT TIME ZONE l.timezone)::date
		  AND qs.status != 'archived'
		ORDER BY qs.created_at DESC LIMIT 1
	`
	var qsStatus string
	err = db.QueryRow(ctx, queueQuery, tenantID, locationID).Scan(&qsStatus)
	if err != nil {
		if err == pgx.ErrNoRows {
			qsStatus = "active"
		} else {
			return StaffShopStatus{}, err
		}
	}

	pinQuery := `
		SELECT arrival_pin_plain
		FROM locations
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`
	var arrivalPin *string
	err = db.QueryRow(ctx, pinQuery, locationID, tenantID).Scan(&arrivalPin)
	if err != nil {
		return StaffShopStatus{}, err
	}

	return StaffShopStatus{
		ShopStatus:           effStatus.Status,
		QueueSessionStatus:   qsStatus,
		ManualOverrideActive: effStatus.ManualOverrideActive,
		OverrideExpiresAt:    effStatus.OverrideExpiresAt,
		ArrivalPin:           arrivalPin,
	}, nil
}

func SetShopStatus(ctx context.Context, db *pgxpool.Pool, params SetShopStatusParams) (int, error) {
	var count int
	err := WithTx(ctx, db, func(tx pgx.Tx) error {
		countQuery := `
			SELECT COUNT(*)
			FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qs.tenant_id = $1
			  AND qs.location_id = $2
			  AND qs.business_date = (NOW() AT TIME ZONE (
				  SELECT timezone FROM locations WHERE id = $2
			  ))::date
			  AND qs.status NOT IN ('closed','archived')
			  AND qe.state IN ('waiting','called','in_progress')
		`
		err := tx.QueryRow(ctx, countQuery, params.TenantID, params.LocationID).Scan(&count)
		if err != nil {
			return err
		}

		if params.Status == "closed" && count > 0 && params.ModalAction == nil {
			return ErrActiveEntriesExist
		}

		lockQuery := `
			SELECT qs.id FROM queue_sessions qs
			JOIN locations l ON l.id = qs.location_id
			WHERE qs.tenant_id = $1 AND qs.location_id = $2
			  AND qs.business_date = (NOW() AT TIME ZONE l.timezone)::date
			  AND qs.status != 'archived'
			FOR UPDATE
		`
		var sessionID uuid.UUID
		sessionExists := true
		err = tx.QueryRow(ctx, lockQuery, params.TenantID, params.LocationID).Scan(&sessionID)
		if err != nil {
			if err == pgx.ErrNoRows {
				sessionExists = false
			} else {
				return err
			}
		}

		var newQsStatus string
		if params.Status == "open" {
			newQsStatus = "active"
		} else if params.Status == "temporarily_closed" {
			newQsStatus = "paused"
		} else if params.Status == "closing_soon" {
			newQsStatus = "ending"
		} else if params.Status == "closed" {
			if params.ModalAction != nil && *params.ModalAction == "finish_remaining" {
				newQsStatus = "ending"
			} else if params.ModalAction != nil && *params.ModalAction == "expire_remaining" {
				newQsStatus = "closed"
			} else if params.ModalAction == nil && count == 0 {
				newQsStatus = "closed"
			}
		}

		if params.Status == "open" {
			clearQuery := `
				UPDATE location_status_overrides
				SET cleared_at = NOW()
				WHERE tenant_id = $1 AND location_id = $2 AND cleared_at IS NULL
			`
			_, err = tx.Exec(ctx, clearQuery, params.TenantID, params.LocationID)
			if err != nil {
				return err
			}
		} else {
			insertQuery := `
				INSERT INTO location_status_overrides
					(tenant_id, location_id, status, reason, set_by, starts_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, NOW(), $6)
			`
			_, err = tx.Exec(ctx, insertQuery, params.TenantID, params.LocationID, params.Status, params.Reason, params.SetBy, params.ExpiresAt)
			if err != nil {
				return err
			}
		}

		if sessionExists {
			updateSessionQuery := `
				UPDATE queue_sessions
				SET status = $3, queue_version = queue_version + 1
				WHERE tenant_id = $1 AND location_id = $2
				  AND business_date = (NOW() AT TIME ZONE (
					  SELECT timezone FROM locations WHERE id = $2
				  ))::date
				  AND status != 'archived'
			`
			_, err = tx.Exec(ctx, updateSessionQuery, params.TenantID, params.LocationID, newQsStatus)
			if err != nil {
				return err
			}
			
			if params.Status == "closed" && params.ModalAction != nil && *params.ModalAction == "expire_remaining" {
				expireEntriesQuery := `
					UPDATE queue_entries
					SET state = 'expired'
					WHERE queue_session_id = (
						SELECT id FROM queue_sessions
						WHERE tenant_id = $1 AND location_id = $2
						  AND business_date = (NOW() AT TIME ZONE (
							  SELECT timezone FROM locations WHERE id = $2
						  ))::date
						LIMIT 1
					)
					AND state IN ('waiting', 'called', 'skipped')
				`
				_, err = tx.Exec(ctx, expireEntriesQuery, params.TenantID, params.LocationID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})

	if err != nil {
		if err == ErrActiveEntriesExist {
			return count, err
		}
		return 0, err
	}

	return 0, nil
}
