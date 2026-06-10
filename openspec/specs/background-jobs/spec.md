# background-jobs Specification

## Purpose
TBD - created by archiving change implement-background-jobs-c4-3. Update Purpose after archive.
## Requirements
### Requirement: Advisory Lock Execution Guard
The background jobs watchdog, end-of-day, and weekly summary MUST use PostgreSQL advisory locks (with keys `0xBBC401`, `0xBBC402`, and `0xBBC403` respectively) to ensure that only a single instance of a job runs at a time. If the lock cannot be acquired immediately, the tick execution SHALL be skipped silently.

#### Scenario: Silent skip when lock is held
- **WHEN** a background job tick triggers and pg_try_advisory_lock returns false
- **THEN** the job tick exits immediately without performing any operations

### Requirement: Watchdog Near-Turn Notification
The watchdog job SHALL run on a 60-second ticker and find all active queue sessions for the current UTC date. For each active session, it SHALL identify waiting remote WhatsApp queue entries whose preceding wait or queue position falls below location thresholds, lock the session first for update, atomically update the entry's presence to 'notified', and enqueue a 'bb_near_turn' template message to the outbox inside the same transaction. An SSE broadcast SHALL occur only after successful transaction commit.

#### Scenario: Near-turn notification triggers outbox and SSE
- **WHEN** watchdog finds a waiting remote WhatsApp entry with wait minutes or people ahead below threshold and no previous near-turn notification
- **THEN** it locks the session, updates presence_state to notified and near_turn_notified_at to now, writes the outbox row, and broadcasts SSE after commit

### Requirement: Watchdog Auto-Snooze
The watchdog job SHALL auto-snooze the top dispatchable waiting entry of a session if it is a remote or notified client when it becomes their turn to be served. The mutation MUST lock the session first, update presence_state to 'snoozed', set is_dispatchable to false, and if they joined via WhatsApp, enqueue a 'bb_queue_snoozed' outbox notification. An SSE broadcast SHALL occur only after successful transaction commit.

#### Scenario: Top remote entry gets auto-snoozed
- **WHEN** the top dispatchable waiting entry has presence remote or notified and there are no arrived customers ahead of them
- **THEN** it locks the session, sets presence to snoozed and is_dispatchable to false, enqueues the outbox event for WhatsApp clients, and triggers SSE broadcast after commit

### Requirement: Watchdog Stale Warning Update
The watchdog job SHALL scan called and in-progress entries and update their stale_warning fields according to per-location threshold columns. This update MUST NOT acquire the session lock and MUST NOT trigger an SSE broadcast.

#### Scenario: Stale warning transition
- **WHEN** a called entry's elapsed time exceeds the location's stale_called_warning_minutes
- **THEN** its stale_warning is updated to called_warning without session locking or SSE broadcast

### Requirement: End-Of-Day Session Expiry
The end-of-day job SHALL run on a 10-minute ticker and identify locations that have reached 2 hours past their scheduled closing time in their local timezone for the current day. For each qualifying session, it MUST lock the session, transition waiting/called/skipped entries to expired, transition in-progress entries to needs_review, set is_dispatchable to false, archive the session, and broadcast SSE after commit without creating any outbox rows.

#### Scenario: EOD archives active session and expires entries
- **WHEN** local time at location is 2 hours past closing time and the queue session is still active/ending
- **THEN** the session is archived, entries are expired or set to needs_review, and SSE is broadcast after commit with zero outbox records

### Requirement: Weekly Summary Generation
The weekly summary job SHALL run on Sunday at 22:00 IST. It SHALL execute a weekly reporting aggregation query with statement_timeout set to 0. For each active location under each active tenant, it SHALL generate an owner_token and enqueue a 'bb_weekly_summary' template message to the outbox.

#### Scenario: Weekly cron schedules summary records
- **WHEN** local time in Asia/Kolkata is Sunday 22:00 IST and the job has not yet run today
- **THEN** the query is run with statement_timeout=0, and an outbox event is created for each active location of active tenants

