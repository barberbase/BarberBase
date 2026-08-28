package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WaitEstimate is the tier-scoped answer to "how long until my turn".
//
// Both ends are computed and tested; only HiMinutes is serialised today,
// into the existing estimated_wait_minutes field. Under-promising is the
// only safe direction on a queue: a customer told 45 who is called at 35 is
// pleased, and one told 30 who waits 45 believes the shop lied — an
// asymmetry that bites hardest on the tier-constrained customers who paid
// extra specifically to feel looked after.
//
// LoMinutes reaches the wire when est_wait_min_minutes / est_wait_max_minutes
// land in openapi.yaml. See the T3 HTTP Handoff.
type WaitEstimate struct {
	AheadCount int
	LoMinutes  int
	HiMinutes  int
}

// tierScopedWaitQuery answers all of it in one round trip.
//
// COMPETING SET. Eligibility is asymmetric: a tier-T barber serves entries
// requiring T plus every unconstrained entry, while an unconstrained entry is
// served by any barber at all. So X competes with E when X is unconstrained,
// or E is unconstrained, or they require the same tier. When required_tier_id
// is NULL throughout — a shop with no tiers — the middle disjunct is always
// true and the result is identical to the pre-T3 global count.
//
// DIVISOR. On-shift is status <> 'offline', so idle, cutting and break all
// count: a break ends, going offline does not, and a customer waiting on a
// barber who is on tea deserves a longer estimate, not an unservable flag.
// (The auto-snooze gate uses a deliberately different, stricter notion —
// status = 'idle' — because snooze means "a barber is free and waiting on
// you". See checkAutoSnooze.)
//
// Entries already called or in_progress are counted, matching the pre-T3
// query this replaces: the barber's current cut is part of the wait. Their
// remaining duration is approximated by the full duration, which is what the
// old query did too — narrowing that is a separate unit.
const tierScopedWaitQuery = `
WITH e AS (
    SELECT qe.id, qe.queue_session_id, qe.priority_group, qe.sort_key,
           qe.required_tier_id, qs.location_id
    FROM queue_entries qe
    JOIN queue_sessions qs ON qs.id = qe.queue_session_id
    WHERE qe.id = $1
), ahead AS (
    SELECT COUNT(*)                                    AS n,
           COALESCE(SUM(v.total_duration_minutes), 0)  AS total,
           COALESCE(MAX(v.total_duration_minutes), 0)  AS longest
    FROM e
    JOIN queue_entries x ON x.queue_session_id = e.queue_session_id
    JOIN visits v        ON v.id = x.visit_id
    WHERE x.is_dispatchable = true
      AND x.state IN ('waiting', 'called', 'in_progress')
      AND (x.priority_group < e.priority_group
           OR (x.priority_group = e.priority_group AND x.sort_key < e.sort_key))
      AND x.id <> e.id
      AND (x.required_tier_id IS NULL
           OR e.required_tier_id IS NULL
           OR x.required_tier_id = e.required_tier_id)
), barbers AS (
    SELECT COUNT(*) AS n
    FROM e
    JOIN staff_members sm ON sm.location_id = e.location_id
    WHERE sm.is_active = true
      AND sm.status <> 'offline'
      AND (e.required_tier_id IS NULL OR sm.tier_id = e.required_tier_id)
)
SELECT ahead.n, ahead.total, ahead.longest, barbers.n FROM ahead, barbers`

// TierScopedWait returns the ahead count and wait range for one entry, counting
// only the entries competing for the same barbers.
//
// The range derives from Graham's bound on greedy list-scheduling makespan,
// S/n + M(1 - 1/n), which is tight and behaves correctly at both ends:
//
//	lo = ceil(S / n)              perfect packing, every eligible barber free now
//	hi = ceil((S + M*(n-1)) / n)  Graham
//
// At n = 1 the extra term is exactly zero, so lo == hi == S — a serial queue is
// deterministic and has no packing uncertainty to express. At n > 1 the spread is
// M(n-1)/n, which grows toward but never reaches one whole extra service. The
// obvious alternative, hi = lo + M, is the one-barber intuition generalised
// wrongly: it adds a full M at every n, overstating a four-barber shop by M/n.
//
// n = 0 (every barber in the required tier offline) is clamped to 1 rather than
// returning an infinity. That case is real, but the honest signal for it is the
// tier_unavailable warning the watchdog writes, not an absurd number of minutes.
func TierScopedWait(ctx context.Context, pool *pgxpool.Pool, entryID uuid.UUID) (WaitEstimate, error) {
	var aheadCount, total, longest, barbers int
	err := pool.QueryRow(ctx, tierScopedWaitQuery, entryID).
		Scan(&aheadCount, &total, &longest, &barbers)
	if err != nil {
		return WaitEstimate{}, err
	}

	lo, hi := waitRange(total, longest, barbers)
	return WaitEstimate{AheadCount: aheadCount, LoMinutes: lo, HiMinutes: hi}, nil
}

// waitRange is the arithmetic, split out so it can be table-tested without a
// database. barbers <= 0 is clamped to 1; see TierScopedWait for why.
func waitRange(total, longest, barbers int) (lo, hi int) {
	n := barbers
	if n < 1 {
		n = 1
	}
	return ceilDiv(total, n), ceilDiv(total+longest*(n-1), n)
}

// ceilDiv rounds up. Integer-only: an estimate expressed in whole minutes must
// never round down, or the range stops being an upper bound.
func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
