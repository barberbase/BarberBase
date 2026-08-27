package realtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestBroadcastUnsubscribeRace is A1: the send-on-closed-channel race.
// Before the fix, Unsubscribe closed the channel while Broadcast held a copy of
// the subscriber list taken under the mutex and released it before sending — so a
// send could land on a closed channel and panic in the sender's goroutine, which
// for StartHeartbeats is fatal. Run with -race.
func TestBroadcastUnsubscribeRace(t *testing.T) {
	duration := 10 * time.Second
	if testing.Short() {
		duration = time.Second
	}

	m := NewManager()
	locations := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	// Broadcasters.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				m.Broadcast(locations[n%len(locations)], SSEEvent{Type: "queue_changed", QueueVersion: n})
			}
		}(i)
	}
	// Subscribers churning in a tight loop.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			loc := locations[n%len(locations)]
			for time.Now().Before(deadline) {
				ch := m.Subscribe(loc)
				select {
				case <-ch:
				default:
				}
				m.Unsubscribe(loc, ch)
			}
		}(i)
	}
	wg.Wait()

	for _, loc := range locations {
		require.Empty(t, subscriberCount(m, loc), "all subscribers must be unregistered")
	}
	require.Zero(t, m.LocationCount(), "no location entries may leak")
}

// TestUnsubscribeLeavesChannelOpen pins the root-cause fix: Unsubscribe must
// unregister without closing, otherwise the race above comes straight back.
func TestUnsubscribeLeavesChannelOpen(t *testing.T) {
	m := NewManager()
	loc := uuid.NewString()

	ch := m.Subscribe(loc)
	m.Unsubscribe(loc, ch)

	select {
	case _, ok := <-ch:
		require.True(t, ok, "channel must not be closed by Unsubscribe")
		t.Fatal("unexpected value on an unsubscribed channel")
	default:
		// Open and empty: correct.
	}

	// Unregistered, so a later broadcast must not reach it.
	m.Broadcast(loc, SSEEvent{Type: "queue_changed", QueueVersion: 1})
	select {
	case e := <-ch:
		t.Fatalf("unsubscribed channel still received %v", e)
	default:
	}
	require.Zero(t, m.LocationCount())
}

// TestLocationCountReturnsToBaseline is the A2 map-leak assertion.
func TestLocationCountReturnsToBaseline(t *testing.T) {
	m := NewManager()
	require.Zero(t, m.LocationCount())

	type entry struct {
		loc string
		ch  chan SSEEvent
	}
	var open []entry
	for i := 0; i < 5; i++ {
		loc := uuid.NewString()
		for j := 0; j < 3; j++ {
			open = append(open, entry{loc, m.Subscribe(loc)})
		}
	}
	require.Equal(t, 5, m.LocationCount())

	for _, e := range open {
		m.Unsubscribe(e.loc, e.ch)
	}
	require.Zero(t, m.LocationCount(), "every location key must be deleted")
}

// TestPerLocationCap is A6 at the hub level: the cap refuses, and a disconnect
// frees the slot.
func TestPerLocationCap(t *testing.T) {
	t.Setenv("SSE_MAX_CONNS_PER_LOCATION", "3")
	m := NewManager()
	loc := uuid.NewString()

	var chans []chan SSEEvent
	for i := 0; i < 3; i++ {
		ch, ok := m.TrySubscribe(loc)
		require.True(t, ok, "subscriber %d must be admitted", i)
		chans = append(chans, ch)
	}

	ch, ok := m.TrySubscribe(loc)
	require.False(t, ok, "the 4th subscriber must be refused")
	require.Nil(t, ch)
	require.Equal(t, 3, subscriberCount(m, loc), "a refusal must not register anything")

	m.Unsubscribe(loc, chans[0])
	ch, ok = m.TrySubscribe(loc)
	require.True(t, ok, "a freed slot must be reusable")
	require.NotNil(t, ch)

	// Subscribe bypasses the cap deliberately — background jobs cannot fail.
	require.NotNil(t, m.Subscribe(loc))
	require.Equal(t, 4, subscriberCount(m, loc))
}

// TestTunableDefaultsAndOverrides is A4 plus the D2 bound: defaults are the
// previous hardcoded values, and each env var is honoured.
func TestTunableDefaultsAndOverrides(t *testing.T) {
	def := NewManager()
	require.Equal(t, 30*time.Second, def.heartbeat, "default heartbeat must stay 30s")
	require.Equal(t, 15*time.Minute, def.MaxLifetime())
	require.Equal(t, 50, def.maxPerLocation)

	for _, bad := range []string{"", "0", "-5", "abc"} {
		t.Setenv("SSE_HEARTBEAT_SECONDS", bad)
		require.Equal(t, 30*time.Second, NewManager().heartbeat, "%q must fall back to the default", bad)
	}

	t.Setenv("SSE_HEARTBEAT_SECONDS", "1")
	t.Setenv("SSE_MAX_CONNECTION_SECONDS", "2")
	t.Setenv("SSE_MAX_CONNS_PER_LOCATION", "7")
	m := NewManager()
	require.Equal(t, time.Second, m.heartbeat)
	require.Equal(t, 2*time.Second, m.MaxLifetime())
	require.Equal(t, 7, m.maxPerLocation)
}

// TestHeartbeatHonoursInterval is A4 end to end: heartbeats actually arrive at
// the configured cadence, not the 30s default.
func TestHeartbeatHonoursInterval(t *testing.T) {
	t.Setenv("SSE_HEARTBEAT_SECONDS", "1")
	m := NewManager()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartHeartbeats(ctx)

	loc := uuid.NewString()
	ch := m.Subscribe(loc)
	defer m.Unsubscribe(loc, ch)
	m.Broadcast(loc, SSEEvent{Type: "queue_changed", LocationID: loc, QueueVersion: 9})
	<-ch // drain the mutation event

	for i := 0; i < 2; i++ {
		select {
		case e := <-ch:
			require.Equal(t, "heartbeat", e.Type)
			require.Equal(t, 9, e.QueueVersion, "heartbeat carries the last-known version")
			require.Empty(t, e.LocationID, "heartbeat omits location_id")
		case <-time.After(3 * time.Second):
			t.Fatalf("heartbeat %d did not arrive at the 1s interval", i)
		}
	}
}

func subscriberCount(m *Manager, locationID string) int {
	val, ok := m.subs.Load(locationID)
	if !ok {
		return 0
	}
	loc := val.(*locationSubs)
	loc.mu.Lock()
	defer loc.mu.Unlock()
	return len(loc.list)
}
