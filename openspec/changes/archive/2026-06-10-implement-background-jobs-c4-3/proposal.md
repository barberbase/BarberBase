## Why

Currently, BarberBase lacks background automated processing for watchdog status monitoring, end-of-day queue cleanup, and weekly performance reporting. Implementing these jobs ensures queue freshness, automatic snoozing of absent clients, stale state tracking, and consistent retention analytics for shop owners.

## What Changes

- **NEW**: Add a background jobs package containing watchdog, end-of-day, and weekly summary processes.
- **NEW**: Configure Postgres advisory locking for each background job to support single-instance execution in multi-node environments.
- **NEW**: Add watchdog logic to check for near-turn entries, auto-snooze absent remote clients, and update stale state warnings.
- **NEW**: Add end-of-day logic to automatically transition waiting/called/skipped entries to expired, flag in-progress entries for review, and archive open queue sessions.
- **NEW**: Add weekly summary logic to run a comprehensive performance aggregation query on Sunday 22:00 IST and queue WhatsApp summary notifications for active tenant owners.
- **MODIFY**: Register and start the background jobs as concurrent goroutines in the main server start sequence.

## Capabilities

### New Capabilities
- `background-jobs`: Automated background execution for watchdog ticks, end-of-day processing, and weekly report compilation.

### Modified Capabilities
<!-- No requirement changes to existing capabilities -->

## Impact

- **Affected Code**: `cmd/server/main.go` (registration and lifecycle management) and a new package `internal/jobs/`.
- **APIs**: Outbox event queueing (`outbox_events` table) and SSE manager broadcasts.
- **Dependencies**: Uses the existing pgx connection pool and config parameters. No new Go dependencies are introduced.
