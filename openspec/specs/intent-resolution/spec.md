# intent-resolution Specification

## Purpose
TBD - created by archiving change webhook-worker-c13. Update Purpose after archive.
## Requirements
### Requirement: JOIN intent resolution transaction
The system SHALL process `ActionJoin` messages by resolving checkin intents and locations, creating a visit, copying service variant snapshots, constructing a queue entry, and queue session state progression within a single database transaction.
It MUST lock the `queue_sessions` table before executing queue modifications. It MUST insert outbox notification events inside the same transaction. It MUST broadcast SSE updates after the transaction commits.

#### Scenario: Successful JOIN resolution
- **WHEN** a valid, unexpired token code is resolved, a customer is matched, and there are no active queue entries for that customer
- **THEN** the system registers a new visit with snapshot variant data, creates a waiting queue entry, updates session token counter, resolves the intent, enqueues a notification outbox event, and pings the SSE broadcaster.

#### Scenario: Reject duplicate active customer entry
- **WHEN** a customer already has an active queue entry (waiting, called, or in_progress) in the current session
- **THEN** the transaction is rolled back, and the customer is notified of the active spot.

#### Scenario: Shop closed status gate
- **WHEN** the checkin intent was created when the shop status was not "open" or "closing_soon", or the location is inactive
- **THEN** the resolution returns a message saying the shop isn't accepting walk-ins.

### Requirement: JOIN exact token matching
The system SHALL match the check-in intent strictly via token code with an exact comparison. It MUST NOT perform wildcard, `LIKE`, `ILIKE` or prefix database query matching on the body slug.

#### Scenario: JOIN with wildcard slug
- **WHEN** a JOIN message "JOIN STAR%SALON_BRANCH JN8K4P" is processed
- **THEN** the system matches the tenant/location strictly by the exact "JN8K4P" token code with no wildcard database matching.

### Requirement: Button-payload tenant resolution
For button-payload actions (such as `ActionOnTheWay`, `ActionCancel`, `ActionRatingButton`), the system SHALL resolve the `tenant_id` and `location_id` by querying the referenced database entity (such as `queue_entries`, `feedback_requests`) by the UUID suffix extracted from the payload. It MUST NOT use the message sender's phone or any text slug to resolve the tenant context.

#### Scenario: Resolve tenant for ActionOnTheWay
- **WHEN** the webhook processes an `ActionOnTheWay` event with entry ID `019001b3-4f9c-70e1-8000-017f8a9b2c3d`
- **THEN** it queries `queue_entries` and its associated `queue_sessions` to load the authoritative `tenant_id` and `location_id` for that entry.

