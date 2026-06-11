## Context

During the visit checkout process (`CompleteVisitAndCheckout`), the system needs to trigger a web push notification if any active staff members at that location have push notifications enabled. This notification tells the system to push updates to staff members. Following transactional outbox pattern guidelines, the event insertion must be atomic with the checkout transaction to avoid orphan events or missing updates.

## Goals / Non-Goals

**Goals:**
- Atomically check for active, push-enabled staff members at the location of checkout.
- If such staff members exist, insert a `web_push.send` outbox event inside the checkout transaction.
- Use `params.LocationID` and `params.TenantID` for the payload and query checks.

**Non-Goals:**
- Rate limiting or applying the frequency gate in this step. Frequency gating is deferred to the dispatch handler.
- Managing push-related errors or delivery failures. The outbox event worker will handle dispatching and retry policies.

## Decisions

- **Query form**: Use `SELECT EXISTS (SELECT 1 FROM staff_members WHERE location_id = $1 AND push_enabled = true AND is_active = true)` which short-circuits instantly upon locating a single matching record.
- **Payload format**: A simple JSON structure: `{"location_id":"<uuid>","tenant_id":"<uuid>"}` serialized using standard `json.Marshal` on an anonymous struct.
- **Location of execution**: Insert after the staff idle update (Step 14) and before the customer feedback scheduling (Step 15) inside the checkout transaction block.

## Risks / Trade-offs

- **Performance impact on Checkout**: Running an extra SELECT query inside the transaction.
  - *Mitigation*: The `EXISTS` check is extremely fast, especially when supported by indexes on `staff_members(location_id, push_enabled, is_active)`.
- **Database Rollbacks**: Transaction failures rolling back the `outbox_events` insert.
  - *Mitigation*: This is intended behavior (orphan-free outbox). If the checkout fails (e.g. payment mismatch), no push notification should be sent.
