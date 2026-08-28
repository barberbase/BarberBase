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

## The runner is the only thing that applies migrations

Once the ledger exists, **never apply a migration by hand.** Not with `psql`, not
"just this once on prod". Run the app; the runner applies what is pending.

This is not style. `ADOPT_BASELINE` records **001 and nothing else**, so every
migration between 001 and head is re-executed on the adopt run. That is survivable
only where the migration is idempotent — 002 is, by construction, which is the
only reason the 2026-08-28 prod adopt succeeded. 003 and 004 are plain
`CREATE TABLE` / `ALTER TABLE ADD COLUMN` with no `IF NOT EXISTS`; re-running
either raises `42P07` or `42701`.

The runner sits on the boot path behind `log.Fatalf` (`cmd/server/main.go:76`),
so that error is not a warning in a log — the process dies and the container never
comes up. A hand-applied migration that never reached the ledger turns the next
deploy into an outage.

The same reasoning kills manual tracking files. A `MIGRATIONS_APPLIED.md` was kept
here until 2026-08-28 and was deleted because it had recorded 002 as *pending on
prod* when prod had been running it since July. `schema_migrations` is the ledger;
a second one that can disagree with it is worse than none.

## `media_assets.content_hash` is client-asserted

The value in `content_hash` is supplied by the browser and **cannot be verified**.
Verifying it would mean reading the object's bytes, and the media pipeline's
central constraint is that object bytes never touch the droplet — one decoded
12MP JPEG costs ~48MB against a 250MiB `GOMEMLIMIT`.

So treat it as a cache key and a dedup key, never an integrity proof. Its
*shape* is validated at presign (exactly 64 lowercase hex, else 400), because a
malformed hash yields a malformed R2 key. Its *value* is trusted only insofar as
the blast radius allows: keys are scoped to `location_id`, and below that to
`service_variant_id` or `staff_member_id`, so a forged or colliding hash can only
collide with objects that same shop already owns. **Nothing security-relevant may
ever depend on this column.**

Two consequences worth knowing. The key embeds only the first 16 hex characters —
64 bits, ample against accidental collision inside one location's keyspace, which
is the only scope in which a collision is reachable. And because keys are
content-addressed, objects are immutable and served with
`Cache-Control: max-age=31536000, immutable`; a shop that PUTs *different* bytes
under a hash it has already used overwrites its own object and sees stale CDN
content for up to a year. That is the price of immutable caching and it is the
right trade, but it is a real behaviour, not a theoretical one.

R2 returns an ETag on the commit-time HEAD. It is logged, not stored:
`media_assets` has no column for it and 003 is frozen. It would be useful for
detecting an object replaced under an existing key — a candidate for the next
migration, not a reason to open one.

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
