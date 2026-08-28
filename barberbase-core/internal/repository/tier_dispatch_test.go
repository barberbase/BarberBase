package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedBarber adds one barber at the fixture's location, optionally tiered.
func seedBarber(t *testing.T, pool *pgxpool.Pool, f tierFixture, name, status string, tierID *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, status, tier_id)
		VALUES ($1, $2, $3, $4, 'barber', $5, $6) RETURNING id`,
		f.tenantID, f.locID, name, "+9198"+uuid.NewString()[:9], status, tierID).Scan(&id))
	return id
}

// seedWaitEntry appends one waiting entry with an explicit sort_key and duration.
func seedWaitEntry(t *testing.T, pool *pgxpool.Pool, f tierFixture, sortKey, durationMinutes int, tierID *uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var visitID, entryID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', $3) RETURNING id`,
		f.tenantID, f.locID, durationMinutes).Scan(&visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state,
			 is_dispatchable, priority_group, sort_key, required_tier_id)
		VALUES ($1, $2, $3, 'waiting', 'arrived', true, 100, $4, $5) RETURNING id`,
		visitID, f.sessionID, sortKey, int64(sortKey), tierID).Scan(&entryID))
	return entryID
}

// A6 — ahead_count for a tier-constrained entry counts only competing entries.
//
// Eligibility is asymmetric, so "competing" is not "same tier". A Senior barber
// can also take unconstrained work, so unconstrained entries ahead DO delay a
// Senior-required customer; entries reserved for a different tier do not.
func TestTierScopedWait_A6_AheadCountIsCompetingSetOnly(t *testing.T) {
	pool := migratedPool(t)
	f := seedTierFixture(t, pool)
	senior, err := insertTier(pool, f, "Senior", 20, false, true)
	require.NoError(t, err)
	junior, err := insertTier(pool, f, "Junior", 10, true, true)
	require.NoError(t, err)
	seedBarber(t, pool, f, "Sen", "idle", &senior)

	// Queue ahead: 2 unconstrained, 3 Junior-only, 1 Senior-only.
	seedWaitEntry(t, pool, f, 1, 30, nil)
	seedWaitEntry(t, pool, f, 2, 30, &junior)
	seedWaitEntry(t, pool, f, 3, 30, &junior)
	seedWaitEntry(t, pool, f, 4, 30, nil)
	seedWaitEntry(t, pool, f, 5, 30, &junior)
	seedWaitEntry(t, pool, f, 6, 30, &senior)
	me := seedWaitEntry(t, pool, f, 7, 30, &senior)

	est, err := TierScopedWait(context.Background(), pool, me)
	require.NoError(t, err)

	// 6 entries sit ahead in raw dispatch order; only 3 compete for a Senior
	// (2 unconstrained + 1 Senior-required). The 3 Junior-only entries do not.
	require.Equal(t, 3, est.AheadCount, "A6: the 3 Junior-only entries must not count against a Senior-required customer")

	// The same queue, seen by an unconstrained entry: every barber can serve it,
	// so everything ahead competes.
	plain := seedWaitEntry(t, pool, f, 8, 30, nil)
	estPlain, err := TierScopedWait(context.Background(), pool, plain)
	require.NoError(t, err)
	require.Equal(t, 7, estPlain.AheadCount, "A6: an unconstrained entry competes with the whole queue")
}

// A7 — a single-barber tier is a serial queue and its estimate must be
// materially larger than the pooled estimate at the same queue position.
func TestTierScopedWait_A7_SingleBarberTierIsSerial(t *testing.T) {
	pool := migratedPool(t)
	f := seedTierFixture(t, pool)
	senior, err := insertTier(pool, f, "Senior", 20, false, true)
	require.NoError(t, err)

	// One Senior; four barbers on shift in total.
	seedBarber(t, pool, f, "Sen", "idle", &senior)
	for i := 0; i < 3; i++ {
		seedBarber(t, pool, f, fmt.Sprintf("Pooled%d", i), "idle", nil)
	}

	// Four 40-minute entries ahead, all Senior-required, then two entries at the
	// same position: one Senior-required, one unconstrained.
	for i := 1; i <= 4; i++ {
		seedWaitEntry(t, pool, f, i, 40, &senior)
	}
	tiered := seedWaitEntry(t, pool, f, 5, 40, &senior)
	pooled := seedWaitEntry(t, pool, f, 6, 40, nil)

	ctx := context.Background()
	estTiered, err := TierScopedWait(ctx, pool, tiered)
	require.NoError(t, err)
	estPooled, err := TierScopedWait(ctx, pool, pooled)
	require.NoError(t, err)

	// Serial: 4 × 40 with one barber. n = 1 collapses the range.
	require.Equal(t, 160, estTiered.HiMinutes)
	require.Equal(t, 160, estTiered.LoMinutes, "A7: one barber is deterministic — no packing spread")

	// Pooled sees 5 entries ahead across 4 barbers: 200/4 = 50 low,
	// (200 + 40*3)/4 = 80 high.
	require.Equal(t, 50, estPooled.LoMinutes)
	require.Equal(t, 80, estPooled.HiMinutes)

	// Material, not marginal: twice the pooled high end and more than three
	// times its low end, for a customer standing at the same queue position.
	require.GreaterOrEqual(t, estTiered.HiMinutes, estPooled.HiMinutes*2,
		"A7: a single-barber tier must read materially longer than the pooled queue")
	require.Greater(t, estTiered.LoMinutes, estPooled.LoMinutes*3, "A7: low end too")
}

// A7b — the range arithmetic, table-driven and database-free.
func TestWaitRange_A7b(t *testing.T) {
	cases := []struct {
		name              string
		total, longest, n int
		wantLo, wantHi    int
	}{
		{"nothing ahead", 0, 0, 4, 0, 0},
		{"nothing ahead, no barbers", 0, 0, 0, 0, 0},
		{"single barber is deterministic", 160, 40, 1, 160, 160},
		{"four barbers, Graham spread", 200, 40, 4, 50, 80},
		{"two barbers", 100, 60, 2, 50, 80},
		{"tier gone dark clamps to serial", 90, 30, 0, 90, 90},
		{"rounds up, never down", 100, 0, 3, 34, 34},
		{"uniform durations still spread", 120, 30, 4, 30, 53},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lo, hi := waitRange(c.total, c.longest, c.n)
			require.Equal(t, c.wantLo, lo)
			require.Equal(t, c.wantHi, hi)
			require.GreaterOrEqual(t, hi, lo, "A7b: hi must never fall below lo")
		})
	}

	// Exhaustive: hi >= lo for every shape, and one barber always collapses.
	for total := 0; total <= 300; total += 7 {
		for longest := 0; longest <= total; longest += 11 {
			for n := 0; n <= 8; n++ {
				lo, hi := waitRange(total, longest, n)
				require.GreaterOrEqual(t, hi, lo, "A7b: total=%d longest=%d n=%d", total, longest, n)
				if n <= 1 {
					require.Equal(t, lo, hi, "A7b: n=%d must be deterministic", n)
				}
			}
		}
	}
}

// A8 companion at the repository layer: a tier with nobody on shift clamps to a
// serial estimate rather than dividing by zero. The honest signal for that case
// is the watchdog's tier_unavailable warning, not the minute count.
func TestTierScopedWait_TierGoneDarkClampsNotPanics(t *testing.T) {
	pool := migratedPool(t)
	f := seedTierFixture(t, pool)
	senior, err := insertTier(pool, f, "Senior", 20, false, true)
	require.NoError(t, err)
	seedBarber(t, pool, f, "Sen", "offline", &senior) // on the roster, not on shift

	seedWaitEntry(t, pool, f, 1, 45, &senior)
	me := seedWaitEntry(t, pool, f, 2, 45, &senior)

	est, err := TierScopedWait(context.Background(), pool, me)
	require.NoError(t, err)
	require.Equal(t, 1, est.AheadCount)
	require.Equal(t, 45, est.LoMinutes)
	require.Equal(t, 45, est.HiMinutes)
}

// A break is on shift; offline is not. The two states differ by one word in the
// SQL, so assert the boundary rather than trusting it.
func TestTierScopedWait_BreakCountsAsOnShift(t *testing.T) {
	pool := migratedPool(t)
	f := seedTierFixture(t, pool)
	senior, err := insertTier(pool, f, "Senior", 20, false, true)
	require.NoError(t, err)
	seedBarber(t, pool, f, "OnBreak", "break", &senior)
	seedBarber(t, pool, f, "Cutting", "cutting", &senior)

	for i := 1; i <= 4; i++ {
		seedWaitEntry(t, pool, f, i, 30, &senior)
	}
	me := seedWaitEntry(t, pool, f, 5, 30, &senior)

	est, err := TierScopedWait(context.Background(), pool, me)
	require.NoError(t, err)
	// Two on-shift Seniors (break + cutting), 120 minutes ahead: 120/2 = 60.
	require.Equal(t, 60, est.LoMinutes, "a barber on break still counts — a break ends, offline does not")
}

// ─── A13 ────────────────────────────────────────────────────────────────────

// dispatchSQL is the REAL Call Next select from queue.go:225-261, ORDER BY and
// presence_state filter included. %s is the tier predicate — empty for the HEAD
// capture, populated for the after capture — so both plans come from one string
// and cannot drift apart.
const dispatchSQL = `
SELECT id, visit_id, customer_id, session_channel, token_number
FROM queue_entries
WHERE queue_session_id = $1
  AND state = 'waiting'
  AND is_dispatchable = true
  AND presence_state = 'arrived'
  %s%s
ORDER BY priority_group ASC, sort_key ASC, token_number ASC
LIMIT 1
FOR UPDATE`

// TestDispatchPlan_A13_TierPredicateIsFreeIsA13 captures the plan for the real
// dispatch query at 50k rows, with and without the tier predicate, in all three
// routing modes. The assertion is differential: T3's claim is "the tier
// predicate costs nothing", which is a comparison, not an absolute. An absolute
// "no Sort" would fail T3 for a plan shape that predates it — and one does:
// token_number is not in idx_queue_dispatch, so HEAD already carries an
// Incremental Sort above the ordered index walk.
func TestDispatchPlan_A13_TierPredicateAddsNoNode(t *testing.T) {
	if testing.Short() {
		t.Skip("A13 seeds 50k rows; skipped under -short")
	}
	pool := migratedPool(t)
	ctx := context.Background()
	f := seedTierFixture(t, pool)

	senior, err := insertTier(pool, f, "Senior", 20, false, true)
	require.NoError(t, err)
	barberID := seedBarber(t, pool, f, "Sen", "idle", &senior)
	// A realistic staff table, not two rows: with a toy table the tier lookup
	// seq-scans, which says nothing about production.
	for i := 0; i < 200; i++ {
		seedBarber(t, pool, f, fmt.Sprintf("B%d", i), "offline", nil)
	}

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET statement_timeout = '180s'`)
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `
		WITH v AS (
			INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
			SELECT $1, $2, 'walk_in', 30 FROM generate_series(1, 50000)
			RETURNING id
		), numbered AS (SELECT id, row_number() OVER () AS n FROM v)
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state,
			 is_dispatchable, requested_barber_id, priority_group, sort_key, required_tier_id)
		SELECT id, $3, n, 'waiting',
		       CASE WHEN n % 2 = 0 THEN 'arrived' ELSE 'remote' END,
		       true,
		       CASE WHEN n % 7 = 0 THEN $4::uuid ELSE NULL END,
		       100, n,
		       CASE WHEN n % 4 = 0 THEN $5::uuid ELSE NULL END
		FROM numbered`, f.tenantID, f.locID, f.sessionID, barberID, senior)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `ANALYZE queue_entries`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `ANALYZE staff_members`)
	require.NoError(t, err)

	const tierPred = `
  AND (required_tier_id IS NULL
       OR required_tier_id = (SELECT tier_id FROM staff_members WHERE id = $2))`

	explain := func(t *testing.T, sql string, args ...any) string {
		t.Helper()
		// FOR UPDATE takes real row locks; roll them back rather than holding
		// them for the rest of the test.
		tx, err := conn.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) "+sql, args...)
		require.NoError(t, err)
		var b strings.Builder
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			b.WriteString(line + "\n")
		}
		rows.Close()
		require.NoError(t, rows.Err())
		return b.String()
	}

	// The node types a plan is allowed to contain. Comparing the multiset of
	// node names is what "identical shape" means here — costs and row counts
	// legitimately differ between the two runs.
	nodes := func(plan string) []string {
		var out []string
		for _, line := range strings.Split(plan, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "->") && !strings.HasPrefix(trimmed, "Limit") {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "-> ")
			if i := strings.Index(trimmed, " (actual"); i > 0 {
				trimmed = trimmed[:i]
			}
			out = append(out, trimmed)
		}
		return out
	}

	for _, mode := range []struct {
		name   string
		filter string
	}{
		{"pooled", ""},
		{"hybrid", "\n  AND (requested_barber_id = $2 OR requested_barber_id IS NULL)"},
		{"barber_specific", "\n  AND requested_barber_id = $2"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			// HEAD: same query, same $2 binding, no tier predicate. Pooled at HEAD
			// had no $2 at all, so bind it in a way the planner ignores.
			headFilter := mode.filter
			if headFilter == "" {
				headFilter = "\n  AND ($2::uuid IS NOT NULL OR TRUE)"
			}
			head := explain(t, fmt.Sprintf(dispatchSQL, headFilter, ""), f.sessionID, barberID)
			after := explain(t, fmt.Sprintf(dispatchSQL, mode.filter, tierPred), f.sessionID, barberID)

			t.Logf("A13 %s HEAD plan:\n%s", mode.name, head)
			t.Logf("A13 %s AFTER plan:\n%s", mode.name, after)

			require.Contains(t, after, "Index Scan using idx_queue_dispatch",
				"A13: the ordered index walk must survive the tier predicate")
			require.NotContains(t, after, "Seq Scan on queue_entries",
				"A13: a sequential scan here would sink Call Next under load")
			require.Contains(t, after, "required_tier_id",
				"A13: the tier predicate must be applied as a per-row Filter")

			headNodes, afterNodes := nodes(head), nodes(after)
			// The InitPlan that resolves the barber's tier is the one node the
			// after-plan legitimately adds; it is the tier LOOKUP, not the
			// predicate, and its alternative — a second round trip inside the
			// dispatch transaction — is strictly worse and invisible to EXPLAIN.
			//
			// Which access method the planner picks for a single-PK lookup on a
			// small table is its business and flips with table size (a seq scan
			// on a few hundred rows, staff_members_pkey beyond that), so assert
			// that the lookup is bounded to one row rather than pinning the node
			// type and getting a test that fails on a bigger fixture.
			var filtered []string
			var lookups int
			for _, n := range afterNodes {
				if strings.Contains(n, "staff_members") {
					lookups++
					continue
				}
				filtered = append(filtered, n)
			}
			require.Equal(t, 1, lookups, "A13: exactly one tier lookup, evaluated once as an InitPlan")
			require.Contains(t, after, "InitPlan 1 (returns $0)",
				"A13: the tier must resolve once per query, not once per row")
			require.Regexp(t, `on staff_members \(actual [^)]*rows=1 loops=1\)`, after,
				"A13: the tier lookup must return exactly one row, once")
			require.Equal(t, headNodes, filtered,
				"A13: %s — the tier predicate must add no node to the queue_entries plan", mode.name)

			if strings.Contains(head, "Incremental Sort") {
				t.Logf("A13 finding (%s): HEAD already carries an Incremental Sort. "+
					"token_number is in the ORDER BY but not in idx_queue_dispatch. "+
					"Predates T3; recorded, not fixed here.", mode.name)
			}
		})
	}
}
