package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		  AND starts_at <= NOW()
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY starts_at DESC
		LIMIT 1
	`
	row := db.QueryRow(ctx, query, tenantID, locationID)
	var o LocationOverrideRow
	err := row.Scan(&o.Status, &o.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ShopStatusResult{Status: "closed", ManualOverrideActive: false}, nil
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

		// Open with no explicit expiry gets a midnight ceiling so a forgotten tap
		// doesn't show "open" all night. EOD doesn't touch overrides and doesn't
		// run for no-hours tenants, so this is the only backstop in Phase 1.
		if params.Status == "open" && params.ExpiresAt == nil {
			var tz string
			if err = tx.QueryRow(ctx, "SELECT timezone FROM locations WHERE id = $1", params.LocationID).Scan(&tz); err != nil {
				return err
			}
			loc, _ := time.LoadLocation(tz)
			if loc == nil {
				loc = time.UTC
			}
			now := time.Now().In(loc)
			midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
			params.ExpiresAt = &midnight
		}

		// Always clear stale overrides first, then write the new state.
		// "open" is also written as an explicit override so the public endpoint
		// (override > hours) has something to read without requiring location_hours.
		clearQuery := `
			UPDATE location_status_overrides
			SET cleared_at = NOW()
			WHERE tenant_id = $1 AND location_id = $2 AND cleared_at IS NULL
		`
		_, err = tx.Exec(ctx, clearQuery, params.TenantID, params.LocationID)
		if err != nil {
			return err
		}
		insertQuery := `
			INSERT INTO location_status_overrides
				(tenant_id, location_id, status, reason, set_by, starts_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, NOW(), $6)
		`
		_, err = tx.Exec(ctx, insertQuery, params.TenantID, params.LocationID, params.Status, params.Reason, params.SetBy, params.ExpiresAt)
		if err != nil {
			return err
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

var (
	ErrTenantSlugConflict   = errors.New("tenant_slug_conflict")
	ErrLocationSlugConflict = errors.New("location_slug_conflict")
	ErrOwnerPhoneConflict   = errors.New("owner_phone_conflict")
	ErrNotFound             = pgx.ErrNoRows
)

type ProvisionTenantParams struct {
	TenantName      string
	TenantSlug      string
	OwnerName       string
	OwnerPhone      string
	LocationName    string
	LocationSlug    string
	Address         *string
	Timezone        string
	ArrivalPinPlain string
	ArrivalPinHash  string
}

type ProvisionTenantResult struct {
	TenantID           uuid.UUID
	LocationID         uuid.UUID
	OwnerStaffMemberID uuid.UUID
}

func ProvisionTenant(ctx context.Context, pool *pgxpool.Pool, p ProvisionTenantParams) (ProvisionTenantResult, error) {
	var res ProvisionTenantResult

	err := WithTx(ctx, pool, func(tx pgx.Tx) error {
		// 1. Insert tenant
		tenantQuery := `
			INSERT INTO tenants (name, slug, owner_phone_number)
			VALUES ($1, $2, $3)
			RETURNING id
		`
		err := tx.QueryRow(ctx, tenantQuery, p.TenantName, p.TenantSlug, p.OwnerPhone).Scan(&res.TenantID)
		if err != nil {
			return err
		}

		// 2. Insert location
		locationQuery := `
			INSERT INTO locations (tenant_id, name, slug, address, timezone, arrival_pin_plain, arrival_pin_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`
		err = tx.QueryRow(ctx, locationQuery, res.TenantID, p.LocationName, p.LocationSlug, p.Address, p.Timezone, p.ArrivalPinPlain, p.ArrivalPinHash).Scan(&res.LocationID)
		if err != nil {
			return err
		}

		// 3. Insert owner staff member
		staffQuery := `
			INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, is_active)
			VALUES ($1, $2, $3, $4, 'owner', true)
			RETURNING id
		`
		err = tx.QueryRow(ctx, staffQuery, res.TenantID, res.LocationID, p.OwnerName, p.OwnerPhone).Scan(&res.OwnerStaffMemberID)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				if pgErr.ConstraintName == "tenants_slug_key" {
					return ProvisionTenantResult{}, ErrTenantSlugConflict
				}
				if pgErr.ConstraintName == "locations_slug_key" {
					return ProvisionTenantResult{}, ErrLocationSlugConflict
				}
				if pgErr.ConstraintName == "staff_members_phone_number_key" {
					return ProvisionTenantResult{}, ErrOwnerPhoneConflict
				}
			}
		}
		return ProvisionTenantResult{}, err
	}

	return res, nil
}

// ConnectModeBWhatsApp atomically updates the location's Mode B columns.
// Verifies location belongs to tenantID before updating (multi-tenant safety).
func ConnectModeBWhatsApp(
	ctx context.Context,
	pool *pgxpool.Pool,
	locationID uuid.UUID,
	tenantID   uuid.UUID,
	phone      string,
	encryptedAPIKey      string,
	encryptedWebhookSecret string,
) error {
	query := `
		UPDATE locations SET
			whatsapp_mode                   = 'own_number',
			business_whatsapp_number        = $3,
			bhejna_api_key_encrypted        = $4,
			bhejna_webhook_secret_encrypted = $5,
			updated_at                      = NOW()
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`
	cmdTag, err := pool.Exec(ctx, query, locationID, tenantID, phone, encryptedAPIKey, encryptedWebhookSecret)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DisconnectModeBWhatsApp reverts the location to shared platform mode.
func DisconnectModeBWhatsApp(
	ctx context.Context,
	pool *pgxpool.Pool,
	locationID uuid.UUID,
	tenantID   uuid.UUID,
) error {
	query := `
		UPDATE locations SET
			whatsapp_mode                   = 'shared',
			business_whatsapp_number        = NULL,
			bhejna_api_key_encrypted        = NULL,
			bhejna_webhook_secret_encrypted = NULL,
			updated_at                      = NOW()
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`
	cmdTag, err := pool.Exec(ctx, query, locationID, tenantID)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LocationModeBWebhookConfig fetches only what the Mode B webhook handler needs.
type LocationModeBWebhookConfig struct {
	TenantID                     uuid.UUID
	BhejnaWebhookSecretEncrypted string
}

// GetLocationForModeBWebhook fetches only what the Mode B webhook handler needs.
// NO tenant filter — this is called from a path that has no JWT.
// Returns error (NotFound variant) if:
//   - location does not exist
//   - is_active = false
//   - whatsapp_mode != 'own_number'
//   - bhejna_webhook_secret_encrypted IS NULL
func GetLocationForModeBWebhook(
	ctx  context.Context,
	pool *pgxpool.Pool,
	locationID uuid.UUID,
) (LocationModeBWebhookConfig, error) {
	query := `
		SELECT tenant_id, bhejna_webhook_secret_encrypted
		FROM locations
		WHERE id = $1
		  AND is_active = true
		  AND whatsapp_mode = 'own_number'
		  AND bhejna_webhook_secret_encrypted IS NOT NULL
	`
	var cfg LocationModeBWebhookConfig
	err := pool.QueryRow(ctx, query, locationID).Scan(&cfg.TenantID, &cfg.BhejnaWebhookSecretEncrypted)
	if err != nil {
		if err == pgx.ErrNoRows {
			return LocationModeBWebhookConfig{}, ErrNotFound
		}
		return LocationModeBWebhookConfig{}, err
	}
	return cfg, nil
}

