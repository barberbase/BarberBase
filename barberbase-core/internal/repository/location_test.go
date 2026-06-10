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

func TestGetEffectiveShopStatus_ExpiredOverride(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantID, locationID := seedLocation(t, pool)

	staffID := seedStaff(t, pool, tenantID, locationID)

	expiresAt := time.Now().Add(-1 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO location_status_overrides (tenant_id, location_id, status, reason, set_by, starts_at, expires_at)
		VALUES ($1, $2, 'closed', 'Test Expired', $3, NOW() - INTERVAL '2 hours', $4)
	`, tenantID, locationID, staffID, expiresAt)
	require.NoError(t, err)

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID)
	require.NoError(t, err)

	assert.Equal(t, "open", res.Status)
	assert.False(t, res.ManualOverrideActive)
	assert.Nil(t, res.OverrideExpiresAt)
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

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID)
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

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID)
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
		VALUES ($1, $2, NOW()::date, 'active')
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
		VALUES ($1, $2, NOW()::date, 'active')
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

	res, err := repository.GetEffectiveShopStatus(ctx, pool, tenantID, locationID)
	require.NoError(t, err)
	assert.Equal(t, "open", res.Status)

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM location_status_overrides WHERE location_id = $1 AND cleared_at IS NULL", locationID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
