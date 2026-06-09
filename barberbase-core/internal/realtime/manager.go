package realtime

import (
	"context"
	"encoding/json"
	"fmt"
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
//   event: <Type>\ndata: <JSON>\n\n
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
	return &Manager{}
}

// Subscribe registers a new client for locationID.
// Returns a buffered channel (capacity 16). Non-blocking broadcast drops on overflow;
// client refetches on reconnect — this is intentional (SSE is notification-only).
func (m *Manager) Subscribe(locationID string) chan SSEEvent {
	ch := make(chan SSEEvent, 16)
	s := &sub{ch: ch}

	for {
		// Load or store a new locationSubs structure.
		val, _ := m.subs.LoadOrStore(locationID, &locationSubs{})
		loc := val.(*locationSubs)

		loc.mu.Lock()
		// Double check if this loc is still registered in the map (to avoid race with deletion in Unsubscribe)
		if val2, ok := m.subs.Load(locationID); ok && val2 == loc {
			loc.list = append(loc.list, s)
			loc.mu.Unlock()
			break
		}
		loc.mu.Unlock()
	}

	return ch
}

// Unsubscribe removes ch from the subscriber list for locationID.
// MUST be called via defer in the SSE handler goroutine.
// After removal the channel is closed to unblock any pending receiver.
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
		// Remove ch from the slice. Shrink the slice. Close ch.
		loc.list[found] = loc.list[len(loc.list)-1]
		loc.list[len(loc.list)-1] = nil
		loc.list = loc.list[:len(loc.list)-1]
		close(ch)
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

// StartHeartbeats launches a goroutine that emits heartbeat events every 30 seconds
// for every location that currently has at least one subscriber.
// The heartbeat carries the last-known queue_version so clients detect missed events.
// ctx is the server root context; the goroutine exits when ctx is cancelled.
func (m *Manager) StartHeartbeats(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
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
