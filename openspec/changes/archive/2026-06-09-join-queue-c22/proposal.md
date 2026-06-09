## Why

To support remote customer check-ins via public channels, the system needs a secure, idempotent endpoint `POST /v1/queue/join` that handles tenant resolution, customer creation, capacity validation, visit and service snapshotting, queue entry creation, and real-time updates without requiring user authentication.

## What Changes

- **New Ingress Endpoint**: `POST /v1/queue/join` to handle public customer check-ins.
- **Atomic Transaction Flow**: A multi-step transaction wrapping all database operations, including:
  - Idempotency check/replay.
  - Queue session creation and locking.
  - Capacity and session status validation.
  - Customer resolution (supporting E.164 and shadow profiles).
  - Service variant validation and total duration calculation.
  - Visit and visit-services (immutable snapshot) insertion.
  - Queue entry insertion.
  - Outbox event insertion for notification.
  - Idempotency key update.
- **Post-Commit Broadcast**: Triggering an SSE broadcast of the new queue version following successful transaction commit.

## Capabilities

### New Capabilities
- `join-queue-c22`: Implements the public queue joining API and its corresponding transactional domain logic, guaranteeing atomicity, idempotency, and data integrity.

### Modified Capabilities
<!-- None -->

## Impact

- **New files**:
  - `internal/api/handlers_public.go` for the API handler.
  - `internal/repository/visit.go` for visit and visit-service database operations.
- **Modified files**:
  - `internal/domain/queue/commands.go` adding `JoinQueue`.
  - `internal/repository/queue.go` adding `InsertQueueEntry` and `GetQueueEntryByCustomer`.
- **API**: Adds public endpoint `POST /v1/queue/join`.
