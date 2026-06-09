## Why

BarberBase needs a low-latency, real-time update mechanism to push queue session changes to staff dashboards and customer status pages. In-memory Server-Sent Events (SSE) will serve as a latency optimization while maintaining PostgreSQL as the single source of truth.

## What Changes

- Implement an in-memory `Manager` in Go using `sync.Map` for per-location SSE fanout.
- Add an `/stream/{location_id}` endpoint with token-based query parameters supporting both StaffJWT and CustomerSession (stateless HMAC verification).
- Add a `/staff/queue/snapshot` endpoint returning today's active entries using optimized SQL queries and batch fetches.
- Replace reflection-based `Broadcast` calls with direct `s.Manager.Broadcast` calls in implemented mutation handlers.

## Capabilities

### New Capabilities
- `sse-realtime`: Real-time queue updates via Server-Sent Events (SSE) and full queue snapshot capability for staff dashboards.

### Modified Capabilities
<!-- None -->

## Impact

- `cmd/server/main.go` registers and starts the realtime manager.
- `internal/api/server.go` gains the manager field.
- `internal/api/handlers_staff.go` implements the snapshot and stream endpoints and updates existing mutation handlers to broadcast.
