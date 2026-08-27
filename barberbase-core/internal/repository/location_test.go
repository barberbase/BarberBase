package repository_test

import (
	"context"
	"testing"
	"time"

	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedStaff(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID) uuid.UUID {
	t.Helper()
	var staffID uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role)
		VALUES ($1, $2, 'Test Staff', $3, 'manager')
		RETURNING id`, tenantID, locationID, "+91"+uuid.New().String()[:10]).Scan(&staffID)
	require.NoError(t, err)
	return staffID
}

// seedHours inserts a location_hours row for today's DOW in the location's
// timezone (seedLocation leaves the default 'Asia/Kolkata').
func seedHours(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, opensAt, closesAt string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO location_hours (tenant_id, location_id, day_of_week, is_open, opens_at, closes_at)
		VALUES ($1, $2, EXTRACT(DOW FROM (NOW() AT TIME ZONE 'Asia/Kolkata'))::SMALLINT, true, $3, $4)
		ON CONFLICT (location_id, day_of_week)
		DO UPDATE SET is_open = true, opens_at = $3, closes_at = $4
	`, tenantID, locationID, opensAt, closesAt)
	require.NoError(t, err)
}

func TestGetEffectiveShopStatus_ExpiredOverride(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)

	staffID := seedStaff(t, pool, tenantID, locationID)
	seedHours(t, pool, tenantID, locationID, "00:00:00", "23:59:59")

	expiresAt := time.Now().Add(-1 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO location_status_overrides (tenant_id, location_id, status, reason, set_by, starts_at, expires_at)
		VALUES ($1, $2, 'closed', 'Test Expired', $3, NOW() - INTERVAL '2 hours', $4)
	`, tenantID, locationID, staffID, expiresAt)
	require.NoError(t, err)

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)

	assert.Equal(t, "open", res.Status)
	assert.False(t, res.ManualOverrideActive)
	assert.Nil(t, res.OverrideExpiresAt)
}

// Override expires → effective status falls back to scheduled hours:
// open inside today's window, closed outside it, closed with no hours row.
func TestGetEffectiveShopStatus_HoursFallback(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)

	// Expired override must be ignored in every case below.
	_, err := pool.Exec(ctx, `
		INSERT INTO location_status_overrides (tenant_id, location_id, status, set_by, starts_at, expires_at)
		VALUES ($1, $2, 'closed', $3, NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour')
	`, tenantID, locationID, staffID)
	require.NoError(t, err)

	// No hours row at all → closed.
	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "closed", res.Status)
	assert.False(t, res.ManualOverrideActive)

	// Inside today's window → open.
	seedHours(t, pool, tenantID, locationID, "00:00:00", "23:59:59")
	res, err = repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "open", res.Status)

	// Outside today's window → closed. Pick a 1-minute window on the far side
	// of the clock from the current IST time so the test is time-independent.
	ist, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)
	opens, closes := "01:00:00", "01:59:00"
	if time.Now().In(ist).Hour() < 12 {
		opens, closes = "23:58:00", "23:59:00"
	}
	seedHours(t, pool, tenantID, locationID, opens, closes)
	res, err = repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "closed", res.Status)
}

func TestSetShopStatus_TemporarilyClosed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)

	expiresAt := time.Now().Add(30 * time.Minute)
	params := repository.SetShopStatusParams{
		TenantID:   tenantID,
		LocationID: locationID,
		SetBy:      staffID,
		Status:     "temporarily_closed",
		ExpiresAt:  &expiresAt,
	}

	_, err := repository.SetShopStatus(ctx, pool, params)
	require.NoError(t, err)

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "temporarily_closed", res.Status)
	assert.True(t, res.ManualOverrideActive)
	require.NotNil(t, res.OverrideExpiresAt)
	// Compare truncated times due to db serialization
	assert.WithinDuration(t, expiresAt, *res.OverrideExpiresAt, time.Second)
}

func TestSetShopStatus_TemporarilyClosed_Indefinite(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)

	params := repository.SetShopStatusParams{
		TenantID:   tenantID,
		LocationID: locationID,
		SetBy:      staffID,
		Status:     "temporarily_closed",
		ExpiresAt:  nil,
	}

	_, err := repository.SetShopStatus(ctx, pool, params)
	require.NoError(t, err)

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "temporarily_closed", res.Status)
	assert.Nil(t, res.OverrideExpiresAt)
}

func TestSetShopStatus_422Gate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)

	// Create a queue session and an active entry
	var sessionID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
		RETURNING id`, tenantID, locationID).Scan(&sessionID)
	require.NoError(t, err)

	var visitID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 30)
		RETURNING id`, tenantID, locationID).Scan(&visitID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (queue_session_id, visit_id, state, token_number)
		VALUES ($1, $2, 'waiting', 1)
	`, sessionID, visitID)
	require.NoError(t, err)

	params := repository.SetShopStatusParams{
		TenantID:   tenantID,
		LocationID: locationID,
		SetBy:      staffID,
		Status:     "closed",
	}

	count, err := repository.SetShopStatus(ctx, pool, params)
	require.ErrorIs(t, err, repository.ErrActiveEntriesExist)
	assert.Equal(t, 1, count)
}

func TestSetShopStatus_ExpireRemaining(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)

	var sessionID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active')
		RETURNING id`, tenantID, locationID).Scan(&sessionID)
	require.NoError(t, err)

	var visitID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 30)
		RETURNING id`, tenantID, locationID).Scan(&visitID)
	require.NoError(t, err)

	var entryID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO queue_entries (queue_session_id, visit_id, state, token_number)
		VALUES ($1, $2, 'waiting', 1)
		RETURNING id
	`, sessionID, visitID).Scan(&entryID)
	require.NoError(t, err)

	action := "expire_remaining"
	params := repository.SetShopStatusParams{
		TenantID:    tenantID,
		LocationID:  locationID,
		SetBy:       staffID,
		Status:      "closed",
		ModalAction: &action,
	}

	count, err := repository.SetShopStatus(ctx, pool, params)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	var state string
	err = pool.QueryRow(ctx, "SELECT state FROM queue_entries WHERE id = $1", entryID).Scan(&state)
	require.NoError(t, err)
	assert.Equal(t, "expired", state)

	var sStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM queue_sessions WHERE id = $1", sessionID).Scan(&sStatus)
	require.NoError(t, err)
	assert.Equal(t, "closed", sStatus)
}

func TestSetShopStatus_OpenClearsOverrides(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)
	staffID := seedStaff(t, pool, tenantID, locationID)
	seedHours(t, pool, tenantID, locationID, "00:00:00", "23:59:59")

	_, err := pool.Exec(ctx, `
		INSERT INTO location_status_overrides (tenant_id, location_id, status, set_by, starts_at)
		VALUES ($1, $2, 'temporarily_closed', $3, NOW() - INTERVAL '1 hour')
	`, tenantID, locationID, staffID)
	require.NoError(t, err)

	params := repository.SetShopStatusParams{
		TenantID:   tenantID,
		LocationID: locationID,
		SetBy:      staffID,
		Status:     "open",
	}

	_, err = repository.SetShopStatus(ctx, pool, params)
	require.NoError(t, err)

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "open", res.Status)

	// "open" clears overrides and writes nothing: effective status comes from
	// scheduled hours (seeded open above), not from an 'open' override row.
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM location_status_overrides WHERE location_id = $1 AND cleared_at IS NULL", locationID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
