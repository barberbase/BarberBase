## Context

The system needs a public API endpoint `POST /v1/queue/join` that enables guest users and external systems to join a queue for a specific location. The design must address concurrency, atomicity, idempotency, and data consistency under load, ensuring no double-booking, over-capacity, or orphaned notifications occur.

## Goals / Non-Goals

**Goals:**
- Implement a transactional `JoinQueue` operation containing:
  - Idempotency check with DB table and `visits.idempotency_key` unique constraint.
  - Queue session auto-create and locking (`FOR UPDATE`).
  - Validation of session status (must not be 'closed', 'archived', or 'paused') and capacity (`active_count < max_total_queue_size`).
  - Customer resolution (supporting E.164 normalization, merging prevention, and shadow/masked profiles).
  - Validation of active service variants and total duration calculation.
  - Generation of unique visit IDs and a secure, time-limited magic link token using HMAC-SHA256 with 23-hour expiry.
  - Insertion of visits, immutable visit services, queue entries, and outbox notification events.
- Broadcast the new queue version via SSE after the transaction commits successfully.

**Non-Goals:**
- Implementing other queue mutations such as canceling or calling next (for future development phases).
- Implementing the live SSE server itself (rely on existing/placeholder broadcaster).

## Decisions

### 1. Multi-Step Atomic Transaction with `WithTx`
- **Decision**: Wrap all database operations starting from the idempotency check to the outbox event and idempotency key update within a single transaction using `repo.WithTx`.
- **Rationale**: Any failure in subsequent steps (e.g. invalid service variants, full capacity, or concurrent queue entry violations) must roll back all previously written rows to prevent inconsistent states.

### 2. Double-Layer Idempotency Guard
- **Decision**: Use a combination of an `idempotency_keys` table check (first operation in the transaction) and a unique index on `visits.idempotency_key` (second guard).
- **Rationale**: The `idempotency_keys` table allows caching of successful responses, while the `visits.idempotency_key` unique constraint ensures database-level protection against race conditions where concurrent duplicate requests might bypass the initial table check.

### 3. Tenant and Location Verification at Ingress
- **Decision**: Resolve `tenant_id` and limit-checks from the database at the handler entry using `location_id`.
- **Rationale**: Since `POST /v1/queue/join` is a public endpoint and does not require authentication, the tenant context must be securely determined from the location specified in the request body rather than being provided arbitrarily by the client.

## Risks / Trade-offs

- **Locking Bottlenecks**:
  - *Risk*: Locking the queue session for the duration of the entire `JoinQueue` transaction may serialize requests, reducing throughput under high concurrent traffic.
  - *Mitigation*: The transaction performs index-based SELECTs and INSERTs, executing extremely fast. Heavy operations like password hashing or external network calls are avoided within the transaction.
-2. **Stale/Invalid Token Codes**:
  - *Risk*: A customer could submit a stale token code that no longer matches or is expired.
  - *Mitigation*: The system checks expiration dates and status before proceeding with the join transaction.
