package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// SSEEvent is the payload broadcast to all subscribers for a location.
// Type: "queue_changed" | "heartbeat"
// LocationID is omitted on heartbeat (omitempty).
// QueueVersion is always present; clients compare to their local version.
type SSEEvent struct {
	Type         string `json:"type"`
	LocationID   string `json:"location_id,omitempty"`
	QueueVersion int    `json:"queue_version"`
}

// MarshalSSE serialises an SSEEvent into the SSE wire format:
//
//	event: <Type>\ndata: <JSON>\n\n
func (e SSEEvent) MarshalSSE() ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	sb := fmt.Sprintf("event: %s\ndata: %s\n\n", e.Type, string(data))
	return []byte(sb), nil
}

// Manager fans out SSE events to all connected clients keyed by location_id (UUID string).
// sync.Map is the only concurrency primitive — no Redis, no external broker.
// The Manager is safe for concurrent use by all goroutines.
type Manager struct {
	// subs: locationID (string) → *locationSubs
	subs sync.Map
	// latestVersions: locationID (string) → int
	// Updated on every Broadcast call. Used by heartbeat to send last-known version.
	latestVersions sync.Map

	// Tunables, read from the environment once in NewManager. Read-only after
	// construction, so no lock. Defaults preserve the previous hardcoded behaviour.
	heartbeat      time.Duration // SSE_HEARTBEAT_SECONDS, default 30s
	maxLifetime    time.Duration // SSE_MAX_CONNECTION_SECONDS, default 15m
	maxPerLocation int           // SSE_MAX_CONNS_PER_LOCATION, default 50
}

type locationSubs struct {
	mu   sync.Mutex
	list []*sub
}

type sub struct {
	ch chan SSEEvent
}

// NewManager constructs a Manager. Call StartHeartbeats(ctx) separately.
func NewManager() *Manager {
	return &Manager{
		heartbeat:      envSeconds("SSE_HEARTBEAT_SECONDS", 30*time.Second),
		maxLifetime:    envSeconds("SSE_MAX_CONNECTION_SECONDS", 15*time.Minute),
		maxPerLocation: envInt("SSE_MAX_CONNS_PER_LOCATION", 50),
	}
}

// envSeconds reads an integer number of seconds from the environment.
// A missing, unparseable, or non-positive value falls back to def.
func envSeconds(key string, def time.Duration) time.Duration {
	if n := envInt(key, 0); n > 0 {
		return time.Duration(n) * time.Second
	}
	return def
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// MaxLifetime is the bound after which the SSE handler closes the connection and
// the client reconnects. Unbounded connections on a 1GB droplet are a slow leak.
func (m *Manager) MaxLifetime() time.Duration { return m.maxLifetime }

// LocationCount reports how many locations currently have at least one
// subscriber. Used to prove subscriber map entries do not leak.
func (m *Manager) LocationCount() int {
	n := 0
	m.subs.Range(func(_, _ any) bool { n++; return true })
	return n
}

// Subscribe registers a new client for locationID, ignoring the per-location cap.
// Retained for callers that cannot fail (background jobs, tests); real client
// connections go through TrySubscribe.
func (m *Manager) Subscribe(locationID string) chan SSEEvent {
	ch, _ := m.subscribe(locationID, false)
	return ch
}

// TrySubscribe registers a new client for locationID, refusing once the location
// is at SSE_MAX_CONNS_PER_LOCATION. Returns (nil, false) when refused.
//
// Returns a buffered channel (capacity 16). Non-blocking broadcast drops on overflow;
// client refetches on reconnect — this is intentional (SSE is notification-only).
//
// The channel is never closed. Unsubscribe only unregisters it; closing it would
// race with an in-flight Broadcast that copied the subscriber list before the
// unsubscribe and would panic in the sender's goroutine.
func (m *Manager) TrySubscribe(locationID string) (chan SSEEvent, bool) {
	return m.subscribe(locationID, true)
}

func (m *Manager) subscribe(locationID string, enforceCap bool) (chan SSEEvent, bool) {
	ch := make(chan SSEEvent, 16)
	s := &sub{ch: ch}

	for {
		// Load or store a new locationSubs structure.
		val, _ := m.subs.LoadOrStore(locationID, &locationSubs{})
		loc := val.(*locationSubs)

		loc.mu.Lock()
		// Double check if this loc is still registered in the map (to avoid race with deletion in Unsubscribe)
		if val2, ok := m.subs.Load(locationID); ok && val2 == loc {
			if enforceCap && m.maxPerLocation > 0 && len(loc.list) >= m.maxPerLocation {
				// The entry is non-empty, so refusing here leaves no orphan key.
				loc.mu.Unlock()
				return nil, false
			}
			loc.list = append(loc.list, s)
			loc.mu.Unlock()
			return ch, true
		}
		loc.mu.Unlock()
	}
}

// Unsubscribe removes ch from the subscriber list for locationID.
// MUST be called via defer in the SSE handler goroutine.
//
// The channel is deliberately NOT closed. Broadcast copies the subscriber list
// under the mutex and then sends outside it; closing here would let a send land
// on a closed channel and panic in the sender — fatal in the StartHeartbeats
// goroutine, which has no recover. The handler is the only reader and always
// exits via r.Context().Done() or the lifetime bound, so the close signal was
// never needed. The channel is garbage once the last reference drops.
func (m *Manager) Unsubscribe(locationID string, ch chan SSEEvent) {
	val, ok := m.subs.Load(locationID)
	if !ok {
		return
	}
	loc := val.(*locationSubs)

	loc.mu.Lock()
	defer loc.mu.Unlock()

	found := -1
	for i, s := range loc.list {
		if s.ch == ch {
			found = i
			break
		}
	}
	if found != -1 {
		// Remove ch from the slice and shrink it. Never close ch — see above.
		loc.list[found] = loc.list[len(loc.list)-1]
		loc.list[len(loc.list)-1] = nil
		loc.list = loc.list[:len(loc.list)-1]
	}

	// If the slice becomes empty, delete the key from subs.
	if len(loc.list) == 0 {
		m.subs.Delete(locationID)
	}
}

// Broadcast fans out event to all subscribers for locationID (non-blocking).
// Updates latestVersions[locationID] to event.QueueVersion.
// MUST be called ONLY after the database transaction has committed (Law 8).
// If Manager is nil, this is a no-op (queue correctness is independent of SSE — Law 21).
func (m *Manager) Broadcast(locationID string, event SSEEvent) {
	if m == nil {
		return
	}
	m.latestVersions.Store(locationID, event.QueueVersion)

	val, ok := m.subs.Load(locationID)
	if !ok {
		return
	}
	loc := val.(*locationSubs)

	loc.mu.Lock()
	// Copy the active subs to release the lock before writing to channels (prevents blocking)
	subsCopy := make([]*sub, len(loc.list))
	copy(subsCopy, loc.list)
	loc.mu.Unlock()

	for _, s := range subsCopy {
		select {
		case s.ch <- event:
		default:
			// drop on overflow
		}
	}
}

// StartHeartbeats launches a goroutine that emits heartbeat events every
// SSE_HEARTBEAT_SECONDS (default 30s) for every location that currently has at
// least one subscriber.
// The heartbeat carries the last-known queue_version so clients detect missed events.
// ctx is the server root context; the goroutine exits when ctx is cancelled.
func (m *Manager) StartHeartbeats(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(m.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.subs.Range(func(key, _ any) bool {
					locationID := key.(string)
					v := 0
					if val, ok := m.latestVersions.Load(locationID); ok {
						v = val.(int)
					}
					m.Broadcast(locationID, SSEEvent{
						Type:         "heartbeat",
						QueueVersion: v,
						// LocationID intentionally omitted in heartbeat (omitempty)
					})
					return true
				})
			}
		}
	}()
}
