## ADDED Requirements

### Requirement: SSE stream subscription and auth
The system SHALL provide an SSE stream at `/stream/{location_id}` that accepts `token` in the query parameters. It MUST authenticate the subscriber using StaffJWT or statelessly HMAC-verifying CustomerSession. If validation fails, it SHALL return a 401 Unauthorized status before writing any SSE headers.

#### Scenario: Staff authenticates successfully
- **WHEN** staff connects to `/stream/{location_id}` with a valid StaffJWT query param matching the location_id path parameter
- **THEN** connection is established as text/event-stream with a 200 OK status

#### Scenario: Customer authenticates successfully
- **WHEN** customer connects to `/stream/{location_id}` with a valid CustomerSession query param matching the location_id path parameter
- **THEN** connection is established as text/event-stream with a 200 OK status

#### Scenario: Auth mismatch or invalid token
- **WHEN** client connects to `/stream/{location_id}` with an invalid token or mismatching location_id
- **THEN** connection is rejected immediately with a 401 Unauthorized status

### Requirement: SSE Broadcast and Heartbeat
The system SHALL broadcast `queue_changed` events to all active subscribers for a location after a successful mutation transaction commit. The manager MUST run a heartbeat loop emitting `heartbeat` events every 30 seconds containing the last-known queue version.

#### Scenario: Active subscribers receive broadcast
- **WHEN** a mutation transaction commits successfully and a Broadcast call is made
- **THEN** all subscribers registered for that location receive the event non-blockingly

#### Scenario: Transaction rollbacks produce zero broadcasts
- **WHEN** a mutation fails and the transaction rolls back
- **THEN** no queue_changed event is broadcast

#### Scenario: Heartbeat carries last-known queue version
- **WHEN** 30 seconds elapse for an active subscription
- **THEN** a heartbeat event is sent containing the latest version of the queue

### Requirement: Full Queue Snapshot
The system SHALL expose `/staff/queue/snapshot` returning today's active entries for a location, sorted by: `in_progress` first, then `called`, then `waiting` (priority_group ASC, sort_key ASC). Terminal states (completed, cancelled, expired) MUST be excluded. Customer phone numbers MUST show only the last 4 digits (masked).

#### Scenario: Query active entries snapshot
- **WHEN** staff fetches `/staff/queue/snapshot` for a location with an active session
- **THEN** the system returns active entries in the correct state order with masked customer phone numbers

#### Scenario: Query snapshot with no active session
- **WHEN** staff fetches `/staff/queue/snapshot` but no active queue session exists for today
- **THEN** the system returns 200 OK with empty entries list
