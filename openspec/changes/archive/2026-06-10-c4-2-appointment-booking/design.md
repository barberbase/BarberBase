## Context

The system needs to support booking and check-in flow for appointments. This includes:
1. Validating and inserting appointments with an idempotency key UNIQUE constraint.
2. Publishing `notification.send` outbox events for appointment confirmation and appointment reminders.
3. Placing the customer in the queue upon check-in, converting JSONB variant IDs into `visit_services` rows, and sorting queue entries correctly.

## Goals / Non-Goals

**Goals:**
- Implement `BookAppointment` with `appointments.idempotency_key` constraint as a second-line defense.
- Guarantee that both outbox events (`notification.send` for confirmation and reminder) are transactionally written inside the `WithTx` block.
- Correctly parse JSONB elements using `jsonb_array_elements_text` during check-in, setting `priority_group` to 50.
- Enforce StaffJWT on handlers and run integration tests.

**Non-Goals:**
- Modify `openapi.yaml` or existing DB schemas.

## Decisions

- **Transactional Consistency (Law 7)**: Perform the entire appointment creation, outbox event generation, and validation inside a single database transaction.
- **Priority Grouping**: Set check-in queue entries to `priority_group = 50`.
- **Stateless HMAC URLs**: Generate reminder urls using state-free signatures instead of database tokens.

## Risks / Trade-offs

- Concurrency on idempotency: Using DB-level UNIQUE constraint handles concurrent requests safely.
