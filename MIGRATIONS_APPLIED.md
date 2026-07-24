# Migrations Applied

Ledger of schema migrations and where they have been applied. `repository.Migrate`
only bootstraps `001` on an empty database — every later migration is applied
manually with psql and logged here.

Apply command (droplet):

```bash
docker exec -i barberbase-postgres psql -U bb_user -d barberbase < barberbase-core/migrations/00N_name.sql
```

| # | File | What | Dev DB | Prod DB |
|---|------|------|--------|---------|
| 001 | `barberbase-core/migrations/001_complete_schema.sql` | Baseline complete schema | applied (bootstrap) | applied (bootstrap) |
| 002 | `barberbase-core/migrations/002_station_devices.sql` | station_devices + station_buttons (device call-next layer) | applied 2026-07-20 | pending |

## Manual data fixes (prod)

2026-07-24 — end_of_day backfill (star-salon location de8f01c9…)
Cause: pre-e78973f end_of_day.go:79 joined only business_date = today(loc);
sessions triggering after midnight were never archived.
Action: expired 5 stranded `waiting` entries (→ expired, is_dispatchable=false),
archived 5 sessions (2026-06-28/29, 07-04, 07-13, 07-20), closed_at backfilled
to end-of-business-date IST, archived_at = now.
Rows: UPDATE 5 / UPDATE 5. Verified 0 remaining.
