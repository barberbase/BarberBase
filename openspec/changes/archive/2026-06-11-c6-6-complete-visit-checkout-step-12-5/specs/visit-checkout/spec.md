## ADDED Requirements

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
