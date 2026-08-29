package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PublicAvailability masks the internal staff status for customer-facing use.
//
// staff_members.status is an operational enum the shop runs on: idle, cutting,
// break, offline. A customer needs one bit of it — can this person cut my hair
// now — and is owed nothing more. "On break" is a fact about someone's day, and
// the barber standing beside the customer holding the screen did not agree to
// broadcast it.
//
// offline maps to "" and the caller omits the barber entirely: an offline
// barber is not a slow choice, they are not a choice.
func PublicAvailability(status string) string {
	switch status {
	case "idle":
		return "available"
	case "cutting", "break":
		return "busy"
	default: // offline, or anything a later migration adds
		return ""
	}
}

// PublicBarberRow is one barber as the public endpoint sees them. No phone
// number, no role, no internal status — if a field is not here, it cannot leak.
type PublicBarberRow struct {
	ID           uuid.UUID
	Name         string
	Availability string // already masked
	TierID       *uuid.UUID
	TierName     *string
	TierRank     *int
	AvatarKey    *string // r2_key; the handler joins it to the public base URL
}

// ListPublicBarbers returns the on-shift barbers at a location, in name order.
//
// Offline barbers are excluded in SQL rather than filtered later, so there is no
// path on which one reaches a response by accident.
//
// Indexes: idx_staff_location leads on (tenant_id, location_id) partial over
// is_active; the tier join is by primary key; the avatar lookup rides
// idx_media_assets_one_avatar, a unique partial index on exactly this predicate.
func ListPublicBarbers(ctx context.Context, pool *pgxpool.Pool, locationID uuid.UUID) ([]PublicBarberRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT sm.id, sm.name, sm.status,
		       st.id, st.name, st.rank,
		       ma.r2_key
		FROM staff_members sm
		LEFT JOIN staff_tiers st ON st.id = sm.tier_id AND st.is_active
		LEFT JOIN media_assets ma ON ma.staff_member_id = sm.id
		     AND ma.purpose = 'staff_avatar' AND ma.status = 'ready'
		WHERE sm.location_id = $1 AND sm.is_active = true AND sm.status <> 'offline'
		ORDER BY sm.name`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PublicBarberRow{}
	for rows.Next() {
		var b PublicBarberRow
		var status string
		if err := rows.Scan(&b.ID, &b.Name, &status, &b.TierID, &b.TierName, &b.TierRank, &b.AvatarKey); err != nil {
			return nil, err
		}
		b.Availability = PublicAvailability(status)
		out = append(out, b)
	}
	return out, rows.Err()
}

// QueueLoadRow is one piece of work already in the queue, reduced to what a
// per-barber wait needs: who it is earmarked for, and how long it takes.
type QueueLoadRow struct {
	AssignedBarberID  *uuid.UUID
	RequestedBarberID *uuid.UUID
	RequiredTierID    *uuid.UUID
	DurationMinutes   int
}

// LoadQueueForWait reads today's outstanding work at a location in one query.
//
// Same set TierScopedWait counts: dispatchable entries that are waiting, called
// or in progress. A barber's current cut is part of the wait, approximated by
// its full duration — narrowing that is the same separate unit T3 named.
func LoadQueueForWait(ctx context.Context, pool *pgxpool.Pool, locationID uuid.UUID) ([]QueueLoadRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT qe.assigned_barber_id, qe.requested_barber_id, qe.required_tier_id,
		       COALESCE(v.total_duration_minutes, 0)
		FROM queue_entries qe
		JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		JOIN visits v          ON v.id = qe.visit_id
		WHERE qs.location_id = $1
		  AND qs.status IN ('active', 'ending')
		  AND qe.is_dispatchable = true
		  AND qe.state IN ('waiting', 'called', 'in_progress')`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QueueLoadRow{}
	for rows.Next() {
		var r QueueLoadRow
		if err := rows.Scan(&r.AssignedBarberID, &r.RequestedBarberID, &r.RequiredTierID, &r.DurationMinutes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PublicBarberWaits estimates, per barber, the minutes before they could start a
// NEW customer. Pure: no database, so the rule is testable on its own.
//
// This is a different question from TierScopedWait, which prices an entry that
// already exists and knows its own position. A customer choosing a barber has no
// entry and no position yet, so the estimate is the work standing between them
// and that barber's next free moment:
//
//	exclusive — entries assigned to or requesting this barber, counted in full
//	            because nobody else will take them
//	shared    — unclaimed entries this barber is eligible for, divided by the
//	            barbers who share that eligibility
//
// A7 falls out of the divisor: a barber alone in a tier carries that tier's
// shared work by themselves, while a pooled shop splits the same queue N ways.
//
// ponytail: eligibility is "same tier id, nil included", one divisor per barber.
// It slightly overstates a senior's wait in a shop where seniors also absorb
// unconstrained work. Model partial eligibility when a shop complains the
// number is pessimistic — pessimistic is the safe direction for a wait.
func PublicBarberWaits(barbers []PublicBarberRow, load []QueueLoadRow) map[uuid.UUID]int {
	sameTier := func(a, b *uuid.UUID) bool {
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		return *a == *b
	}

	// How many on-shift barbers share each tier, so shared work has a divisor.
	peers := map[uuid.UUID]int{}
	for _, b := range barbers {
		n := 0
		for _, other := range barbers {
			if sameTier(b.TierID, other.TierID) {
				n++
			}
		}
		peers[b.ID] = n
	}

	waits := make(map[uuid.UUID]int, len(barbers))
	for _, b := range barbers {
		exclusive, shared := 0, 0
		for _, e := range load {
			claimed := e.AssignedBarberID != nil || e.RequestedBarberID != nil
			switch {
			case (e.AssignedBarberID != nil && *e.AssignedBarberID == b.ID) ||
				(e.RequestedBarberID != nil && *e.RequestedBarberID == b.ID):
				exclusive += e.DurationMinutes
			case claimed:
				// Someone else's customer; not this barber's problem.
			case e.RequiredTierID == nil || (b.TierID != nil && *e.RequiredTierID == *b.TierID):
				shared += e.DurationMinutes
			}
		}
		waits[b.ID] = exclusive + ceilDiv(shared, peers[b.ID])
	}
	return waits
}
