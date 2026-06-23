package repository_test

import (
	"context"
	"testing"

	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedCustomStaff(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID, name, role string, isActive bool) uuid.UUID {
	t.Helper()
	var staffID uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`, tenantID, locationID, name, "+91"+uuid.New().String()[:10], role, isActive).Scan(&staffID)
	require.NoError(t, err)
	return staffID
}

func seedQueueSessionAndEntry(t *testing.T, pool *pgxpool.Pool, tenantID, locationID, staffID uuid.UUID, state string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var sessionID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, NOW()::date, 'active')
		ON CONFLICT (location_id, business_date) DO UPDATE SET status = 'active'
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
		INSERT INTO queue_entries (queue_session_id, visit_id, state, token_number, assigned_barber_id)
		VALUES ($1, $2, $3, (SELECT COALESCE(MAX(token_number), 0) + 1 FROM queue_entries WHERE queue_session_id = $1), $4)
		RETURNING id
	`, sessionID, visitID, state, staffID).Scan(&entryID)
	require.NoError(t, err)
	return entryID
}

func TestListStaffMembers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Seed tenant & location A
	tenantA, locationA := seedLocation(t, pool)

	// Seed active & inactive staff in location A
	staffA1 := seedCustomStaff(t, pool, tenantA, locationA, "Active Staff A1", "barber", true)
	_ = seedCustomStaff(t, pool, tenantA, locationA, "Inactive Staff A2", "barber", false)
	staffA3 := seedCustomStaff(t, pool, tenantA, locationA, "Active Staff A3", "barber", true)

	// Seed tenant & location B for tenant isolation verification
	tenantB, locationB := seedLocation(t, pool)
	staffB1 := seedCustomStaff(t, pool, tenantB, locationB, "Active Staff B1", "barber", true)

	// Seed active queue entry for staffA1 (called)
	entryA1 := seedQueueSessionAndEntry(t, pool, tenantA, locationA, staffA1, "called")

	// Seed terminal queue entry for staffA3 (completed) -> shouldn't show as current entry
	_ = seedQueueSessionAndEntry(t, pool, tenantA, locationA, staffA3, "completed")

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM queue_entries")
		_, _ = pool.Exec(ctx, "DELETE FROM visits")
		_, _ = pool.Exec(ctx, "DELETE FROM queue_sessions")
		_, _ = pool.Exec(ctx, "DELETE FROM staff_members")
	})

	// Test case 1: List staff members for Location A
	members, err := repository.ListStaffMembers(ctx, pool, tenantA.String(), locationA.String())
	require.NoError(t, err)

	// Inactive staff A2 should be excluded, and staff B1 from other location should be excluded
	require.Len(t, members, 2)

	// Ordering by created_at ensures staffA1 is first, staffA3 is second
	assert.Equal(t, staffA1.String(), members[0].ID)
	assert.Equal(t, "Active Staff A1", members[0].Name)
	assert.Equal(t, "barber", members[0].Role)
	assert.Equal(t, "offline", members[0].Status)
	require.NotNil(t, members[0].CurrentEntryID)
	assert.Equal(t, entryA1.String(), *members[0].CurrentEntryID)

	assert.Equal(t, staffA3.String(), members[1].ID)
	assert.Equal(t, "Active Staff A3", members[1].Name)
	assert.Nil(t, members[1].CurrentEntryID) // terminal state doesn't count

	// Test case 2: List staff members for Location B
	membersB, err := repository.ListStaffMembers(ctx, pool, tenantB.String(), locationB.String())
	require.NoError(t, err)
	require.Len(t, membersB, 1)
	assert.Equal(t, staffB1.String(), membersB[0].ID)
	assert.Nil(t, membersB[0].CurrentEntryID)

	// Test case 3: Invalid UUID string
	_, err = repository.ListStaffMembers(ctx, pool, "invalid-uuid", locationA.String())
	assert.Error(t, err)
}
