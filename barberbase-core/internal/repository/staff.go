package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPhoneAlreadyExists is returned when a staff member with the same phone number already exists.
var ErrPhoneAlreadyExists = errors.New("phone_already_exists")

// InsertStaffMemberParams holds the parameters for inserting a new staff member.
type InsertStaffMemberParams struct {
	TenantID    uuid.UUID
	LocationID  uuid.UUID
	Name        string
	PhoneNumber string // E.164
	Role        string // "manager" or "barber"
}

// InsertStaffMember inserts a new staff member into the database.
func InsertStaffMember(ctx context.Context, pool *pgxpool.Pool, p InsertStaffMemberParams) (uuid.UUID, error) {
	var id uuid.UUID
	query := `INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role)
VALUES ($1, $2, $3, $4, $5)
RETURNING id`

	err := pool.QueryRow(ctx, query, p.TenantID, p.LocationID, p.Name, p.PhoneNumber, p.Role).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" && pgErr.ConstraintName == "staff_members_phone_number_key" {
				return uuid.Nil, ErrPhoneAlreadyExists
			}
		}
		return uuid.Nil, err
	}

	return id, nil
}

// StaffMemberRow holds selected staff member details.
type StaffMemberRow struct {
	ID             string
	Name           string
	Role           string
	Status         string
	CurrentEntryID *string // nullable UUID string, nil if no active entry
}

// ListStaffMembers retrieves active staff members for a given tenant and location,
// along with any active queue entry currently assigned to them.
func ListStaffMembers(ctx context.Context, pool *pgxpool.Pool, tenantID, locationID string) ([]StaffMemberRow, error) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID: %w", err)
	}
	locationUUID, err := uuid.Parse(locationID)
	if err != nil {
		return nil, fmt.Errorf("invalid location ID: %w", err)
	}

	query := `
		SELECT
			sm.id::text,
			sm.name,
			sm.role,
			sm.status,
			qe.id::text AS current_entry_id
		FROM staff_members sm
		LEFT JOIN queue_entries qe
			ON qe.assigned_barber_id = sm.id
			AND qe.state IN ('called', 'in_progress')
		WHERE sm.tenant_id = $1
		  AND sm.location_id = $2
		  AND sm.is_active = true
		ORDER BY sm.created_at ASC
	`

	var members []StaffMemberRow = []StaffMemberRow{}
	rows, err := pool.Query(ctx, query, tenantUUID, locationUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var r StaffMemberRow
		err := rows.Scan(&r.ID, &r.Name, &r.Role, &r.Status, &r.CurrentEntryID)
		if err != nil {
			return nil, err
		}
		members = append(members, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return members, nil
}

