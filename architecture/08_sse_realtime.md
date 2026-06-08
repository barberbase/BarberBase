# Purpose
Complete specification of the SSE (Server-Sent Events) system. Covers the Go in-memory manager, broadcast pattern, client reconnect behavior, version comparison, and the correctness contract that SSE is notification-only.
 
# Use This File When
- Implementing the SSE manager (`realtime/manager.go`)
- Implementing the SSE stream endpoint (`/stream/{location_id}`)
- Implementing SSE client logic in SvelteKit
- Debugging missing updates on staff dashboard or magic link page
# Do Not Use This File For
- Queue state transitions (→ `04_queue_state_machine.md`)
- When outbox events are inserted (→ `07_webhooks_outbox_workers.md`)
- API endpoint auth (→ `openapi.yaml`)
# Related Files
- `openapi.yaml` — `/stream/{location_id}` endpoint definition
- `05_queue_locking_transactions.md` — where SSE broadcast is triggered (after COMMIT)
- `02_architecture_constraints.md` — no Redis, sync.Map only
- `15_critical_laws.md` — Law 8 (broadcast after COMMIT)
# Source Of Truth Priority
`openapi.yaml` for endpoint contract. This file for manager implementation.
 
---
 
## Core Correctness Contract
 
**SSE is notification-only. It is never required for correctness.**
 
- SSE tells clients "something changed, go fetch"
- Clients must recover by re-fetching canonical HTTP state
- If SSE is down: staff dashboard refetches on action; customer page polls or retries
- Queue state is always deterministic from PostgreSQL — SSE is a latency optimization
---
 
## Go SSE Manager
 
Implementation location: `internal/realtime/manager.go`
 
```
sync.Map keyed by location_id (UUID string)
Value: []chan SSEEvent  (slice of subscriber channels)
 
SSEEvent struct:
  Type        string  // "queue_changed" | "heartbeat"
  LocationID  string  // UUID
  QueueVersion int    // monotonically increasing integer
```
 
### Broadcast
 
Called AFTER every successful COMMIT (never inside a transaction):
 
```go
manager.Broadcast(locationID, SSEEvent{
    Type:         "queue_changed",
    LocationID:   locationID.String(),
    QueueVersion: newVersion,
})
```
 
Iterates all channels registered for `locationID`. Non-blocking send: if a channel's buffer is full, the event is dropped for that subscriber. The client will reconnect and refetch.
 
### Heartbeat
 
Every 30 seconds, broadcast the current `queue_version` for the location alongside the heartbeat.
A client that silently missed a dropped `queue_changed` event detects divergence and refetches —
with no polling loop:
```
event: heartbeat
data: {"type":"heartbeat","queue_version":184}
```
Client: on heartbeat, if `queue_version > localQueueVersion`, debounce a canonical refetch
(same path as a `queue_changed` event).
 
### Connection Cleanup
 
On client disconnect: remove channel from sync.Map slice. Use deferred cleanup in the SSE handler goroutine.
 
---
 
## SSE Stream Endpoint
 
```
GET /stream/{location_id}?token={jwt_or_session_token}
```
 
Auth: Token in query param (not header — EventSource API doesn't support custom headers).
- Staff: `?token={StaffJWT}`
- Customer: `?t={CustomerSession}`
- Unauthenticated: reject with 401
Response: `Content-Type: text/event-stream`
 
### Event Format
 
```
event: queue_changed
data: {"type":"queue_changed","location_id":"019...","queue_version":184}
 
event: heartbeat
data: {"type":"heartbeat"}
```
 
---
 
## Client Behavior (SvelteKit)
 
### Version Comparison Logic
 
```javascript
onSSEEvent(event) {
  if (event.type === "queue_changed") {
    if (event.queue_version > localQueueVersion) {
      debounce(fetchCanonicalState, 500ms)
    }
  }
}
```
 
Debounce 500ms: rapid-fire mutations (e.g., staff completes checkout, SSE fires, then another mutation fires) don't cause double-fetches.
 
### Reconnect Backoff
 
```
1s → 2s → 4s → 8s → max 30s
On reconnect: fetch REST immediately (do not wait for next SSE event)
```
 
### Staff Dashboard
 
Persistent SSE connection. Reconnects immediately on disconnect. After reconnect: `GET /v1/staff/queue/snapshot` immediately.
 
### Customer Magic Link Page
 
Active while tab is visible. Pause/freeze when tab is hidden (Page Visibility API). Timeout and stop after 4 hours of idle (no state changes). On reconnect: `GET /v1/queue/my-status` immediately.
 
---
 
## Failure Cases
 
| Failure | Client Behavior |
|---|---|
| SSE connection dropped | Reconnect with backoff, fetch REST on reconnect |
| SSE event lost (buffer full) | Client sees stale version, detects on next event or poll |
| SSE never fires | Staff dashboard shows stale state until next action; acceptable |
| Go process restarts | All in-memory channels lost; clients reconnect within 30s max |
| queue_version on SSE < local version | Stale event (delayed delivery), ignore — don't downgrade |
 
**No client-side polling.** The client relies on SSE + explicit fetches after SSE events. No timer-based polling loop.
 
---
 
## Horizontal Scaling Path (future, no Redis)
 
The `sync.Map` manager is a single-node optimization: a node only broadcasts to the SSE
subscribers it holds locally. With multiple Go nodes, a mutation that commits on node B must
still reach a client connected to node A. The bridge is PostgreSQL `LISTEN/NOTIFY` — not Redis:
 
- After COMMIT (Law 8), the mutation issues `NOTIFY queue_changes, '<location_id>:<queue_version>'`
  (this replaces or wraps the in-process `manager.Broadcast`).
- Each node holds one dedicated `LISTEN queue_changes` connection and, on NOTIFY, fans out to
  its local subscriber set exactly as today.
`NOTIFY` is delivered only to currently-connected listeners; payloads are <8000 bytes — both
fine (SSE is best-effort; clients refetch; payload is tiny). Zero new infrastructure. Not
needed on a single node. Because tokens are stateless and OTPs are in PostgreSQL, SSE
reconnects can land on any node — no sticky sessions required.
 
---
 
## Memory Considerations (1GB Droplet)
 
Each SSE connection holds one goroutine and one buffered channel. At 50 concurrent shop staff connections + 200 concurrent customer sessions = ~250 goroutines + channels. This is negligible on the memory budget.
 
The bottleneck is PostgreSQL connections (`max_connections=50`, pgxpool `MaxConns=20`), not SSE connections.
