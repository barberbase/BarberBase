## Why

To enforce tenant notification quotas (marketing and transactional WhatsApp messages) before dispatching Bhejna notifications, preventing abuse and managing usage costs while ensuring core transactional notifications are never blocked.

## What Changes

- Add three repository functions in a new repository file `internal/repository/outbox.go` to handle quota period upsert, locking, usage ledger insert, and usage increment inside transactions.
- Update `internal/outbox/handlers/notification.go` to integrate quota checks (`consumeQuota`) and template mapping (`quotaTypeForTemplate`).
- Implement hard-blocking for marketing broadcast notifications at their monthly limit, while ensuring transactional notifications are soft-limited and never blocked.
- Handle blocked marketing events by inserting a `blocked_quota` notification event and updating the outbox event status to `failed` with error `quota_exhausted`.

## Capabilities

### New Capabilities
- `tenant-quota-enforcement`: Ensure tenant WhatsApp quota checks are enforced atomically and idempotently prior to notification dispatch.

### Modified Capabilities

## Impact

- `internal/repository/outbox.go` (new repository layer code)
- `internal/outbox/handlers/notification.go` (modified outbox dispatch flow)
- Database schema tables: `tenant_quota_periods`, `quota_usage_ledger`, and `notification_events`
