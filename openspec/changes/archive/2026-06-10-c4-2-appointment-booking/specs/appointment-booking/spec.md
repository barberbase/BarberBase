## ADDED Requirements

### Requirement: Book Appointment Idempotency
The system MUST prevent duplicate appointment bookings for the same customer, time, and service using an idempotency key UNIQUE constraint on the database.

#### Scenario: Submitting duplicate booking request
- **WHEN** client submits POST `/appointments/book` with an existing idempotency key
- **THEN** system rejects request or returns existing appointment without creating a duplicate.

### Requirement: Check-in Priority
The system MUST place checked-in appointments onto the queue with a priority group value of 50.

#### Scenario: Appointment checked in
- **WHEN** staff calls check-in endpoint for a booked appointment
- **THEN** queue entry is created with `priority_group = 50`.
