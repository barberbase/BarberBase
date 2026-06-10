## 1. Domain & DB Implementation

- [x] 1.1 Implement `BookAppointment` with `appointments.idempotency_key` UNIQUE check.
- [x] 1.2 Implement transaction logic creating both outbox events (`notification.send` confirmation + reminder) inside the same Tx.
- [x] 1.3 Implement `CheckInAppointment` utilizing `jsonb_array_elements_text` to copy services to `visit_services` without an UPDATE path.
- [x] 1.4 Set `priority_group = 50` on check-in queue entries.

## 2. API Handlers

- [x] 2.1 Enforce StaffJWT on public appointment endpoints and extract `tenant_id` from claims.
- [x] 2.2 Manually wire Check-In handler.

## 3. Verification

- [x] 3.1 Implement `TestC42_BookAppointment_Idempotency` integration test.
- [x] 3.2 Implement `TestC42_CheckInAppointment_PrioritySort` integration test.
- [x] 3.3 Ensure all tests pass.
