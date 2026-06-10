## ADDED Requirements

### Requirement: Quota period auto-creation
The system SHALL auto-create a quota period row for the current calendar month when performing a quota check if one does not already exist for the tenant and quota type. The limits SHALL be sourced from the tenant's `monthly_marketing_quota` and `monthly_transactional_quota` fields.

#### Scenario: Auto-create period on first send of the month
- **WHEN** a tenant has no quota period row for the current month and triggers a notification dispatch
- **THEN** a new quota period row is inserted with dates covering the full calendar month and the correct limit, and the check proceeds

### Requirement: Marketing quota checking and locking
The system SHALL lock the quota period row using a database row lock (`FOR UPDATE`) to serialize checks. The system SHALL block marketing broadcasts if `used_count` is greater than or equal to `included_limit`.

#### Scenario: Marketing quota checked and blocked at limit
- **WHEN** a marketing notification (`bb_marketing_broadcast`) is processed and the tenant's marketing `used_count` has reached the `included_limit`
- **THEN** the dispatch is blocked, status is set to `blocked_quota` in `notification_events`, and the outbox event is marked as `failed` with error `quota_exhausted`

### Requirement: Transactional quota checking and soft-limit bypass
The system SHALL lock the transactional quota period row but SHALL NEVER block the dispatch of transactional notifications, regardless of the `used_count` or `included_limit`.

#### Scenario: Transactional quota checked and never blocked
- **WHEN** a transactional notification (any template except `bb_marketing_broadcast`) is processed and the tenant's transactional quota is exhausted
- **THEN** the dispatch continues normally without blocking

### Requirement: Idempotency of quota ledger and increment
The system SHALL insert a row into the quota usage ledger using the outbox event ID as the idempotency key, and only increment the period's `used_count` if the ledger insert actually succeeded (meaning it was not a duplicate/retry).

#### Scenario: Retry of the same outbox event ID
- **WHEN** a retried outbox event with a previously processed event ID is dispatched
- **THEN** the ledger insert is skipped due to unique constraint conflict, `used_count` is not double-incremented, and the dispatch is allowed to proceed (re-entering Bhejna dispatch)

### Requirement: Concurrency safety under high load
The system SHALL serialize all concurrent quota period upserts and locks for the same tenant and quota type.

#### Scenario: Concurrent dispatches for same tenant
- **WHEN** multiple goroutines concurrently attempt to dispatch outbox events for the same tenant and quota type
- **THEN** they serialize on the database lock, and `used_count` is incremented precisely by the number of successful ledger insertions without race conditions
