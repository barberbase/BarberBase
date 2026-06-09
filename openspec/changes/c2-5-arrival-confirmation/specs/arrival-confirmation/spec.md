## ADDED Requirements

### Requirement: Arrival Presence Physical Verification Enforced
The system SHALL NOT allow a customer to self-declare presence state as `arrived` without physical verification. Verified arrival MUST occur only via the correct PIN (bcrypt), GPS proximity, or Staff override.

#### Scenario: PIN verification success
- **WHEN** customer provides the correct location PIN and has remaining attempts
- **THEN** their presence state is updated to arrived, the attempt is logged as success, and a 200 response is returned

#### Scenario: PIN verification failure
- **WHEN** customer provides an incorrect location PIN
- **THEN** their presence state is unchanged, the attempt is logged as failure, and a 400 response with remaining attempts is returned

#### Scenario: GPS proximity success
- **WHEN** customer is within the location arrival radius and GPS accuracy is <= 150m
- **THEN** their presence state is updated to arrived, the attempt is logged as success, and a 200 response is returned

#### Scenario: GPS proximity failure
- **WHEN** customer is outside the location arrival radius
- **THEN** their presence state is unchanged, no database mutations occur, and a 422 response is returned

### Requirement: Bcrypt Offloading for PIN Verification
The system MUST perform bcrypt operations (PIN hash comparison) outside of the database transaction to prevent locking database rows during slow computations.

#### Scenario: PIN bcrypt verification outside transaction
- **WHEN** a customer submits a PIN arrival request
- **THEN** the system compares the bcrypt hash with the input PIN before acquiring any database locks

### Requirement: Rate Limiting on Arrival Verification
The system SHALL enforce rate limits on arrival verification: a maximum of 5 failed attempts per queue entry, and 10 attempts per IP address per hour.

#### Scenario: Entry limit exceeded
- **WHEN** a queue entry has 5 failed arrival attempts
- **THEN** any subsequent arrival attempt returns a 429 rate limited error

#### Scenario: IP limit exceeded
- **WHEN** an IP address makes more than 10 arrival attempts in an hour
- **THEN** subsequent arrival attempts from that IP return a 429 rate limited error

### Requirement: GPS Accuracy Guard
The system SHALL reject GPS verification immediately if the reported accuracy is greater than 150 metres, without performing any database writes.

#### Scenario: Inaccurate GPS reading
- **WHEN** a customer submits GPS coordinates with accuracy > 150m
- **THEN** the system returns a 422 response and does not write to the database

### Requirement: Staff Override with Location Isolation
The system SHALL support staff check-in override, which requires StaffJWT authentication and enforces location isolation (meaning a staff member cannot check in a customer belonging to a different location).

#### Scenario: Staff override success
- **WHEN** a staff member with a valid StaffJWT checks in an entry belonging to their location
- **THEN** the customer's presence state is updated to arrived and logged with method staff

#### Scenario: Staff override forbidden location
- **WHEN** a staff member attempts to check in an entry belonging to a different location
- **THEN** the system returns a 403 Forbidden response

### Requirement: Logging of All Arrival Attempts
The system SHALL record every verification attempt (both success and failure) into the `arrival_attempts` table.

#### Scenario: Every attempt logged
- **WHEN** any arrival attempt is processed
- **THEN** a corresponding row is inserted into `arrival_attempts` detailing the method, success status, and IP address

### Requirement: Queue Session Serializable Lock
Every queue state mutation (ConfirmArrival, ConfirmOnTheWay, CancelMyEntry, StaffConfirmArrival) MUST lock `queue_sessions` FOR UPDATE first to ensure serializability.

#### Scenario: Lock session first
- **WHEN** any presence state mutation is initiated
- **THEN** the system executes SELECT FOR UPDATE on the associated queue_session before performing validations or mutations

### Requirement: SSE Broadcast After Commit
The system SHALL broadcast the new queue version via Server-Sent Events (SSE) only after the database transaction has successfully committed.

#### Scenario: SSE broadcast delayed until commit
- **WHEN** a state mutation transaction completes successfully
- **THEN** the SSE broadcast is sent to the location clients after commit

### Requirement: Confirm On The Way Transition
The system SHALL allow customers to transition their presence state to `on_the_way` from allowed states, which sets the `on_the_way_at` timestamp.

#### Scenario: Confirm on the way success
- **WHEN** a customer transitions from notified/remote state
- **THEN** the state changes to on_the_way and on_the_way_at is set to the current time

### Requirement: Cancel Queue Entry
The system SHALL allow a customer or staff member to cancel a queue entry, setting `state` to `cancelled` and `is_dispatchable` to false, provided the service has not started.

#### Scenario: Cancel waiting entry
- **WHEN** a cancel request is made for an entry in waiting state
- **THEN** the state becomes cancelled and is_dispatchable is set to false

#### Scenario: Cancel in progress entry fails
- **WHEN** a cancel request is made for an entry in progress
- **THEN** the request fails with a 422 response
