## 1. Define Types and Errors

- [x] 1.1 Add `CheckoutProductItem`, `CheckoutPaymentLine`, `CheckoutParams`, and `CheckoutResult` types to `internal/domain/queue/commands.go`.
- [x] 1.2 Define domain errors (`ErrSessionNotFound`, `ErrEntryNotFound`, `ErrInvalidTransition`, `ErrPaymentMismatch`, `ErrInvalidDiscount`, `ErrProductNotFound`) at the top of `internal/domain/queue/commands.go` (do not create `errors.go`).

## 2. Repository Layer (`internal/repository/visit.go`)

- [x] 2.1 Add internal types `checkoutEntry`, `visitServiceRow`, and `productRow` to `internal/repository/visit.go`.
- [x] 2.2 Add any necessary repository helper functions or data structures to `internal/repository/visit.go` if required to support the checkout operation.

## 3. Implement CompleteVisitAndCheckout Domain Logic (`internal/domain/queue/commands.go`)

- [x] 3.1 Create `CompleteVisitAndCheckout` function skeleton with `WithTx`.
- [x] 3.2 Step 1: Implement `FOR UPDATE` lock on `queue_sessions`.
- [x] 3.3 Step 2: Implement `FOR UPDATE` lock on `queue_entries` and state validation.
- [x] 3.4 Step 3: Implement `FOR UPDATE` lock on `visits`.
- [x] 3.5 Step 4: Fetch snapshot data from `visit_services` into `visitServiceRow`.
- [x] 3.6 Step 5: Fetch product prices and validate existence into `productRow`.
- [x] 3.7 Step 6: Compute subtotals, validate discount, compute total, validate payments, and determine payment status.
- [x] 3.8 Step 7: Insert into `visit_charges`.
- [x] 3.9 Step 8: Insert service lines into `visit_charge_line_items` using pgx batching.
- [x] 3.10 Step 9: Insert product lines into `visit_charge_line_items` using pgx batching.
- [x] 3.11 Step 10: Insert into `visit_payments`.
- [x] 3.12 Step 11: Update `queue_entries` to `completed` and `is_dispatchable = false`.
- [x] 3.13 Step 12: Update `visits` to `completed`.
- [x] 3.14 Step 13: Increment customer metrics (last_visit_at, visit_count, lifetime_value_paise) if `customer_id` is present.
- [x] 3.15 Step 14: Update assigned barber's `staff_members` status to `idle` if `assigned_barber_id` is present.
- [x] 3.16 Step 15: Insert feedback scheduling `outbox_events` if `customer_id` is present, and set `FeedbackScheduled = true`.
- [x] 3.17 Step 16: Increment `queue_version` on `queue_sessions` and return the final `CheckoutResult`.

## 4. Implement API Handler (`internal/api/handlers_staff.go`)

- [x] 4.1 Create `CompleteService` in `internal/api/handlers_staff.go`.
- [x] 4.2 Map JWT context, validate IDs, and populate `CheckoutParams`.
- [x] 4.3 Invoke `CompleteVisitAndCheckout`.
- [x] 4.4 Map domain errors to 422/404/500 HTTP responses.
- [x] 4.5 Call `s.SSE.Broadcast` with the updated queue version on success (outside the transaction).

## 5. Integration and Concurrency Testing

- [x] 5.1 Integration: Test payment sum mismatch results in full rollback; zero rows in `visit_charges`, `visit_charge_line_items`, `visit_payments`.
- [x] 5.2 Integration: Test entry state not `in_progress` returns 422 before any write.
- [x] 5.3 Integration: Test completed entry is never re-dispatched (`is_dispatchable = false`, `state = 'completed'`, excluded from Call Next index).
- [x] 5.4 Integration: Test historical `visit_charge_line_items` rows remain unchanged after `service_variants.price_paise` is updated post-checkout.
- [x] 5.5 Integration: Test `customers.visit_count`, `lifetime_value_paise`, and `last_visit_at` are incremented correctly.
- [x] 5.6 Integration: Test `staff_members.status = 'idle'` for the assigned barber after commit.
- [x] 5.7 Integration: Test `outbox_events` row with `type='feedback_request.schedule'` is present after commit, but absent on rollback.
- [x] 5.8 Concurrency: Test two goroutines racing to checkout the same entry — exactly one succeeds, one returns 422, and no duplicate `visit_charges` rows are created.
