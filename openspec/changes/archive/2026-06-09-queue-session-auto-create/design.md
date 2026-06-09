## Context

When a customer attempts to join the queue on a new business date, a `queue_sessions` record for that date must be present. Multiple customers might attempt to join concurrently at the very start of the day. Without coordination, multiple transactions could try to insert the session row, leading to unique key violations or race conditions where multiple parallel sessions are created or tokens are incorrectly started from duplicate states.

## Goals / Non-Goals

**Goals:**
- Guarantee that exactly one `queue_sessions` record is created per location and business date.
- Provide a blocking serialization lock (`SELECT ... FOR UPDATE`) to coordinate all operations on a queue session.
- Implement the "upsert-then-lock" database order: insert with `ON CONFLICT DO NOTHING` first, followed by `SELECT ... FOR UPDATE`.
- Verify behavior with a concurrency test of 50 simultaneous transactions.

**Non-Goals:**
- Actual queue joins, cancellations, or state transitions (these will be implemented in subsequent phases C2.2+).
- Custom business date timezone logic inside the repository (caller must provide the computed date at UTC midnight).

## Decisions

### Upsert-then-Lock Database Pattern
- **Decision**: Perform an idempotent `INSERT ON CONFLICT (location_id, business_date) DO NOTHING` as the first statement, followed by `SELECT FOR UPDATE` as the second statement inside the transaction.
- **Rationale**: If we performed `SELECT FOR UPDATE` first, on a fresh date it would lock zero rows because the row does not exist yet. A concurrent transaction could then insert the row, leading to a race condition. By upserting first, we guarantee the row exists, and subsequent `SELECT FOR UPDATE` blocks all other concurrent operations.
- **Alternatives Considered**:
  - `INSERT ON CONFLICT DO UPDATE`: Unnecessary because we do not need to update any fields on creation, we only need to ensure existence.
  - Advisory locks: More complex to manage and clean up; standard row-level locks on the session table naturally scope to the business transaction.

## Risks / Trade-offs

- **Lock Contention**:
  - *Risk*: Multiple concurrent clients blocking on the queue session lock could lead to timeouts or slow responses.
  - *Mitigation*: Ensure all transactions performing queue mutations are short-lived. Set a database-level session lock timeout (e.g., 1s) to return fail-fast errors instead of blocking indefinitely.
