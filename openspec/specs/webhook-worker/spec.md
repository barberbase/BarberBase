# webhook-worker Specification

## Purpose
TBD - created by archiving change webhook-worker-c13. Update Purpose after archive.
## Requirements
### Requirement: Webhook event claiming and processing loop
The system SHALL continuously run a background worker loop to claim pending or failed/processing webhook events from the `webhook_events` table using database locking with `FOR UPDATE SKIP LOCKED`.
It MUST limit processing retries to 10 attempts. It MUST lock the claimed event for 30 seconds.
It MUST execute the processing loop outside of the claim transaction and handle any panics gracefully by marking the event as failed without leaking goroutines.

#### Scenario: Claiming a pending webhook event
- **WHEN** there is a pending event in `webhook_events` and a worker calls the claim query
- **THEN** the system updates its status to `processing`, increments attempts, sets a lock lease, and returns the row.

#### Scenario: Re-claiming an event after lease expiry
- **WHEN** an event is stuck in `processing` status and its `locked_until` timestamp is in the past
- **THEN** the worker loop claims the event again, increments attempts, and updates the lease.

#### Scenario: Skipping terminal failed events
- **WHEN** an event has 10 attempts and its status is `failed`
- **THEN** the worker loop never claims this event again.

#### Scenario: Gracefully handling process panic
- **WHEN** the webhook execution block panics during processing
- **THEN** the system catches the panic, updates the event status to `failed`, records the error, and continues the loop.

