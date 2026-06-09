package repository_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"barberbase-core/internal/repository"
)

// testPool returns a pgxpool connected to the PG16 test instance.
// Reads TEST_DATABASE_URL or DATABASE_URL from env; test is skipped if not set.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL or DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// seedLocation inserts a minimal tenant + location and returns their IDs.
// Cleaned up via t.Cleanup DELETE.
func seedLocation(t *testing.T, pool *pgxpool.Pool) (tenantID, locationID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	err := pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('Test Tenant', $1, '+919999999999')
		RETURNING id`, "test-"+uuid.New().String()[:8],
	).Scan(&tenantID)
	require.NoError(t, err)

	err = pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, 'Test Location')
		RETURNING id`, tenantID, "loc-"+uuid.New().String()[:8],
	).Scan(&locationID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM queue_sessions WHERE location_id = $1`, locationID)
		pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, locationID)
		pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return
}

// TestEnsureAndLockQueueSession_ConcurrentFirstJoiners proves that 50 goroutines
// racing on a fresh business_date produce exactly one queue_sessions row with
// last_token_number=0 and no errors.
func TestEnsureAndLockQueueSession_ConcurrentFirstJoiners(t *testing.T) {
	pool := testPool(t)
	tenantID, locationID := seedLocation(t, pool)

	// Use a far-future date so it cannot collide with production or other tests.
	bizDate := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	const goroutines = 50
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			_, err = repository.EnsureAndLockQueueSession(ctx, tx, tenantID, locationID, bizDate)
			if err != nil {
				_ = tx.Rollback(ctx)
				errs[i] = err
				return
			}
			errs[i] = tx.Commit(ctx)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed", i)
	}

	// Exactly one row must exist.
	var rowCount, lastToken int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*), MAX(last_token_number)
		 FROM queue_sessions
		 WHERE location_id = $1 AND business_date = $2`,
		locationID, bizDate,
	).Scan(&rowCount, &lastToken)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount, "expected exactly one queue_sessions row")
	require.Equal(t, 0, lastToken, "last_token_number must be 0 — no tokens assigned yet")
}
