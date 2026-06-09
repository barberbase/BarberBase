package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

type QueueEntryServiceRow struct {
	Name            string
	DurationMinutes int
	PricePaise      int
}

type QueueEntryStaffRow struct {
	ID                   uuid.UUID
	TokenNumber          int
	State                string
	PresenceState        string
	IsDispatchable       bool
	CustomerID           *uuid.UUID
	CustomerName         *string
	CustomerPhone        *string
	CustomerVisitCount   *int
	CustomerNotes        []string
	Services             []QueueEntryServiceRow
	TotalDurationMinutes int
	PartySize            *int
	RequestedBarberID    *uuid.UUID
	AssignedBarberID     *uuid.UUID
	JoinedAt             time.Time
	CalledAt             *time.Time
	StartedAt            *time.Time
	StaleWarning         *string
}

type CallNextParams struct {
	TenantID      uuid.UUID
	LocationID    uuid.UUID
	StaffMemberID uuid.UUID
}

type ErrRepoNoDispatchable struct {
	WaitingRemoteCount int
}

func (e ErrRepoNoDispatchable) Error() string { return "no dispatchable customers" }

var ErrRepoSessionNotFound = errors.New("no active queue session for today")

func GetLocationRoutingMode(ctx context.Context, pool *pgxpool.Pool, locationID uuid.UUID) (routingMode, timezone string, err error) {
	const query = `
		SELECT queue_routing_mode, timezone FROM locations
		WHERE id = $1 AND is_active = true`
	err = pool.QueryRow(ctx, query, locationID).Scan(&routingMode, &timezone)
	return routingMode, timezone, err
}

func CallNextTx(ctx context.Context, tx pgx.Tx, params CallNextParams, routingMode, businessDate string) (sessionID, entryID, visitID uuid.UUID, newVersion int, err error) {
	// Step 4: [Law 1] Lock queue_session FOR UPDATE
	const lockQuery = `
		SELECT id, queue_version
		FROM queue_sessions
		WHERE location_id = $1 AND business_date = $2
		FOR UPDATE`
	var oldVersion int
	err = tx.QueryRow(ctx, lockQuery, params.LocationID, businessDate).Scan(&sessionID, &oldVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, uuid.Nil, 0, ErrRepoSessionNotFound
		}
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("lock queue session: %w", err)
	}

	// Step 5: [Law 12] Dispatch query — routing-mode-specific, plain FOR UPDATE, never SKIP LOCKED
	var selectErr error
	var customerID *uuid.UUID
	var sessionChannel string

	switch routingMode {
	case "pooled":
		const q = `
			SELECT id, visit_id, customer_id, session_channel
			FROM queue_entries
			WHERE queue_session_id = $1
			  AND state = 'waiting'
			  AND is_dispatchable = true
			  AND presence_state = 'arrived'
			ORDER BY priority_group ASC, sort_key ASC, token_number ASC
			LIMIT 1
			FOR UPDATE`
		selectErr = tx.QueryRow(ctx, q, sessionID).Scan(&entryID, &visitID, &customerID, &sessionChannel)
	case "hybrid":
		const q = `
			SELECT id, visit_id, customer_id, session_channel
			FROM queue_entries
			WHERE queue_session_id = $1
			  AND state = 'waiting'
			  AND is_dispatchable = true
			  AND presence_state = 'arrived'
			  AND (requested_barber_id = $2 OR requested_barber_id IS NULL)
			ORDER BY priority_group ASC, sort_key ASC, token_number ASC
			LIMIT 1
			FOR UPDATE`
		selectErr = tx.QueryRow(ctx, q, sessionID, params.StaffMemberID).Scan(&entryID, &visitID, &customerID, &sessionChannel)
	case "barber_specific":
		const q = `
			SELECT id, visit_id, customer_id, session_channel
			FROM queue_entries
			WHERE queue_session_id = $1
			  AND state = 'waiting'
			  AND is_dispatchable = true
			  AND presence_state = 'arrived'
			  AND requested_barber_id = $2
			ORDER BY priority_group ASC, sort_key ASC, token_number ASC
			LIMIT 1
			FOR UPDATE`
		selectErr = tx.QueryRow(ctx, q, sessionID, params.StaffMemberID).Scan(&entryID, &visitID, &customerID, &sessionChannel)
	default:
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("invalid routing mode: %s", routingMode)
	}

	// Step 6: If no row — run waiting_remote_count query
	if errors.Is(selectErr, pgx.ErrNoRows) {
		var count int
		var countErr error
		switch routingMode {
		case "pooled":
			const q = `
				SELECT COUNT(*) FROM queue_entries
				WHERE queue_session_id = $1 AND state = 'waiting'
				  AND is_dispatchable = true AND presence_state != 'arrived'`
			countErr = tx.QueryRow(ctx, q, sessionID).Scan(&count)
		case "hybrid":
			const q = `
				SELECT COUNT(*) FROM queue_entries
				WHERE queue_session_id = $1 AND state = 'waiting'
				  AND is_dispatchable = true AND presence_state != 'arrived'
				  AND (requested_barber_id = $2 OR requested_barber_id IS NULL)`
			countErr = tx.QueryRow(ctx, q, sessionID, params.StaffMemberID).Scan(&count)
		case "barber_specific":
			const q = `
				SELECT COUNT(*) FROM queue_entries
				WHERE queue_session_id = $1 AND state = 'waiting'
				  AND is_dispatchable = true AND presence_state != 'arrived'
				  AND requested_barber_id = $2`
			countErr = tx.QueryRow(ctx, q, sessionID, params.StaffMemberID).Scan(&count)
		}
		if countErr != nil {
			return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("count waiting remote: %w", countErr)
		}
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, ErrRepoNoDispatchable{WaitingRemoteCount: count}
	} else if selectErr != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("select next entry: %w", selectErr)
	}

	// Step 7: Update entry (state → called)
	const updateEntry = `
		UPDATE queue_entries
		SET state = 'called',
		    called_at = NOW(),
		    assigned_barber_id = $1,
		    stale_warning = NULL
		WHERE id = $2`
	_, err = tx.Exec(ctx, updateEntry, params.StaffMemberID, entryID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("update queue entry state: %w", err)
	}

	// Step 8: Update staff status
	const updateStaff = `
		UPDATE staff_members
		SET status = 'cutting'
		WHERE id = $1`
	_, err = tx.Exec(ctx, updateStaff, params.StaffMemberID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("update staff status: %w", err)
	}

	// Step 9: [Law 7] Insert outbox event
	payloadMap := map[string]any{
		"template_key":    "bb_you_are_next",
		"queue_entry_id":  entryID.String(),
		"visit_id":        visitID.String(),
		"tenant_id":       params.TenantID.String(),
		"location_id":     params.LocationID.String(),
		"session_channel": sessionChannel,
	}
	if customerID != nil {
		payloadMap["customer_id"] = customerID.String()
	}

	payloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("marshal outbox payload: %w", err)
	}

	const insertOutbox = `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1, 'notification.send', $2, NOW())`
	_, err = tx.Exec(ctx, insertOutbox, params.TenantID, payloadBytes)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("insert outbox event: %w", err)
	}

	// Step 10: Increment queue_version
	newVersion = oldVersion + 1
	const updateSession = `
		UPDATE queue_sessions
		SET queue_version = $1
		WHERE id = $2`
	_, err = tx.Exec(ctx, updateSession, newVersion, sessionID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("increment queue session version: %w", err)
	}

	return sessionID, entryID, visitID, newVersion, nil
}

func MaskPhoneNumber(phone string) string {
	if len(phone) < 4 {
		return phone
	}
	last4 := phone[len(phone)-4:]
	if len(phone) >= 13 && phone[:3] == "+91" {
		return "+91 XXXX XX" + last4
	}
	if len(phone) > 7 {
		return phone[:3] + " XXXX XX" + last4
	}
	return "XXXX " + last4
}

func GetEntryStaffView(ctx context.Context, pool *pgxpool.Pool, entryID uuid.UUID) (*QueueEntryStaffRow, error) {
	const queryEntry = `
		SELECT qe.id, qe.token_number, qe.state, qe.presence_state, qe.is_dispatchable,
		       qe.customer_id, c.name, c.phone_number, c.visit_count,
		       v.total_duration_minutes, v.party_size, qe.requested_barber_id, qe.assigned_barber_id,
		       qe.remote_joined_at, qe.called_at, qe.started_at, qe.stale_warning, v.id AS visit_id
		FROM queue_entries qe
		JOIN visits v ON qe.visit_id = v.id
		LEFT JOIN customers c ON qe.customer_id = c.id
		WHERE qe.id = $1`

	var r QueueEntryStaffRow
	var visitID uuid.UUID
	var phoneNum *string
	err := pool.QueryRow(ctx, queryEntry, entryID).Scan(
		&r.ID, &r.TokenNumber, &r.State, &r.PresenceState, &r.IsDispatchable,
		&r.CustomerID, &r.CustomerName, &phoneNum, &r.CustomerVisitCount,
		&r.TotalDurationMinutes, &r.PartySize, &r.RequestedBarberID, &r.AssignedBarberID,
		&r.JoinedAt, &r.CalledAt, &r.StartedAt, &r.StaleWarning, &visitID,
	)
	if err != nil {
		return nil, err
	}

	if phoneNum != nil {
		masked := MaskPhoneNumber(*phoneNum)
		r.CustomerPhone = &masked
	}

	// Fetch customer notes if customer exists
	if r.CustomerID != nil {
		const queryNotes = `
			SELECT note FROM customer_notes
			WHERE customer_id = $1 AND deleted_at IS NULL
			ORDER BY created_at DESC`
		rows, err := pool.Query(ctx, queryNotes, *r.CustomerID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var note string
			if err := rows.Scan(&note); err != nil {
				return nil, err
			}
			r.CustomerNotes = append(r.CustomerNotes, note)
		}
	}

	// Fetch services
	const queryServices = `
		SELECT variant_name_snapshot, duration_minutes_snapshot, price_paise_snapshot
		FROM visit_services
		WHERE visit_id = $1
		ORDER BY sort_order ASC`
	rows, err := pool.Query(ctx, queryServices, visitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s QueueEntryServiceRow
		if err := rows.Scan(&s.Name, &s.DurationMinutes, &s.PricePaise); err != nil {
			return nil, err
		}
		r.Services = append(r.Services, s)
	}

	return &r, nil
}
