package queue

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"barberbase-core/internal/repository"
)

// Commands is the domain service for all queue mutations.
// C2.2–C2.6 add methods to this type (JoinQueue, CallNext, Start, etc.).
// Every method must call lockSession as its first operation inside the tx.
type Commands struct {
	pool *pgxpool.Pool
}

// NewCommands constructs the queue domain service.
// Wire once in cmd/server/main.go; attach to api.Server.
func NewCommands(pool *pgxpool.Pool) *Commands {
	return &Commands{pool: pool}
}

// lockSession is the single enforced entry point for the upsert-then-lock pattern.
// Every queue mutation method calls this first inside its transaction.
// Package-private; not exposed outside the queue domain package.
func lockSession(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, locationID uuid.UUID,
	businessDate time.Time,
) (*repository.QueueSession, error) {
	return repository.EnsureAndLockQueueSession(ctx, tx, tenantID, locationID, businessDate)
}
