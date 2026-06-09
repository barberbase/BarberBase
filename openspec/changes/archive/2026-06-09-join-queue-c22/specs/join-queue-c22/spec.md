## ADDED Requirements

### Requirement: Public Join Queue API
The system SHALL expose a public HTTP POST endpoint `/v1/queue/join` allowing customers or agents to join a queue. The request body must include `location_id`, `variant_ids`, `party_size`, and an `idempotency_key`. It may optionally include `customer_name`, `phone_number`, `bsuid`, and `requested_barber_id`.

#### Scenario: Successful Join Queue
- **WHEN** a valid request to join the queue is received with all required fields
- **THEN** a new visit, visit_services, and queue_entry records are created, an outbox notification is queued, the queue session's version and token number are incremented, and an SSE broadcast is triggered after the transaction commits successfully.

### Requirement: Idempotency Protection
The system SHALL protect the join queue operation from duplicate submissions using an idempotency key. A unique constraint on `visits.idempotency_key` must serve as a secondary guard.

#### Scenario: Replayed Request with Stored Response
- **WHEN** a request with an existing, completed idempotency key is received
- **THEN** the system commits the transaction (doing nothing) and returns the previously stored HTTP 200 response.

#### Scenario: Replayed Request In-Flight
- **WHEN** a request with an existing, incomplete/in-flight idempotency key is received
- **THEN** the system rolls back the transaction and returns a 409 conflict.

### Requirement: Queue Session Status and Capacity Validation
The system SHALL validate that the queue session is active and has sufficient capacity before allowing a customer to join.

#### Scenario: Joining a Closed or Paused Session
- **WHEN** attempting to join a queue session whose status is 'closed', 'archived', or 'paused'
- **THEN** the system aborts the transaction and returns a 422 error with code "shop_not_accepting".

#### Scenario: Joining a Full Queue
- **WHEN** the number of active queue entries in a session equals or exceeds the location's `max_total_queue_size`
- **THEN** the system aborts the transaction and returns a 422 error with code "queue_full".

### Requirement: Customer and Barber Validation
The system SHALL validate all associated resources during the join process. The customer resolution must use the `merged_into_customer_id IS NULL` check. The requested barber must be active and associated with the same location.

#### Scenario: Invalid or Inactive Barber
- **WHEN** a requested barber is not found, is inactive, or belongs to a different location
- **THEN** the system aborts the transaction and returns a 422 error.

#### Scenario: Duplicate Active Customer in Queue
- **WHEN** a customer with an active entry in the current session attempts to join the queue again
- **THEN** the system aborts the transaction and returns a 409 conflict.
