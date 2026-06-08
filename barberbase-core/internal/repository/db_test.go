package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func getTestDatabaseURL() string {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}
	return url
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	connStr := getTestDatabaseURL()

	pool, err := InitPool(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to initialize test DB pool: %v", err)
	}

	// Run migration to ensure tables exist
	err = Migrate(ctx, pool, "../../migrations/001_complete_schema.sql")
	if err != nil {
		t.Fatalf("Failed to run migrations for test: %v", err)
	}

	// Clean up database tables for a clean test run
	_, _ = pool.Exec(ctx, "TRUNCATE tenants CASCADE")

	return pool
}

func TestWithTx_PanicRollbackAndNoLeak(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// 1. Define a function that inserts a tenant then panics
	runPanicTx := func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic to propagate, but it was swallowed")
			}
		}()

		_ = WithTx(ctx, pool, func(tx pgx.Tx) error {
			// Insert a tenant
			_, err := tx.Exec(ctx, `
				INSERT INTO tenants (id, name, slug, owner_phone_number)
				VALUES ('01906a2c-4f3e-7000-8000-000000000001', 'Test Tenant', 'test-tenant', '+919876543210');
			`)
			if err != nil {
				t.Fatalf("Failed to insert test tenant: %v", err)
			}

			panic("simulated panic inside transaction")
		})
	}

	// Run the panic transaction
	runPanicTx()

	// 2. Assert that the tenant was NOT committed (rolled back)
	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM tenants WHERE id = '01906a2c-4f3e-7000-8000-000000000001'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected tenant row to be rolled back, but found %d rows", count)
	}

	// 3. Assert no connection leaks
	stats := pool.Stat()
	if stats.AcquiredConns() != 0 {
		t.Errorf("Expected 0 acquired connections, but got %d", stats.AcquiredConns())
	}
}

func TestWithTx_LockTimeout(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// Seed one tenant row
	tenantID := "01906a2c-4f3e-7000-8000-000000000002"
	_, err := pool.Exec(ctx, `
		INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Lock Tenant', 'lock-tenant', '+919876543211');
	`, tenantID)
	if err != nil {
		t.Fatalf("Failed to seed test tenant: %v", err)
	}

	var tx1Started sync.WaitGroup
	var tx2Finished sync.WaitGroup
	var tx1CanFinish sync.WaitGroup

	tx1Started.Add(1)
	tx2Finished.Add(1)
	tx1CanFinish.Add(1)

	var tx2Err error

	// Goroutine 1: Tx1 starts and acquires a FOR UPDATE lock on the tenant row
	go func() {
		err := WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "SELECT id FROM tenants WHERE id = $1 FOR UPDATE", tenantID)
			if err != nil {
				t.Errorf("Tx1 failed to lock row: %v", err)
			}

			tx1Started.Done() // Signal Tx2 that the lock is held
			tx1CanFinish.Wait() // Wait until Tx2 attempts lock and completes with timeout

			return nil
		})
		if err != nil {
			t.Errorf("Tx1 failed: %v", err)
		}
	}()

	// Goroutine 2: Tx2 attempts to lock the same row and should time out
	go func() {
		tx1Started.Wait() // Wait until Tx1 holds the lock

		tx2Err = WithTx(ctx, pool, func(tx pgx.Tx) error {
			// This select should block and time out because lock_timeout is 1s
			_, err := tx.Exec(ctx, "SELECT id FROM tenants WHERE id = $1 FOR UPDATE", tenantID)
			return err
		})

		tx1CanFinish.Done() // Signal Tx1 that it can commit
		tx2Finished.Done()
	}()

	tx2Finished.Wait()

	// Assert that Tx2 failed with a lock timeout error
	if tx2Err == nil {
		t.Fatal("Expected Tx2 to fail with lock timeout, but got no error")
	}

	var pgErr *pgconn.PgError
	if errors.As(tx2Err, &pgErr) {
		// PostgreSQL lock timeout SQLSTATE code is 55P03 (lock_not_available)
		if pgErr.Code != "55P03" {
			t.Errorf("Expected SQLSTATE 55P03 (lock_not_available), but got %s", pgErr.Code)
		}
	} else {
		t.Errorf("Expected pgconn.PgError, but got: %T (%v)", tx2Err, tx2Err)
	}
}
