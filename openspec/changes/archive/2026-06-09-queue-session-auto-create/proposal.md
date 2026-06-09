## Why

When a customer joins the queue on a new business date, the corresponding queue session might not exist yet. To prevent race conditions and ensure safe token allocation, the system must automatically initialize and lock the queue session using an idempotent "upsert-then-lock" database pattern (first `INSERT ON CONFLICT DO NOTHING`, then `SELECT FOR UPDATE`).

## What Changes

- **Queue Session Auto-Creation and Locking**: Add `EnsureAndLockQueueSession` in the repository layer to perform the safe upsert-then-lock sequence in a single transaction.
- **Queue Domain Commands**: Introduce a domain-level `Commands` service wrapping queue mutations and enforcing session locking as the mandatory entry point.
- **Concurrency Safeguards**: Provide unit and integration testing verifying that up to 50 concurrent transactions racing to initialize a session on a fresh date converge safely to exactly one queue session row without errors.

## Capabilities

### New Capabilities
- `queue-session-auto-create`: Enforces that all queue mutations begin by safely upserting and locking the queue session for the current business date.

### Modified Capabilities
<!-- None -->

## Impact

- **New files**:
  - `internal/repository/queue.go` containing database structures and logic.
  - `internal/domain/queue/commands.go` wrapping transaction commands.
  - `internal/repository/queue_test.go` integration test suite.
- **Dependencies**: Requires `pgx/v5` and a PostgreSQL database setup.
