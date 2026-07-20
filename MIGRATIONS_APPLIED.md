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
