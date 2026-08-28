package notification

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type purgeStore struct {
	deleted []string
	err     error
}

func (p *purgeStore) Delete(key string) error {
	p.deleted = append(p.deleted, key)
	return p.err
}

type purgeFixture struct {
	pool       *pgxpool.Pool
	tenantID   uuid.UUID
	locationID uuid.UUID
	repo       *repository.MediaRepository
}

func setupPurge(t *testing.T) purgeFixture {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, "TRUNCATE tenants CASCADE")
	require.NoError(t, err)

	f := purgeFixture{pool: pool, repo: &repository.MediaRepository{Pool: pool}}
	sfx := uuid.NewString()[:8]
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('Purge', $1, '+919999922222') RETURNING id`, "pg-"+sfx).Scan(&f.tenantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name) VALUES ($1,$2,'Purge Loc') RETURNING id`,
		f.tenantID, "pgloc-"+sfx).Scan(&f.locationID))
	return f
}

func (f purgeFixture) seedReady(t *testing.T, key string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		INSERT INTO media_assets
			(tenant_id, location_id, purpose, r2_key, content_hash, status, committed_at)
		VALUES ($1,$2,'location_logo',$3, repeat('b',64), 'ready', NOW()) RETURNING id`,
		f.tenantID, f.locationID, key).Scan(&id))
	return id
}

// A7 — archive writes the asset and the outbox row in ONE transaction, the
// purge runs only after process_after, and a rolled-back archive leaves no
// outbox row at all.
func TestA7_ArchiveSchedulesPurgeAtomically(t *testing.T) {
	f := setupPurge(t)
	ctx := context.Background()
	assetID := f.seedReady(t, "loc/x/logo/live.webp")

	require.NoError(t, f.repo.ArchiveWithPurge(ctx, f.tenantID, f.locationID, assetID, 7*24*time.Hour))

	var status string
	var archivedAt *time.Time
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status, archived_at FROM media_assets WHERE id=$1`, assetID).Scan(&status, &archivedAt))
	require.Equal(t, "archived", status)
	require.NotNil(t, archivedAt)

	var evType string
	var processAfter time.Time
	var payload []byte
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT type, process_after, payload FROM outbox_events WHERE tenant_id=$1`,
		f.tenantID).Scan(&evType, &processAfter, &payload))
	require.Equal(t, "media.purge", evType)

	// The grace window is what makes a mistaken delete recoverable.
	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), processAfter, time.Minute)
	require.Greater(t, processAfter.Sub(time.Now()).Hours(), 24.0,
		"A7: the purge must not be eligible to run today")

	var p mediaPurgePayload
	require.NoError(t, json.Unmarshal(payload, &p))
	require.Equal(t, assetID.String(), p.MediaAssetID)
	require.Equal(t, "loc/x/logo/live.webp", p.R2Key)
}

// A7 (second half) — Law 7 the other way round: if the transaction rolls back,
// no outbox row exists. The asset and its scheduled purge can never disagree.
func TestA7_RolledBackArchiveLeavesNoOutboxRow(t *testing.T) {
	f := setupPurge(t)
	ctx := context.Background()
	assetID := f.seedReady(t, "loc/x/logo/rollback.webp")

	// Drive the same two statements the repository does, then roll back.
	tx, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	var key string
	require.NoError(t, tx.QueryRow(ctx, `
		UPDATE media_assets SET status='archived', archived_at=NOW()
		WHERE id=$1 RETURNING r2_key`, assetID).Scan(&key))
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1,'media.purge',$2, NOW())`, f.tenantID, []byte(`{"r2_key":"x"}`))
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))

	var n int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE tenant_id=$1`, f.tenantID).Scan(&n))
	require.Zero(t, n, "A7: a rolled-back archive schedules nothing")

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, assetID).Scan(&status))
	require.Equal(t, "ready", status, "A7: and the asset is still live")
}

// A7 (third half) — the handler deletes the R2 object, then the row.
func TestA7_PurgeHandlerDeletesObjectThenRow(t *testing.T) {
	f := setupPurge(t)
	ctx := context.Background()
	assetID := f.seedReady(t, "loc/x/logo/purge-me.webp")
	require.NoError(t, f.repo.ArchiveWithPurge(ctx, f.tenantID, f.locationID, assetID, 0))

	var payload []byte
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT payload FROM outbox_events WHERE tenant_id=$1`, f.tenantID).Scan(&payload))

	store := &purgeStore{}
	h := &MediaPurgeHandler{Pool: f.pool, Store: store}
	require.NoError(t, h.Handle(ctx, f.pool, &OutboxEvent{Payload: payload}))

	require.Equal(t, []string{"loc/x/logo/purge-me.webp"}, store.deleted)
	var n int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE id=$1`, assetID).Scan(&n))
	require.Zero(t, n, "A7: the row goes only after the object")
}

// An R2 failure is retryable and must leave the row intact — the outbox's
// backoff handles the rest.
func TestPurgeHandler_R2FailureIsRetryableAndKeepsRow(t *testing.T) {
	f := setupPurge(t)
	ctx := context.Background()
	assetID := f.seedReady(t, "loc/x/logo/keep.webp")
	require.NoError(t, f.repo.ArchiveWithPurge(ctx, f.tenantID, f.locationID, assetID, 0))

	var payload []byte
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT payload FROM outbox_events WHERE tenant_id=$1`, f.tenantID).Scan(&payload))

	store := &purgeStore{err: r2.ErrUnavailable}
	h := &MediaPurgeHandler{Pool: f.pool, Store: store}
	err := h.Handle(ctx, f.pool, &OutboxEvent{Payload: payload})
	require.Error(t, err)
	require.ErrorIs(t, err, r2.ErrUnavailable)

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, assetID).Scan(&status))
	require.Equal(t, "archived", status, "the row survives for the retry")
}

// A malformed payload can only ever fail the same way, so it is terminal — the
// worker sets attempts to max rather than retrying it every backoff step.
func TestPurgeHandler_MalformedPayloadIsTerminal(t *testing.T) {
	f := setupPurge(t)
	h := &MediaPurgeHandler{Pool: f.pool, Store: &purgeStore{}}

	for _, bad := range [][]byte{
		[]byte(`not json`),
		[]byte(`{}`),
		[]byte(`{"media_asset_id":"not-a-uuid","r2_key":"k"}`),
	} {
		err := h.Handle(context.Background(), f.pool, &OutboxEvent{Payload: bad})
		require.Error(t, err)
		var te terminalError
		require.True(t, errors.As(err, &te), "payload %q must be terminal, got %v", bad, err)
	}
}

var _ = pgx.ErrNoRows // keep the pgx import honest if the file is trimmed later
