## Context

The backend handles the queue state machine, and now we need to implement the staff-facing component to control the overall Shop Status. Currently, the system lacks the ability to explicitly control and query the composite shop status (overrides, queue state, arrival pin) and enforce complex state transitions when a shop status changes (e.g. expiring remaining queue entries transactionally when a shop is closed).

## Goals / Non-Goals

**Goals:**
- Provide a transactional database layer (`internal/repository/location.go`) for reading and writing location status overrides and updating associated queue entries/sessions.
- Implement GET and PATCH endpoints at `/staff/shop/status` adhering to Law 11 (tenant_id from JWT).
- Guarantee atomic transitions when shop status is closed and entries must be expired.

**Non-Goals:**
- Do not implement booking resolver logic (C4.2).
- Do not touch public API paths (C3.3).
- Do not modify existing API or Schema files (`openapi.yaml`, `001_complete_schema.sql`).

## Decisions

- **Single Transaction for Status Change and Entry Expiry**: We will use a database transaction with `FOR UPDATE` on `queue_sessions` to handle concurrent mutations. This prevents race conditions with call-next, start, and checkout events.
- **Law 11 Compliance**: `tenantID` is explicitly passed from JWT claims and strictly filtered in all SQL queries, ensuring isolated tenancy.
- **Sentinel Errors**: We define `var ErrActiveEntriesExist = errors.New("active_entries_require_action")` so the API handler can gracefully translate it to a 422 error when `closed` is requested but active entries exist without a `modal_action`.

## Risks / Trade-offs

- [Concurrency Risk] Race conditions when multiple clients modify shop status or queue entries simultaneously → We mitigate this by using `FOR UPDATE` on the queue session (avoiding `SKIP LOCKED` as required by Law 2). Locking the session strictly serializes operations on the queue session.
