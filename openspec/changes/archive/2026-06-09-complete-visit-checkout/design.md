## Context

Phase C3.2 is the culmination of a customer's visit, checking them out and processing payments. The application requires an atomic operation to prevent partial state corruption (e.g. money collected but visit not finalized, or vice versa) and enforce strict business laws regarding monetary representation and asynchronous event processing.

## Goals / Non-Goals

**Goals:**
- Provide a single atomic `CompleteVisitAndCheckout` function that performs all validations, calculations, and updates.
- Adhere strictly to the "all money in paise, no FLOAT" rule (Law 4).
- Ensure outbox pattern is followed correctly for feedback scheduling (Law 7).
- Prevent duplicate checkouts for the same queue entry via proper locking and state checks.
- Prevent phantom data issues by using immutable service and product snapshots (Law 10).

**Non-Goals:**
- Handling pushing of push notifications (deferred to Phase C6.6).
- Integrating with external payment gateways directly in this transaction (assumes payment was confirmed or is being recorded out-of-band/synchronously for UPI/Cash/Card).

## Decisions

- **Single Transaction (`WithTx`):** All database interactions will be wrapped in a single transaction via `WithTx` from C0.2 to ensure atomicity. 
- **Pessimistic Row Locking:** `queue_sessions` will be locked `FOR UPDATE` first to enforce a strict ordering (Law 1), preventing race conditions across concurrent checkouts in the same session. Then `queue_entries` and `visits` are locked.
- **Batched Inserts:** Using `pgx.CopyFromRows` or batched inserts to handle `visit_charge_line_items` to avoid performance degradation with N+1 insert queries.
- **Outbox Pattern:** Feedback request scheduling is added as an `outbox_events` record inside the same transaction, avoiding distributed transaction complexities.
- **SSE Broadcast:** Emitting SSE broadcast strictly *after* the commit completes, as per Law 8. This guarantees clients don't fetch stale data.

## Risks / Trade-offs

- **Risk: Lock Contention:** Locking the `queue_sessions` record limits concurrency to one checkout at a time per location/date.
  - **Mitigation:** Checkout operations are extremely fast (a few milliseconds) and the contention scope is isolated to a single location. This is an acceptable trade-off to ensure correctness and prevent race conditions.
- **Risk: Product Deletion Mid-checkout:** A product becomes inactive while the checkout modal is open.
  - **Mitigation:** The transaction explicitly validates product existence and active status. It will rollback and return an `ErrProductNotFound` (422), forcing the UI to refresh.
