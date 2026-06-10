## 1. Repository Implementation

- [x] 1.1 Create `internal/repository/outbox.go` and implement the receiver pattern.
- [x] 1.2 Implement `UpsertAndLockQuotaPeriod` to handle month truncations, auto-creation of month quota periods, and lock acquisition.
- [x] 1.3 Implement `InsertQuotaLedgerIdempotent` using unique constraint handling.
- [x] 1.4 Implement `IncrementQuotaPeriodUsed` to update the used count.

## 2. Interceptor Implementation

- [x] 2.1 Add `quotaTypeForTemplate` to `internal/outbox/handlers/notification.go`.
- [x] 2.2 Add `consumeQuota` function to `internal/outbox/handlers/notification.go`.
- [x] 2.3 Edit the call-site in the notification handler to invoke `consumeQuota` and insert a blocked status notification event and fail the outbox event.

## 3. Verification

- [x] 3.1 Verify building the codebase using `make build`.
- [x] 3.2 Run and verify the integration tests using `make test`.
