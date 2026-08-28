package jobs

import (
	"context"
	"errors"
	"log"
	"time"

	"barberbase-core/internal/domain/media"
	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockMediaReaper is its own key. 0xBBC401-403 are taken in watchdog.go
// (watchdog, end-of-day, weekly summary) — check there before adding a fourth.
const advisoryLockMediaReaper = int64(0xBBC404) // 12304388

// MediaReaper deletes presigned uploads the browser never confirmed.
//
// It runs as its own goroutine with its own ticker rather than riding the
// watchdog's, and that is a coupling decision, not a cost one: one indexed
// empty-result query a minute would be free. The watchdog drives auto-snooze,
// near-turn notification and the tier-availability check — it is the loop where
// a stall costs a customer their turn. An R2 outage, a slow HEAD or a hung
// DELETE must not be able to delay it. The watchdog's failure mode is customers
// not being called; the reaper's is an orphaned object living an extra hour.
type MediaReaper struct {
	db    *pgxpool.Pool
	repo  *repository.MediaRepository
	store objectDeleter
	batch int
}

// objectDeleter is the slice of r2.Store the reaper needs, as an interface so
// tests can drive the failure paths without an R2 endpoint.
type objectDeleter interface {
	Delete(key string) error
}

func NewMediaReaper(db *pgxpool.Pool, store objectDeleter, batch int) *MediaReaper {
	if batch <= 0 {
		batch = 100
	}
	return &MediaReaper{
		db:    db,
		repo:  &repository.MediaRepository{Pool: db},
		store: store,
		batch: batch,
	}
}

func (m *MediaReaper) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *MediaReaper) tick(ctx context.Context) {
	var acquired bool
	err := m.db.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockMediaReaper).Scan(&acquired)
	if err != nil || !acquired {
		return
	}
	defer m.db.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockMediaReaper)

	m.reap(ctx)
}

// reap removes each stale pending upload from R2, then from the database.
//
// That order is load-bearing. The row is the only record that the object exists,
// so deleting it first would strand the object in R2 with nothing left pointing
// at it. Crashing between the two leaves a pending row whose object is already
// gone; r2.Delete treats a 404 as success, so the next tick converges.
//
// An R2 failure never deletes the row — the candidate is left for the next tick.
// Nothing here is fatal: the reaper failing is an orphan living longer, which is
// not worth taking a process down for.
func (m *MediaReaper) reap(ctx context.Context) {
	candidates, err := m.repo.ReapCandidates(ctx, media.ReapAge, m.batch)
	if err != nil {
		log.Printf("MediaReaper: failed to list candidates: %v", err)
		return
	}
	if len(candidates) == 0 {
		return
	}

	var deleted, failed int
	for _, c := range candidates {
		if err := m.store.Delete(c.R2Key); err != nil {
			if errors.Is(err, r2.ErrNotConfigured) {
				log.Printf("MediaReaper: R2 not configured — %d candidates left in place", len(candidates))
				return
			}
			// Transport or 5xx. Keep the row: it is the only handle on the object.
			log.Printf("MediaReaper: R2 delete failed for %s (asset %s), leaving row for next tick: %v",
				c.R2Key, c.ID, err)
			failed++
			continue
		}
		if err := m.repo.DeleteRow(ctx, c.ID); err != nil {
			log.Printf("MediaReaper: object %s deleted but row %s survived; next tick's DELETE 404s and converges: %v",
				c.R2Key, c.ID, err)
			failed++
			continue
		}
		deleted++
	}
	log.Printf("MediaReaper: reaped %d orphaned uploads, %d deferred", deleted, failed)
}
