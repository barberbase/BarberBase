## Context

BarberBase currently dispatches outbox notifications to Bhejna without enforcing tenant limits or accounting for quota usage. To prevent abuse and control WhatsApp/SMS delivery costs, we need to introduce a transactional quota enforcement layer that runs immediately before any Bhejna dispatch.

## Goals / Non-Goals

**Goals:**
- Implement three database-backed repository functions in `internal/repository/outbox.go` to handle all quota SQL operations.
- Intercept the notification outbox worker dispatch path in `internal/outbox/handlers/notification.go` to check and consume quotas using the new repository functions.
- Enforce hard-blocking for marketing notifications (`bb_marketing_broadcast` → `whatsapp_marketing`) at their limit, while allowing transactional notifications (`whatsapp_transactional`) to pass through.
- Implement idempotency checks using the outbox event ID to prevent double-counting of quota usage on retries.
- Ensure concurrency safety using row locks (`SELECT ... FOR UPDATE`) during quota period check/upsert.

**Non-Goals:**
- Bypassing or modifying Bhejna-calling logic or template parameter wiring.
- Implementing sms_otp quota tracking or web_push quota tracking (web_push bypasses Bhejna quota entirely).
- Modifying the core database schema or OpenAPI documents.

## Decisions

### 1. Quota Repository Layer in `internal/repository/outbox.go`
- **Decision:** Place all three SQL functions (`UpsertAndLockQuotaPeriod`, `InsertQuotaLedgerIdempotent`, `IncrementQuotaPeriodUsed`) in `internal/repository/outbox.go` under the `repository` package, receiving `pgx.Tx` for transactional operation.
- **Alternative:** Having inline SQL in the handler package.
- **Rationale:** Strict encapsulation of data access logic. By requiring `pgx.Tx` as a parameter, we guarantee these queries execute inside the caller's transaction.

### 2. Lock Serialization and Month Boundaries
- **Decision:** Use `FOR UPDATE` on `tenant_quota_periods` when looking up the current month's record. The month boundaries are determined dynamically using PostgreSQL's `date_trunc('month', NOW())::DATE`.
- **Rationale:** Row-level locking serializes concurrent dispatch requests for the same tenant/quota type, avoiding race conditions in used counts.

### 3. Separation of Transactions
- **Decision:** The `consumeQuota` function opens and commits its own quota transaction. The `notification_events` insert for the `blocked_quota` status case is performed in a separate top-level transaction.
- **Rationale:** If marketing quota is exceeded, the quota transaction commits the period upsert and returns `blocked=true`. The handler then inserts a `blocked_quota` log event and marks the outbox event as failed. Neither of these should be bound to a rolled back quota transaction.

## Risks / Trade-offs

- **Risk:** Database lock contention on `tenant_quota_periods` under high concurrency.
- **Mitigation:** Quota checks are extremely fast (simple primary key/index lookups) and are committed immediately. The locks are held for a very brief duration (within `consumeQuota`).
