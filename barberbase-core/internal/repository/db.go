package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InitPool initializes the pgxpool connection pool with specified session-level defaults
func InitPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database url: %w", err)
	}

	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = time.Minute

	// Apply sticky session parameters on connection creation
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `
			SET lock_timeout = '1s';
			SET statement_timeout = '5s';
			SET idle_in_transaction_session_timeout = '10s';
		`)
		if err != nil {
			return fmt.Errorf("failed to apply session parameters: %w", err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pgxpool: %w", err)
	}

	// Ping connection to verify accessibility
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

// WithTx executes a callback function within a database transaction.
// It handles panic-recovery, rollback on error, and commit on success.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (retErr error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if r := recover(); r != nil {
			// Catch panic, rollback transaction, ignore rollback error, and re-panic
			_ = tx.Rollback(ctx)
			panic(r)
		} else if retErr != nil {
			// Rollback transaction on error, ignore rollback error
			_ = tx.Rollback(ctx)
		} else {
			// Commit transaction on success, return commit error if it fails
			if commitErr := tx.Commit(ctx); commitErr != nil {
				retErr = fmt.Errorf("failed to commit transaction: %w", commitErr)
			}
		}
	}()

	retErr = fn(tx)
	return retErr
}
