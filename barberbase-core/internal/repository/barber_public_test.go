package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A1/A3 at the source: the mask itself, no database needed.
func TestPublicAvailability(t *testing.T) {
	require.Equal(t, "available", PublicAvailability("idle"))
	require.Equal(t, "busy", PublicAvailability("cutting"))
	require.Equal(t, "busy", PublicAvailability("break"),
		"A3: a break must be indistinguishable from cutting")
	require.Equal(t, PublicAvailability("cutting"), PublicAvailability("break"),
		"A3: a customer must not be able to tell them apart")
	require.Equal(t, "", PublicAvailability("offline"), "A2: offline is omitted, not reported")
	require.Equal(t, "", PublicAvailability("something_a_later_migration_adds"),
		"an unknown status must not leak through raw")
}

// A7 at the source: the divisor is what makes a lone barber in a tier read
// longer than one of four in a pool at the same queue depth.
func TestPublicBarberWaits(t *testing.T) {
	senior, junior := uuid.New(), uuid.New()
	sole := PublicBarberRow{ID: uuid.New(), TierID: &senior}
	pooledA := PublicBarberRow{ID: uuid.New(), TierID: &junior}
	pooledB := PublicBarberRow{ID: uuid.New(), TierID: &junior}
	pooledC := PublicBarberRow{ID: uuid.New(), TierID: &junior}
	barbers := []PublicBarberRow{sole, pooledA, pooledB, pooledC}

	t.Run("A7 a tier of one carries its shared work alone", func(t *testing.T) {
		load := []QueueLoadRow{
			{RequiredTierID: &senior, DurationMinutes: 30},
			{RequiredTierID: &senior, DurationMinutes: 30},
			{RequiredTierID: &junior, DurationMinutes: 30},
			{RequiredTierID: &junior, DurationMinutes: 30},
		}
		w := PublicBarberWaits(barbers, load)
		require.Equal(t, 60, w[sole.ID], "60 minutes of senior work, one senior")
		require.Equal(t, 20, w[pooledA.ID], "60 minutes of junior work split three ways")
		require.Greater(t, w[sole.ID], w[pooledA.ID],
			"A7: the same queue depth must read materially longer for a tier of one")
	})

	t.Run("work naming a barber is theirs alone", func(t *testing.T) {
		id := pooledA.ID
		load := []QueueLoadRow{
			{AssignedBarberID: &id, DurationMinutes: 45},
			{RequestedBarberID: &id, DurationMinutes: 15},
		}
		w := PublicBarberWaits(barbers, load)
		require.Equal(t, 60, w[pooledA.ID], "counted in full — nobody else will take it")
		require.Equal(t, 0, w[pooledB.ID], "and it is not their problem")
		require.Equal(t, 0, w[sole.ID])
	})

	t.Run("unconstrained work is shared within a tier", func(t *testing.T) {
		load := []QueueLoadRow{{DurationMinutes: 100}}
		w := PublicBarberWaits(barbers, load)
		require.Equal(t, 100, w[sole.ID], "alone in the tier, so undivided")
		require.Equal(t, 34, w[pooledA.ID], "ceil(100/3) — rounds up, never down")
	})

	t.Run("an empty queue is zero everywhere", func(t *testing.T) {
		for _, v := range PublicBarberWaits(barbers, nil) {
			require.Equal(t, 0, v)
		}
	})

	t.Run("untiered barbers pool together", func(t *testing.T) {
		a := PublicBarberRow{ID: uuid.New()}
		b := PublicBarberRow{ID: uuid.New()}
		w := PublicBarberWaits([]PublicBarberRow{a, b}, []QueueLoadRow{{DurationMinutes: 60}})
		require.Equal(t, 30, w[a.ID])
		require.Equal(t, 30, w[b.ID])
	})
}
