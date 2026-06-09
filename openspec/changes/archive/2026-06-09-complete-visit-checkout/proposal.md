## Why

This change implements Phase C3.2 — CompleteVisitAndCheckout. It solves the critical business need of finalizing a customer visit at the barber shop by ensuring all payment, queue status, customer history, and staff availability updates occur atomically. It provides a robust, all-or-nothing transaction to prevent partial state updates (e.g., payment mismatch, bad queue state, lock timeout) guaranteeing data integrity when a visit concludes.

## What Changes

- Implement the `CompleteVisitAndCheckout` domain command with strict validation and business logic.
- Execute an all-or-nothing database transaction to finalize a visit, calculate subtotal and total, apply discounts, and record payments.
- Lock necessary rows (queue sessions, queue entries, visits) in a strict order to prevent race conditions.
- Create `visit_charges`, `visit_charge_line_items`, and `visit_payments` records immutably without float types.
- Update `queue_entries`, `visits`, `customers` (if applicable), and `staff_members` correctly.
- Insert `outbox_events` for feedback scheduling asynchronously.
- Increment queue version and dispatch SSE broadcast strictly after the transaction commits.
- Add handler `CompleteService` in `internal/api/handlers_staff.go`.

## Capabilities

### New Capabilities
- `visit-checkout`: Core capability covering the calculation of totals, applying discounts, processing split payments, updating entity statuses, and producing feedback request events upon visit completion.

### Modified Capabilities
- (None)

## Impact

- **Code:** `internal/domain/queue/commands.go`, `internal/repository/visit.go`, `internal/api/handlers_staff.go`, `internal/domain/queue/errors.go`
- **Data:** Introduces strict row locking on `queue_sessions`, `queue_entries`, and `visits`. Modifies `customers` and `staff_members`. Inserts into `visit_charges`, `visit_charge_line_items`, `visit_payments`, and `outbox_events`.
- **System:** Ensures atomicity. Broadcasts Server-Sent Events (SSE) out-of-band upon success.
