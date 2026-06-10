## Context

BarberBase requires three background operations:
1. **Watchdog**: Tick every 60 seconds to monitor and alert customers, auto-snooze if they are not present, and update stale alerts.
2. **End of Day (EOD)**: Run every 10 minutes to auto-expire remaining active queue entries and archive session 2 hours after location close.
3. **Weekly Summary**: Compile and dispatch shop performance reports to owners every Sunday at 22:00 IST.

These jobs must be multi-node safe using Postgres advisory locks, adhere to critical transactional rules (e.g. Law 1, Law 7, Law 8), and run without blocking on global timeout limits (Weekly Summary query timeout = 0).

## Goals / Non-Goals

**Goals:**
- Implement watchdog task runner with near-turn alerts, auto-snoozing, and stale warning updates.
- Implement EOD task runner with auto-archiving.
- Implement Weekly Summary task runner with aggregation and WhatsApp template queueing.
- Ensure strict concurrency control using pg_try_advisory_lock.
- Integrate jobs into server startup goroutines.

**Non-Goals:**
- No frontend changes in barberbase-frontend.
- No modifications to database schemas or migrations.
- No manual queue worker handling (handled in C2.2/C3.4).

## Decisions

### 1. File Structure & Constructors
Each job is self-contained in its own file under `internal/jobs/`.
- `internal/jobs/watchdog.go`: `Watchdog` struct, `NewWatchdog` constructor, `Start(ctx)` method.
- `internal/jobs/end_of_day.go`: `EndOfDay` struct, `NewEndOfDay` constructor, `Start(ctx)` method.
- `internal/jobs/weekly_summary.go`: `WeeklySummary` struct, `NewWeeklySummary` constructor, `Start(ctx)` method.

### 2. Advisory Locking Pattern
Advisory lock keys are defined as int64 constants inside `internal/jobs/watchdog.go`. Since all files reside in the same `jobs` package, these package-level constants are visible to `end_of_day.go` and `weekly_summary.go` as well:
- `advisoryLockWatchdog` = `0xBBC401`
- `advisoryLockEndOfDay` = `0xBBC402`
- `advisoryLockWeeklySummary` = `0xBBC403`
The locks are acquired per tick via `pg_try_advisory_lock` and unlocked via `pg_advisory_unlock` inside a deferred statement to ensure they are cleaned up.

### 3. Goroutine Lifecycles
The `Start(ctx context.Context)` methods launch goroutines that run tickers or periodic checks and gracefully terminate when the context is cancelled.

### 4. Watchdog Processing
- **Locking**: Session updates (Near-Turn and Auto-Snooze) acquire a session lock first (`SELECT id FROM queue_sessions WHERE id = $id FOR UPDATE`) to satisfy Law 1. Stale warning updates are display-only and do not lock the session.
- **Outbox**: Outbox inserts occur in the same transaction as queue state updates (Law 7).
- **SSE Broadcast**: SSE notifications are broadcasted after the transaction commits (Law 8).

### 5. EOD Processing
- time.LoadLocation("Asia/Kolkata") and location timezone math is used to compare with UTC now and closing times.
- Updates session status to archived and all open entries to expired (waiting/called/skipped) or needs_review (in_progress).
- Broadcasts SSE after transaction commit (Law 8).

### 6. Weekly Summary Generation
- Calculated relative to Sunday 22:00 IST. Uses a `lastRunDate` variable to prevent multiple runs on the same Sunday.
- Temporarily sets `statement_timeout = 0` using a connection-specific session override to prevent global 5s limits from failing the aggregation query.
- Generates base64url signed JWT-like owner deep link tokens using the HMAC secret.
- Enqueues weekly summary notifications in the outbox.

## Risks / Trade-offs

- **Risk: Lock timeout during concurrency** → **Mitigation**: Keep transactions extremely short, lock queue sessions at the very beginning of the transaction, and release quickly.
- **Risk: Global statement timeout aborting Weekly Summary** → **Mitigation**: Exclusively use `conn.Exec(ctx, "SET LOCAL statement_timeout = 0")` inside a single connection lease rather than general pool queries.
