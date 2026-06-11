package repository

import (
	"context"
	"errors"

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
