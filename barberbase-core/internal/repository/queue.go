package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueueSession mirrors the columns returned by EnsureAndLockQueueSession.
// All queue mutations receive a *QueueSession as the serialization anchor.
type QueueSession struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	LocationID      uuid.UUID
	BusinessDate    time.Time  // DATE → UTC midnight; IST conversion is caller's concern
	Status          string
	LastTokenNumber int
	QueueVersion    int
	OpenedAt        time.Time
	ClosedAt        *time.Time
	ArchivedAt      *time.Time
	CreatedAt       time.Time
}

type QueueEntryRow struct {
	ID                uuid.UUID
	VisitID           uuid.UUID
	QueueSessionID    uuid.UUID
	CustomerID        *uuid.UUID
	TokenNumber       int
	State             string
	PresenceState     string
	SessionChannel    string
	IsDispatchable    bool
	RequestedBarberID *uuid.UUID
	PriorityGroup     int
	SortKey           int64
	RemoteJoinedAt    time.Time
}

// EnsureAndLockQueueSession is the mandatory first call inside every queue mutation tx.
//
// Ordering (05_queue_locking_transactions.md §Queue Session Auto-Create):
//
//  1. INSERT ON CONFLICT DO NOTHING — idempotent.
//     All N concurrent first-joiners issue this INSERT. PostgreSQL's
//     UNIQUE(location_id, business_date) allows exactly one to succeed;
//     the rest silently do nothing. The row is now guaranteed to exist.
//
//  2. SELECT FOR UPDATE — blocking serialization lock.
//     All N callers then compete for this lock and serialize.
//     lock_timeout=1s (set per-session in C0.2); timeout returns a
//     retriable error — never a silent failure.
//
// Invariant: never call SELECT FOR UPDATE before the INSERT; on a fresh date
// the row does not exist and FOR UPDATE would lock zero rows, leaving
// last_token_number exposed to a concurrent-write race.
//
// tx must be an active pgx.Tx. Caller owns COMMIT/ROLLBACK.
func EnsureAndLockQueueSession(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, locationID uuid.UUID,
	businessDate time.Time,
) (*QueueSession, error) {
	// Step 1: idempotent upsert — ensures the row exists.
	const upsert = `
		INSERT INTO queue_sessions (
			tenant_id, location_id, business_date,
			status, queue_version, last_token_number
		) VALUES ($1, $2, $3, 'active', 0, 0)
		ON CONFLICT (location_id, business_date) DO NOTHING`

	if _, err := tx.Exec(ctx, upsert, tenantID, locationID, businessDate); err != nil {
		return nil, fmt.Errorf("queue_sessions upsert: %w", err)
	}

	// Step 2: blocking serialization lock — row is now guaranteed present.
	const lockSelect = `
		SELECT id, tenant_id, location_id, business_date,
		       status, last_token_number, queue_version,
		       opened_at, closed_at, archived_at, created_at
		FROM queue_sessions
		WHERE location_id = $1 AND business_date = $2
		FOR UPDATE`

	var s QueueSession
	err := tx.QueryRow(ctx, lockSelect, locationID, businessDate).Scan(
		&s.ID, &s.TenantID, &s.LocationID, &s.BusinessDate,
		&s.Status, &s.LastTokenNumber, &s.QueueVersion,
		&s.OpenedAt, &s.ClosedAt, &s.ArchivedAt, &s.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("queue_sessions lock: %w", err)
	}
	return &s, nil
}

func InsertQueueEntry(ctx context.Context, tx pgx.Tx, e *QueueEntryRow) error {
	const query = `
		INSERT INTO queue_entries (
			id, visit_id, queue_session_id, customer_id,
			token_number, state, presence_state, is_dispatchable,
			requested_barber_id, priority_group, sort_key,
			session_channel, remote_joined_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	_, err := tx.Exec(ctx, query,
		e.ID, e.VisitID, e.QueueSessionID, e.CustomerID,
		e.TokenNumber, e.State, e.PresenceState, e.IsDispatchable,
		e.RequestedBarberID, e.PriorityGroup, e.SortKey,
		e.SessionChannel, e.RemoteJoinedAt,
	)
	return err
}

func GetQueueEntryByCustomer(ctx context.Context, tx pgx.Tx, sessionID, customerID uuid.UUID) (*QueueEntryRow, error) {
	const query = `
		SELECT id, visit_id, queue_session_id, customer_id,
		       token_number, state, presence_state, session_channel,
		       is_dispatchable, requested_barber_id, priority_group,
		       sort_key, remote_joined_at
		FROM queue_entries
		WHERE queue_session_id = $1 AND customer_id = $2
		  AND state IN ('waiting', 'called', 'in_progress')
		LIMIT 1`
	var e QueueEntryRow
	err := tx.QueryRow(ctx, query, sessionID, customerID).Scan(
		&e.ID, &e.VisitID, &e.QueueSessionID, &e.CustomerID,
		&e.TokenNumber, &e.State, &e.PresenceState, &e.SessionChannel,
		&e.IsDispatchable, &e.RequestedBarberID, &e.PriorityGroup,
		&e.SortKey, &e.RemoteJoinedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}
