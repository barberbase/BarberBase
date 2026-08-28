package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"barberbase-core/internal/r2"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// fakeDeleter records what the reaper asked R2 to remove and can be made to
// fail, which is the whole surface the reaper touches.
type fakeDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (f *fakeDeleter) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, key)
	return f.err
}
func (f *fakeDeleter) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}
func (f *fakeDeleter) setErr(e error) { f.mu.Lock(); f.err = e; f.mu.Unlock() }

// seedAsset inserts one media_assets row with an explicit age and status.
func seedAsset(t *testing.T, pool *pgxpool.Pool, s tierShop, key, status string, age time.Duration) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO media_assets
			(tenant_id, location_id, purpose, r2_key, content_hash, status, created_at)
		VALUES ($1, $2, 'location_logo', $3, repeat('a', 64), $4, NOW() - $5::interval)
		RETURNING id`,
		s.tenantID, s.locationID, key, status,
		time.Duration(age).String()).Scan(&id))
	return id
}

func assetExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM media_assets WHERE id = $1`, id).Scan(&n))
	return n == 1
}

// A6 — pending rows older than an hour are deleted from R2 and then the DB;
// newer pending rows and ready/archived rows are untouched.
func TestMediaReaper_A6_ReapsOnlyStalePendingRows(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	stale := seedAsset(t, pool, shop, "loc/x/logo/stale.webp", "pending", 2*time.Hour)
	fresh := seedAsset(t, pool, shop, "loc/x/logo/fresh.webp", "pending", 5*time.Minute)
	ready := seedAsset(t, pool, shop, "loc/x/logo/ready.webp", "ready", 9*time.Hour)
	archived := seedAsset(t, pool, shop, "loc/x/logo/arch.webp", "archived", 9*time.Hour)

	del := &fakeDeleter{}
	NewMediaReaper(pool, del, 100).reap(ctx)

	require.Equal(t, []string{"loc/x/logo/stale.webp"}, del.keys(),
		"A6: only the stale pending object is deleted from R2")
	require.False(t, assetExists(t, pool, stale), "A6: its row is gone too")
	require.True(t, assetExists(t, pool, fresh), "A6: a pending row inside the window survives")
	require.True(t, assetExists(t, pool, ready), "A6: a committed asset is never reaped")
	require.True(t, assetExists(t, pool, archived), "A6: archived is the purge handler's business, not the reaper's")
}

// A6b — R2 unreachable: no rows deleted, and the next tick with R2 restored
// completes the reap. The row is the only handle on the object, so losing it
// while the object survives would orphan the object permanently.
func TestMediaReaper_A6b_R2DownDeletesNoRows(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)
	id := seedAsset(t, pool, shop, "loc/x/logo/stale.webp", "pending", 2*time.Hour)

	del := &fakeDeleter{}
	del.setErr(r2.ErrUnavailable)
	reaper := NewMediaReaper(pool, del, 100)

	reaper.reap(ctx)
	require.True(t, assetExists(t, pool, id),
		"A6b: an R2 failure must never delete the row — it is the only record the object exists")

	del.setErr(nil)
	reaper.reap(ctx)
	require.False(t, assetExists(t, pool, id), "A6b: the next tick with R2 back completes the reap")
}

// A6c — a DELETE for an object that is already gone is success, not an error.
// The reaper deletes from R2 first, so a crash between the two steps leaves a
// row whose object is gone; the retry must converge rather than wedge forever.
func TestMediaReaper_A6c_MissingObjectIsSuccess(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)
	id := seedAsset(t, pool, shop, "loc/x/logo/already-gone.webp", "pending", 2*time.Hour)

	// r2.Store.Delete maps a 404 to nil; the fake returning nil is that contract.
	del := &fakeDeleter{}
	NewMediaReaper(pool, del, 100).reap(ctx)

	require.Len(t, del.keys(), 1)
	require.False(t, assetExists(t, pool, id),
		"A6c: a 404 on delete is success — the row must be removed, not retried forever")
}

// A6d — the reaper and the watchdog are independent: distinct advisory locks and
// separate goroutines. Holding the reaper's lock must not stop a watchdog tick.
//
// This is the structural guarantee behind the design choice: media talks to R2,
// and R2 must never be able to delay the loop that drives auto-snooze.
func TestMediaReaper_A6d_IndependentOfWatchdog(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	require.NotEqual(t, advisoryLockWatchdog, advisoryLockMediaReaper,
		"A6d: the reaper must not share the watchdog's advisory lock")

	// Take the reaper's lock on a dedicated connection and keep it.
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	var got bool
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1)", advisoryLockMediaReaper).Scan(&got))
	require.True(t, got)
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockMediaReaper)

	// A reaper tick now no-ops rather than blocking.
	id := seedAsset(t, pool, shop, "loc/x/logo/held.webp", "pending", 2*time.Hour)
	del := &fakeDeleter{}
	done := make(chan struct{})
	go func() { NewMediaReaper(pool, del, 100).tick(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("A6d: a reaper tick blocked on a held lock instead of skipping")
	}
	require.Empty(t, del.keys(), "A6d: the tick correctly skipped")
	require.True(t, assetExists(t, pool, id))

	// And the watchdog runs to completion regardless.
	wdDone := make(chan struct{})
	go func() { shop.watchdog.tick(ctx); close(wdDone) }()
	select {
	case <-wdDone:
	case <-time.After(10 * time.Second):
		t.Fatal("A6d: the watchdog was delayed by the reaper's lock — they must be independent")
	}
}

// The batch bound exists so a backlog cannot hold the goroutine for minutes on a
// 1GB droplet.
func TestMediaReaper_BatchIsBounded(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)

	for i := 0; i < 7; i++ {
		seedAsset(t, pool, shop, "loc/x/logo/o"+string(rune('a'+i))+".webp", "pending", 2*time.Hour)
	}
	del := &fakeDeleter{}
	NewMediaReaper(pool, del, 3).reap(ctx)
	require.Len(t, del.keys(), 3, "one tick processes at most MEDIA_REAP_BATCH rows")
}

// An unconfigured deployment must not have its reaper delete rows whose objects
// it cannot reach.
func TestMediaReaper_UnconfiguredR2LeavesRowsAlone(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	shop := seedTierShop(t, pool)
	id := seedAsset(t, pool, shop, "loc/x/logo/unconfigured.webp", "pending", 2*time.Hour)

	del := &fakeDeleter{}
	del.setErr(r2.ErrNotConfigured)
	NewMediaReaper(pool, del, 100).reap(ctx)

	require.True(t, assetExists(t, pool, id),
		"no credentials means the object cannot be reached; the row must stay")
}
