package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// O3 A3-A7. est_wait_min_minutes / est_wait_max_minutes over the my-status
// handler, which is the only producer of QueueEntryPublic that computes a
// queue-scoped wait. The arithmetic itself is already covered exhaustively by
// repository.TestWaitRange_A7b; what is tested here is that both ends reach the
// wire, that max still equals estimated_wait_minutes, and — A7 — that the two
// producers which cannot compute a range omit the fields instead of faking one.

// waitRangeFixture is one shop with a session, seeded per subtest.
type waitRangeFixture struct {
	tenantID, locationID, sessionID uuid.UUID
}

func seedRangeBarber(t *testing.T, pool *pgxpool.Pool, f waitRangeFixture, tierID *uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, status, is_active, tier_id)
		VALUES ($1, $2, 'Range Barber', $3, 'barber', 'idle', true, $4)`,
		f.tenantID, f.locationID, "+9198"+uuid.NewString()[:9], tierID)
	require.NoError(t, err)
}

// seedRangeEntry appends one waiting entry. token is the magic-link token when
// the entry is the caller's own; pass "" for the entries ahead of it.
func seedRangeEntry(t *testing.T, pool *pgxpool.Pool, f waitRangeFixture, sortKey, duration int, tierID *uuid.UUID, token string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var visitID, entryID uuid.UUID
	var tokenHash *string
	var expires *time.Time
	if token != "" {
		e := time.Now().Add(23 * time.Hour)
		tokenHash, expires = &token, &e
	}
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, status, party_size,
		                    total_duration_minutes, magic_link_token_hash, magic_link_expires_at)
		VALUES ($1, $2, 'walk_in', 'active', 1, $3, $4, $5) RETURNING id`,
		f.tenantID, f.locationID, duration, tokenHash, expires).Scan(&visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries (visit_id, queue_session_id, token_number, state, presence_state,
		                           is_dispatchable, priority_group, sort_key, required_tier_id)
		VALUES ($1, $2, $3, 'waiting', 'arrived', true, 100, $4, $5) RETURNING id`,
		visitID, f.sessionID, sortKey, int64(sortKey), tierID).Scan(&entryID))
	return entryID
}

func TestWaitRangeOverHTTP(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()

	// setupTestServer's own staff member defaults to status 'offline', so every
	// on-shift barber in these cases is one this test seeded deliberately.
	f := waitRangeFixture{tenantID, locationID, seedQueueSession(t, pool, tenantID, locationID)}

	myStatus := func(t *testing.T, token string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/queue/my-status", nil)
		req.Header.Set("X-Session-Token", token)
		rec := httptest.NewRecorder()
		s.GetMyQueueStatus(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body
	}

	// assertRange is A6: wherever both ends are emitted, min <= max and max is
	// exactly estimated_wait_minutes. Applied to every case below, not one.
	assertRange := func(t *testing.T, body map[string]any) (lo, hi float64) {
		t.Helper()
		loV, okLo := body["est_wait_min_minutes"].(float64)
		hiV, okHi := body["est_wait_max_minutes"].(float64)
		require.True(t, okLo && okHi, "both ends must be present: %v", body)
		require.LessOrEqual(t, loV, hiV, "A6: min must never exceed max")
		require.Equal(t, hiV, body["estimated_wait_minutes"], "max must equal estimated_wait_minutes")
		return loV, hiV
	}

	// A5: nothing ahead. Not merely min == max, but both zero.
	t.Run("A5 zero entries ahead is 0/0", func(t *testing.T) {
		seedRangeBarber(t, pool, f, nil)
		token := "range-token-a5"
		seedRangeEntry(t, pool, f, 10, 30, nil, token)
		t.Cleanup(func() { clearRangeQueue(t, pool, f) })

		lo, hi := assertRange(t, myStatus(t, token))
		require.Equal(t, float64(0), lo)
		require.Equal(t, float64(0), hi)
	})

	// A4: unconstrained entry, three barbers on shift, 120 minutes ahead with a
	// 60-minute longest job. Graham: lo = 40, hi = (120 + 60*2)/3 = 80.
	t.Run("A4 multiple barbers spread the range", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			seedRangeBarber(t, pool, f, nil)
		}
		seedRangeEntry(t, pool, f, 1, 60, nil, "")
		seedRangeEntry(t, pool, f, 2, 30, nil, "")
		seedRangeEntry(t, pool, f, 3, 30, nil, "")
		token := "range-token-a4"
		seedRangeEntry(t, pool, f, 10, 30, nil, token)
		t.Cleanup(func() { clearRangeQueue(t, pool, f) })

		body := myStatus(t, token)
		lo, hi := assertRange(t, body)
		require.Equal(t, float64(40), lo)
		require.Equal(t, float64(80), hi)
		require.Less(t, lo, hi, "A4: more than one eligible barber must spread the range")
		require.Equal(t, float64(3), body["position_ahead"])
	})

	// A3: tier-constrained entry whose tier has exactly one barber. A serial
	// queue has no packing uncertainty, so the range collapses to a point.
	t.Run("A3 single-barber tier collapses to a point", func(t *testing.T) {
		var tierID uuid.UUID
		require.NoError(t, pool.QueryRow(context.Background(), `
			INSERT INTO staff_tiers (tenant_id, location_id, name, rank, is_default, is_active)
			VALUES ($1, $2, 'Senior', 1, false, true) RETURNING id`,
			f.tenantID, f.locationID).Scan(&tierID))
		seedRangeBarber(t, pool, f, &tierID) // the only barber in the tier
		seedRangeBarber(t, pool, f, nil)     // untiered, cannot serve this entry
		seedRangeEntry(t, pool, f, 1, 45, &tierID, "")
		token := "range-token-a3"
		seedRangeEntry(t, pool, f, 10, 30, &tierID, token)
		t.Cleanup(func() { clearRangeQueue(t, pool, f) })

		lo, hi := assertRange(t, myStatus(t, token))
		require.Equal(t, lo, hi, "A3: one eligible barber is deterministic")
		require.Equal(t, float64(45), hi)
	})

	// A7: getPublicQueueEntryByID feeds the join 200 and the already-in-queue
	// 409. It reports the entry's own service duration and never calls
	// TierScopedWait, so it has no range to report — and must say nothing rather
	// than echo max into min.
	t.Run("A7 a producer without a range omits both fields", func(t *testing.T) {
		token := "range-token-a7"
		entryID := seedRangeEntry(t, pool, f, 10, 30, nil, token)
		t.Cleanup(func() { clearRangeQueue(t, pool, f) })

		entry, err := s.getPublicQueueEntryByID(context.Background(), entryID)
		require.NoError(t, err)
		require.Nil(t, entry.EstWaitMinMinutes, "A7: must not fabricate a low end")
		require.Nil(t, entry.EstWaitMaxMinutes, "A7: must not fabricate a high end")

		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		_, hasLo := body["est_wait_min_minutes"]
		_, hasHi := body["est_wait_max_minutes"]
		require.False(t, hasLo, "A7: key must be absent from the wire, not null")
		require.False(t, hasHi, "A7: key must be absent from the wire, not null")
		require.Contains(t, body, "estimated_wait_minutes", "the required field still ships")
	})
}

func clearRangeQueue(t *testing.T, pool *pgxpool.Pool, f waitRangeFixture) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DELETE FROM queue_entries WHERE queue_session_id = $1`, f.sessionID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM visits WHERE location_id = $1`, f.locationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM staff_members WHERE location_id = $1 AND name = 'Range Barber'`, f.locationID)
	require.NoError(t, err)
}
