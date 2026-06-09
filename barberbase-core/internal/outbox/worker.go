package outbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/bhejna"
	notification "barberbase-core/internal/outbox/handlers"
)

// OutboxEvent mirrors the columns returned by the claim-or-reclaim RETURNING *.
type OutboxEvent = notification.OutboxEvent

// OutboxHandler handles one outbox event type.
// Return nil on success.
// Return a retryable error for transient failures.
// Return ErrTerminal (or wrap with %w) for permanent failures (non-retryable Bhejna 4xx, quota block).
type OutboxHandler interface {
	Handle(ctx context.Context, pool *pgxpool.Pool, event *OutboxEvent) error
}

var ErrTerminal = errors.New("terminal: do not retry")
var ErrUnhandledType = fmt.Errorf("unhandled outbox type: %w", ErrTerminal)

type Worker struct {
	pool     *pgxpool.Pool
	handlers map[string]OutboxHandler
}

func NewWorker(pool *pgxpool.Pool, bhejna bhejna.Client) *Worker {
	n := notification.NewHandler(pool, bhejna)
	return &Worker{
		pool: pool,
		handlers: map[string]OutboxHandler{
			"notification.send":         n,
			"appointment.reminder":      n,
			"weekly_summary.send":       n,
			"feedback_request.schedule": &stubHandler{}, // C4.4 replaces
			"web_push.send":             &stubHandler{}, // C6.5 replaces
		},
	}
}

type stubHandler struct{}

func (s *stubHandler) Handle(ctx context.Context, pool *pgxpool.Pool, event *OutboxEvent) error {
	return ErrUnhandledType
}

// Run loop
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.processOne(ctx); err != nil {
				log.Printf("outbox: processOne error: %v", err)
			}
		}
	}
}

func (w *Worker) processOne(ctx context.Context) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var event OutboxEvent
	err = tx.QueryRow(ctx, `
		UPDATE outbox_events
		SET    status = 'processing',
		       locked_until = NOW() + INTERVAL '30 seconds',
		       attempts     = attempts + 1
		WHERE  id = (
			SELECT id FROM outbox_events
			WHERE  process_after <= NOW()
			  AND  (   status = 'pending'
			       OR (status = 'failed'     AND attempts < max_attempts)
			       OR (status = 'processing' AND locked_until < NOW()) )
			ORDER BY process_after
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING
			id, tenant_id, type, payload, status, attempts, max_attempts,
			process_after, locked_until, last_error, created_at;
	`).Scan(
		&event.ID,
		&event.TenantID,
		&event.Type,
		&event.Payload,
		&event.Status,
		&event.Attempts,
		&event.MaxAttempts,
		&event.ProcessAfter,
		&event.LockedUntil,
		&event.LastError,
		&event.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Commit(ctx)
			return nil
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	handler, ok := w.handlers[event.Type]
	if !ok {
		handler = &stubHandler{}
	}

	err = handler.Handle(ctx, w.pool, &event)

	if err == nil {
		// success
		_, execErr := w.pool.Exec(ctx,
			`UPDATE outbox_events SET status='dispatched', dispatched_at=NOW() WHERE id=$1`,
			event.ID)
		return execErr
	}

	errMsg := err.Error()

	if errors.Is(err, ErrTerminal) {
		// terminal — set attempts to max so the claim WHERE never picks it up again
		_, execErr := w.pool.Exec(ctx,
			`UPDATE outbox_events SET status='failed', last_error=$1, attempts=max_attempts WHERE id=$2`,
			errMsg, event.ID)
		return execErr
	}

	// retryable
	if event.Attempts >= event.MaxAttempts {
		// exhausted
		_, execErr := w.pool.Exec(ctx,
			`UPDATE outbox_events SET status='failed', last_error=$1 WHERE id=$2`,
			errMsg, event.ID)
		return execErr
	}

	backoff := backoffFor(event.Attempts)
	_, execErr := w.pool.Exec(ctx,
		`UPDATE outbox_events SET status='failed', last_error=$1, process_after=NOW()+$2 WHERE id=$3`,
		errMsg, backoff, event.ID)
	return execErr
}

func backoffFor(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}
