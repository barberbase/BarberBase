## Why

We need to implement the Appointment Booking and Check-in domain functions (C4.2) to support appointment-driven workflow integrations with our queue system. This handles booking appointments, persisting them with an idempotency guard, and check-in processing where an appointment is placed on the check-in queue with correct priorities.

## What Changes

- Implement `BookAppointment` and `CheckInAppointment` domain functions in `internal/domain/queue/booking_resolver.go`.
- Wire up HTTP handlers in `internal/api/handlers_public.go` and enforce StaffJWT authentication.
- Create tests in `internal/api/handlers_public_test.go` to verify booking idempotency and queue check-in priority sorting.

## Capabilities

### New Capabilities
- `appointment-booking`: Support staff-only appointment booking with idempotency, outbox alerts, and check-in flow with custom queue priority.

### Modified Capabilities

## Impact

- `internal/domain/queue/booking_resolver.go` implements the core booking logic.
- `internal/api/handlers_public.go` exposes the endpoints.
- DB tables `appointments`, `visit_services`, `outbox_events`, and `queue_entries` are transactionally updated.
