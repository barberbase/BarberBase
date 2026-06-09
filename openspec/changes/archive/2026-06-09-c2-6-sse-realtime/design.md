## Context

BarberBase needs a robust realtime updates layer using Server-Sent Events (SSE). It must handle concurrent subscribers per location, run a heartbeat mechanism, cleanly handle client disconnects, and support high-performance snapshot queries.

## Goals / Non-Goals

**Goals:**
- Implement the Go SSE `Manager` in package `realtime` using only in-memory constructs (`sync.Map` and channels).
- Implement the `/stream/{location_id}` handler with JWT (Staff) and Customer Session (stateless HMAC) verification.
- Implement `/staff/queue/snapshot` returning active entries sorted by `in_progress` -> `called` -> `waiting`.
- Write automated integration tests for transaction rollbacks, concurrent subscribers, disconnect cleanup, and correct snapshot return.

**Non-Goals:**
- Do not implement horizontal scaling with Redis (keep simple `sync.Map` in-memory).
- Do not modify `handlers_public.go` since it is not in the C2.6 write list.

## Decisions

- **Local Mutex Per Location**: To avoid global mutex contention, we store `locationSubs` inside `sync.Map` containing a per-location `sync.Mutex` and list of active channels.
- **Stateless HMAC Customer Session Verification**: Verify `CustomerSession` query param without querying database. Split token, compute HMAC-SHA256, and assert correctness and expiration.
- **Snapshot Batch Queries**: Batch query service lines, customer notes (using `deleted_at IS NULL`), and per-location visit counts to avoid N+1 queries.

## Risks / Trade-offs

- **Memory/Connection Leaks on Disconnect**: Mitigated by deferred `Unsubscribe` calls in the handler that cleanly close channels and remove them from the map.
- **Missing Updates During Disconnect**: Mitigated by exponential reconnect backoff and immediate HTTP state refetch on client reconnect.
