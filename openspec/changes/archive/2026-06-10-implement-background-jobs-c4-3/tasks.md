## 1. Setup and Advisory Lock Constants

- [x] 1.1 Define the required advisory lock constants in `internal/jobs/watchdog.go` at the package level.

## 2. Implement Watchdog Background Job

- [x] 2.1 Create `internal/jobs/watchdog.go` with `Watchdog` struct, constructor, `Start(ctx)` loop, and package-level advisory lock constants.
- [x] 2.2 Implement watchdog ticks executing every 60 seconds with PG advisory lock checks.
- [x] 2.3 Implement watchdog Near-Turn checks inside transaction: locking session, updating queue entry presence to notified, enqueuing 'bb_near_turn' outbox event, and broadcasting SSE after commit.
- [x] 2.4 Implement watchdog Auto-Snooze checks inside transaction: locking session, setting top remote/notified waiting entry presence to snoozed and is_dispatchable to false, enqueuing 'bb_queue_snoozed' outbox event for WhatsApp channel clients, and broadcasting SSE after commit.
- [x] 2.5 Implement watchdog Stale Warning updates for called and in-progress entries matching location threshold columns without locking the session or broadcasting SSE.

## 3. Implement End-Of-Day Background Job

- [x] 3.1 Create `internal/jobs/end_of_day.go` with `EndOfDay` struct, constructor, and `Start(ctx)` loop.
- [x] 3.2 Implement EOD ticks running every 10 minutes with PG advisory lock checks.
- [x] 3.3 Implement EOD logic: fetch locations whose closes_at timezone-relative is 2 hours past now, run EOD database updates (locking session, expiring waiting/called/skipped, marking in_progress to needs_review, archiving session, updating queue version), and broadcasting SSE after commit.

## 4. Implement Weekly Summary Background Job

- [x] 4.1 Create `internal/jobs/weekly_summary.go` with `WeeklySummary` struct, constructor, and `Start(ctx)` loop checking for Sunday 22:00 IST.
- [x] 4.2 Implement weekly summary aggregation query with `statement_timeout = 0`.
- [x] 4.3 Implement deep link owner token generation using HMAC-SHA256 signature logic.
- [x] 4.4 Implement creation of 'bb_weekly_summary' outbox rows for each active location of active tenants.

## 5. Register background jobs in main server

- [x] 5.1 Edit `cmd/server/main.go` to import jobs package, construct job instances, and run `watchdog.Start(ctx)`, `eod.Start(ctx)`, and `weekly.Start(ctx)` as concurrent goroutines.

## 6. Integration and Testing

- [x] 6.1 Create and run integration tests for the background jobs and verify all unit tests pass with `make build` and `make test`.
- [x] 6.2 Explicitly test that the heavy weekly summary query runs with `statement_timeout = 0` and does not abort at the global 5s limit (e.g. by setting a session timeout of 1ms, running a query with pg_sleep to ensure it would fail, and verifying that the weekly summary connection overrides it to 0 / successfully runs despite the active session-level timeout).
- [x] 6.3 Explicitly test that the weekly summary enqueues reports exclusively for active tenants by seeding an inactive tenant and asserting that zero outbox rows are written for that tenant.
