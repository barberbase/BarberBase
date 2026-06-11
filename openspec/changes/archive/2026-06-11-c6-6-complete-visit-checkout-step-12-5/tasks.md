## 1. Implementation

- [x] 1.1 Add check for active, push-enabled staff members at the location in CompleteVisitAndCheckout.
- [x] 1.2 Marshal JSON payload containing the location ID and tenant ID.
- [x] 1.3 Insert web_push.send event into outbox_events inside the transaction if push-enabled staff members are present.

## 2. Verification

- [x] 2.1 Verify that checkout rollback (e.g. payment sum mismatch) results in no web_push.send event.
- [x] 2.2 Verify that checkout with zero push-enabled staff writes no web_push.send event.
- [x] 2.3 Verify that checkout with at least one active, push-enabled staff member writes exactly one web_push.send event with correct payload.
