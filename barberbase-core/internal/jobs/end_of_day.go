package jobs

import (
	"context"
	"log"
	"time"

	"barberbase-core/internal/config"
	"barberbase-core/internal/realtime"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EndOfDay struct {
	db      *pgxpool.Pool
	manager *realtime.Manager
	cfg     *config.Config
}

func NewEndOfDay(db *pgxpool.Pool, manager *realtime.Manager, cfg *config.Config) *EndOfDay {
	return &EndOfDay{
		db:      db,
		manager: manager,
		cfg:     cfg,
	}
}

func (e *EndOfDay) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// Run immediately on start
	e.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

func (e *EndOfDay) tick(ctx context.Context) {
	var acquired bool
	err := e.db.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockEndOfDay).Scan(&acquired)
	if err != nil || !acquired {
		return
	}
	defer e.db.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockEndOfDay)

	e.runJob(ctx)
}

type locationEODInfo struct {
	LocationID uuid.UUID
	TenantID   uuid.UUID
	Timezone   string
	ClosesAt   time.Time
	SessionID  uuid.UUID
}

func (e *EndOfDay) runJob(ctx context.Context) {
	rows, err := e.db.Query(ctx, `
		SELECT DISTINCT ON (l.id)
		  l.id AS location_id, l.tenant_id, l.timezone,
		  lh.closes_at,
		  qs.id AS session_id
		FROM locations l
		JOIN location_hours lh ON lh.location_id = l.id
		  AND lh.day_of_week = EXTRACT(DOW FROM (NOW() AT TIME ZONE l.timezone))::SMALLINT
		JOIN queue_sessions qs ON qs.location_id = l.id
		  AND qs.business_date = (NOW() AT TIME ZONE l.timezone)::DATE
		  AND qs.status NOT IN ('archived', 'closed')
		WHERE l.is_active = true
		  AND lh.is_open = true
		  AND lh.closes_at IS NOT NULL
	`)
	if err != nil {
		log.Printf("EOD: failed to query locations for EOD: %v", err)
		return
	}
	defer rows.Close()

	var locations []locationEODInfo
	for rows.Next() {
		var info locationEODInfo
		err := rows.Scan(&info.LocationID, &info.TenantID, &info.Timezone, &info.ClosesAt, &info.SessionID)
		if err != nil {
			log.Printf("EOD: failed to scan location EOD info: %v", err)
			continue
		}
		locations = append(locations, info)
	}
	rows.Close()

	for _, row := range locations {
		loc, err := time.LoadLocation(row.Timezone)
		if err != nil {
			loc = time.Local
		}
		now := time.Now().In(loc)
		closingTime := time.Date(
			now.Year(), now.Month(), now.Day(),
			row.ClosesAt.Hour(), row.ClosesAt.Minute(), 0, 0, loc,
		)
		eodTrigger := closingTime.Add(2 * time.Hour)
		if time.Now().Before(eodTrigger) {
			continue // not yet time
		}

		e.runEODForSession(ctx, row)
	}
}

func (e *EndOfDay) runEODForSession(ctx context.Context, row locationEODInfo) {
	var newQueueVersion int

	err := repository.WithTx(ctx, e.db, func(tx pgx.Tx) error {
		// Law 1: Lock session first
		var sessionLockID uuid.UUID
		err := tx.QueryRow(ctx, "SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE", row.SessionID).Scan(&sessionLockID)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET state = 'expired', is_dispatchable = false
			WHERE queue_session_id = $1
			  AND state IN ('waiting', 'called', 'skipped')
		`, row.SessionID)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET state = 'needs_review', is_dispatchable = false
			WHERE queue_session_id = $1
			  AND state = 'in_progress'
		`, row.SessionID)
		if err != nil {
			return err
		}

		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET status = 'archived',
			    closed_at = COALESCE(closed_at, NOW()),
			    archived_at = NOW(),
			    queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version
		`, row.SessionID).Scan(&newQueueVersion)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("EOD: failed to run EOD for session %s: %v", row.SessionID, err)
		return
	}

	// After COMMIT: broadcast SSE — Law 8
	e.manager.Broadcast(row.LocationID.String(), realtime.SSEEvent{
		Type:         "queue_changed",
		LocationID:   row.LocationID.String(),
		QueueVersion: newQueueVersion,
	})
}
