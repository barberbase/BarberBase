# Migrations

Applied state lives in the `schema_migrations` table, owned by
`internal/repository/migration.go`. The runner executes on app boot
(`cmd/server/main.go`), applies every pending migration in version order, and
writes each ledger row in the **same transaction** as the migration it records.
A partial apply is impossible; any failure halts the run and later migrations do
not execute.

## Writing a migration

- Name it `NNN_snake_name.sql`. Every `.sql` file in this directory must match,
  and two files resolving to the same `NNN` is a hard error at discovery.
- **Do not include your own `BEGIN;` / `COMMIT;`.** The runner owns the
  transaction. `001_complete_schema.sql` predates the runner and carries its
  own wrapper, which is stripped for backward compatibility only — do not copy
  that pattern. A file with only half a wrapper is rejected outright.
- Connections run with `statement_timeout=5s` and `lock_timeout=1s`. A migration
  needing longer sets its own `SET LOCAL statement_timeout = '...'` at the top of
  its own file. The runner never changes these globally.
- Prefer `IF NOT EXISTS` where it is free; it makes a re-run after a hand-applied
  deploy harmless.

## Checksums

The ledger stores the lowercase hex SHA-256 of the **unmodified file bytes**, so
`sha256sum migrations/00N_name.sql` reproduces the stored value. Editing a
migration that has already been applied halts the runner with a mismatch error.
Never edit an applied migration — add a new one.

## Adopting an existing database (`ADOPT_BASELINE`)

A database created before the ledger existed has the 001 schema but no
`schema_migrations` rows. The runner refuses to guess and halts. To adopt it:

```bash
ADOPT_BASELINE=1 ./server     # records 001 without executing it, then applies 002+
```

- The value must be exactly `1`. `true`, `yes`, a typo, or empty all resolve to
  false and the runner halts — fail closed.
- Adoption only happens when `schema_migrations` is empty **and** `queue_sessions`
  exists **and** every table 001 creates is present. A partially migrated
  database is rejected rather than recorded as a clean baseline.
- **Unset it immediately after the adopt run.** Leaving `ADOPT_BASELINE=1` in a
  `.env` turns the guard into a no-op. The runner logs a warning if it is set
  while the ledger is already populated.
