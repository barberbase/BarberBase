## Purpose
Define requirements for visit checkout and completion operations.
## Requirements
### Requirement: Atomic Checkout Operation
The system SHALL execute the checkout process as a single atomic transaction that either succeeds fully or rolls back leaving zero rows written.

#### Scenario: Successful full checkout
- **WHEN** the checkout endpoint is called with valid data and exact payment matching
- **THEN** visit charge, line items, and payments are written, queue entry and visit are marked completed, queue version is incremented, and SSE is broadcast.

#### Scenario: Payment mismatch rollback
- **WHEN** the sum of payment amounts does not equal the calculated total
- **THEN** the transaction is rolled back and no records are saved.

#### Scenario: Negative or excessive discount
- **WHEN** the discount amount is less than 0 or greater than the subtotal
- **THEN** the transaction is rolled back and a 422 error is returned.

### Requirement: Idempotency and State Guard
The system SHALL prevent checkout on a queue entry that is not in the `in_progress` state.

#### Scenario: Duplicate checkout request
- **WHEN** a second checkout request arrives for an already completed entry
- **THEN** the system rejects it with a 422 without writing any rows.

### Requirement: Strict Type Usage
The system SHALL store and calculate all monetary amounts exclusively in paise as integers.

#### Scenario: Subtotal calculation
- **WHEN** calculating the subtotal
- **THEN** the system sums the unit amounts of services and products using integer math in paise, avoiding any floating point math.

### Requirement: Immutable Records
The system SHALL copy snapshot information into the visit charge line items to prevent historical alterations.

#### Scenario: Post-checkout price update
- **WHEN** the price of a service or product is updated after a checkout
- **THEN** the historical `visit_charge_line_items` rows remain unchanged.

### Requirement: Customer and Staff Updates
The system SHALL update the customer and staff member records correctly upon completion, when applicable.

#### Scenario: Anonymous walk-in checkout
- **WHEN** a queue entry with no customer ID completes checkout
- **THEN** the checkout succeeds without attempting to update any customer records.

#### Scenario: Customer metrics increment
- **WHEN** a queue entry with a customer ID completes checkout
- **THEN** the customer's `last_visit_at` is set, `visit_count` is incremented, and `lifetime_value_paise` is increased by the total amount.

#### Scenario: Staff idle reset
- **WHEN** checkout completes for an entry with an assigned barber
- **THEN** the `staff_members` status for that barber is set to `idle`.

### Requirement: Feedback Event Scheduling
The system SHALL write a scheduling event to the outbox pattern inside the transaction for known customers.

#### Scenario: Known customer checkout
- **WHEN** checkout completes for an entry with a customer ID
- **THEN** an `outbox_events` row of type `feedback_request.schedule` is written with `process_after` set to 30 minutes in the future.

### Requirement: SSE Broadcast Sequence
The system SHALL only broadcast SSE updates after the transaction has successfully committed.

#### Scenario: Checkout success SSE
- **WHEN** the checkout transaction commits successfully
- **THEN** an SSE broadcast is triggered using the newly incremented queue version.

### Requirement: Web Push Notification Trigger
The system SHALL conditionally insert a `web_push.send` event into `outbox_events` inside the checkout transaction when there is at least one active, push-enabled staff member at the location.

#### Scenario: Checkout rollback does not write event
- **WHEN** the checkout transaction fails and is rolled back
- **THEN** no `web_push.send` event is committed to the database.

#### Scenario: Checkout with no push-enabled staff
- **WHEN** checkout is completed for a location with zero active, push-enabled staff members
- **THEN** the checkout succeeds normally and no `web_push.send` event is written.

#### Scenario: Checkout with push-enabled staff
- **WHEN** checkout is completed for a location with at least one active, push-enabled staff member
- **THEN** exactly one `web_push.send` event is written with a payload containing the location ID and tenant ID, and `process_after` set to the current time.

