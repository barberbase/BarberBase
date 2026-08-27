package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// realMigrationsDir is the path Migrate is given: a file inside migrations/.
// Migrate derives the directory from it, exactly as cmd/server/main.go does.
const realMigrationsEntry = "../../migrations/001_complete_schema.sql"

// adminDSN returns the dev-postgres DSN, same env contract as testPool in
// queue_test.go. A skip here is a FAIL for this unit, not a pass.
func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL or DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

// freshDB creates a throwaway database and returns a pool on it carrying the
// same session parameters InitPool applies in production (statement_timeout=5s,
// lock_timeout=1s, idle_in_transaction_session_timeout=10s), so migrations are
// exercised under the real constraints.
func freshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	dsn := adminDSN(t)

	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	name := "bb_mig_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Skipf("cannot CREATE DATABASE (role lacks CREATEDB?): %v", err)
	}
	t.Cleanup(func() {
		a, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Database = name
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `
			SET lock_timeout = '1s';
			SET statement_timeout = '5s';
			SET idle_in_transaction_session_timeout = '10s';
		`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// execRaw applies a migration file the way it is applied by hand with psql:
// verbatim, no ledger. Used to build prod- and dev-shaped databases.
func execRaw(t *testing.T, pool *pgxpool.Pool, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), string(content))
	require.NoError(t, err)
}

func ledger(t *testing.T, pool *pgxpool.Pool) map[string]struct {
	checksum  string
	appliedAt time.Time
	duration  int
} {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT version, checksum, applied_at, duration_ms FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer rows.Close()

	out := map[string]struct {
		checksum  string
		appliedAt time.Time
		duration  int
	}{}
	for rows.Next() {
		var v, c string
		var at time.Time
		var d int
		require.NoError(t, rows.Scan(&v, &c, &at, &d))
		out[v] = struct {
			checksum  string
			appliedAt time.Time
			duration  int
		}{c, at, d}
	}
	require.NoError(t, rows.Err())
	return out
}

func exists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT to_regclass('public.' || quote_ident($1)) IS NOT NULL`, table).Scan(&ok))
	return ok
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeFixtures writes tiny migrations into a temp dir and returns the entry
// path to hand to Migrate.
func writeFixtures(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var entry string
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		if entry == "" || name < filepath.Base(entry) {
			entry = p
		}
	}
	return entry
}

// --- A1 / A2 / A8 / A9 -------------------------------------------------------

// TestMigrate_FreshDatabase covers A1 (all migrations apply in order, one ledger
// row each), A2 (second run skips everything, applied_at unchanged), A8 (the 001
// plpgsql body survived the wrapper strip) and A9 (checksum is the sha256 of the
// unmodified file on disk).
func TestMigrate_FreshDatabase(t *testing.T) {
	pool := freshDB(t)
	ctx := context.Background()

	t.Setenv("ADOPT_BASELINE", "")
	require.NoError(t, Migrate(ctx, pool, realMigrationsEntry))

	// A1: one row per migration file, both applied, in order.
	first := ledger(t, pool)
	require.Len(t, first, 2, "A1: expected exactly one ledger row per migration")
	require.Contains(t, first, "001")
	require.Contains(t, first, "002")
	require.True(t, first["001"].appliedAt.Before(first["002"].appliedAt) ||
		first["001"].appliedAt.Equal(first["002"].appliedAt), "A1: 001 must be applied before 002")
	require.True(t, exists(t, pool, "tenants"), "A1: 001 objects must exist")
	require.True(t, exists(t, pool, "queue_sessions"), "A1: 001 objects must exist")
	require.True(t, exists(t, pool, "station_devices"), "A1: 002 objects must exist")

	// A9: recorded checksum is lowercase hex sha256 of the raw file bytes.
	want001 := fileSHA(t, "../../migrations/001_complete_schema.sql")
	require.Equal(t, want001, first["001"].checksum, "A9: checksum must match sha256sum of the file")
	require.Equal(t, strings.ToLower(first["001"].checksum), first["001"].checksum, "A9: lowercase hex")
	require.Equal(t, fileSHA(t, "../../migrations/002_station_devices.sql"), first["002"].checksum)

	// A8: 001's wrapper was stripped, and the plpgsql body inside $$ ... $$
	// survived it — set_updated_at exists and actually fires.
	raw001, err := os.ReadFile("../../migrations/001_complete_schema.sql")
	require.NoError(t, err)
	_, stripped, err := stripTxWrapper(string(raw001))
	require.NoError(t, err)
	require.True(t, stripped, "A8: 001's BEGIN;/COMMIT; wrapper must be stripped")

	var tenantID uuid.UUID
	var updatedAt time.Time
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('A8', 'a8-'||substr(gen_random_uuid()::text,1,8), '+919999999999')
		RETURNING id, updated_at`).Scan(&tenantID, &updatedAt))
	var bumped time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`UPDATE tenants SET name = 'A8b' WHERE id = $1 RETURNING updated_at`, tenantID).Scan(&bumped))
	require.True(t, bumped.After(updatedAt), "A8: set_updated_at() trigger must fire")

	// A2: second run is a no-op — everything skipped, nothing rewritten.
	require.NoError(t, Migrate(ctx, pool, realMigrationsEntry))
	second := ledger(t, pool)
	require.Equal(t, first, second, "A2: second run must change nothing")
}

// --- A3 / A4 / A11 -----------------------------------------------------------

// TestMigrate_AdoptBaseline covers A3 (prod shape: 001 applied by hand, ledger
// empty) and A11 (dev shape: 001 and 002 both applied by hand).
func TestMigrate_AdoptBaseline(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handApply  []string
		seedDevice bool
	}{
		{name: "A3_prod_shape_001_only", handApply: []string{"../../migrations/001_complete_schema.sql"}},
		{name: "A11_dev_shape_001_and_002", handApply: []string{
			"../../migrations/001_complete_schema.sql",
			"../../migrations/002_station_devices.sql",
		}, seedDevice: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := freshDB(t)
			ctx := context.Background()
			for _, p := range tc.handApply {
				execRaw(t, pool, p)
			}

			var tenantID, locationID, deviceID uuid.UUID
			if tc.seedDevice {
				require.NoError(t, pool.QueryRow(ctx, `
					INSERT INTO tenants (name, slug, owner_phone_number)
					VALUES ('A11', 'a11', '+919999999999') RETURNING id`).Scan(&tenantID))
				require.NoError(t, pool.QueryRow(ctx, `
					INSERT INTO locations (tenant_id, slug, name)
					VALUES ($1, 'a11-loc', 'A11') RETURNING id`, tenantID).Scan(&locationID))
				require.NoError(t, pool.QueryRow(ctx, `
					INSERT INTO station_devices (tenant_id, location_id, label, token_hash)
					VALUES ($1, $2, 'pre-existing', '\xdeadbeef'::bytea) RETURNING id`,
					tenantID, locationID).Scan(&deviceID))
			}

			require.False(t, exists(t, pool, "schema_migrations"), "precondition: ledger does not exist yet")

			t.Setenv("ADOPT_BASELINE", "1")
			require.NoError(t, Migrate(ctx, pool, realMigrationsEntry))

			l := ledger(t, pool)
			require.Len(t, l, 2)
			// 001 adopted, never executed: duration_ms is 0, and re-executing it
			// inside a transaction would have failed on "relation already exists".
			require.Equal(t, 0, l["001"].duration, "001 must be adopted, not executed")
			require.Equal(t, fileSHA(t, "../../migrations/001_complete_schema.sql"), l["001"].checksum)
			require.True(t, exists(t, pool, "station_devices"), "002 must have been applied")

			if tc.seedDevice {
				// A11: 002 was re-executed (idempotent, IF NOT EXISTS) over live data.
				var label string
				require.NoError(t, pool.QueryRow(ctx,
					`SELECT label FROM station_devices WHERE id = $1`, deviceID).Scan(&label))
				require.Equal(t, "pre-existing", label, "A11: existing data must survive 002 re-execution")
			}
		})
	}
}

// TestMigrate_BaselineRequiresExplicitOptIn covers A4: without an exact "1",
// the runner halts and changes nothing. Any other value fails closed.
func TestMigrate_BaselineRequiresExplicitOptIn(t *testing.T) {
	for _, val := range []string{"", "true", "yes", "ture", "0", "1 "} {
		t.Run("ADOPT_BASELINE="+val, func(t *testing.T) {
			pool := freshDB(t)
			ctx := context.Background()
			execRaw(t, pool, "../../migrations/001_complete_schema.sql")

			t.Setenv("ADOPT_BASELINE", val)
			err := Migrate(ctx, pool, realMigrationsEntry)
			require.Error(t, err, "A4: must halt")
			require.Contains(t, err.Error(), "ADOPT_BASELINE=1")
			require.Empty(t, ledger(t, pool), "A4: ledger must stay empty")
			require.False(t, exists(t, pool, "station_devices"), "A4: 002 must not have been applied")
		})
	}
}

// TestMigrate_RefusesPartialBaseline: queue_sessions is present but the database
// is not a clean 001 — adoption would record a lie, so it halts.
func TestMigrate_RefusesPartialBaseline(t *testing.T) {
	pool := freshDB(t)
	ctx := context.Background()
	execRaw(t, pool, "../../migrations/001_complete_schema.sql")
	_, err := pool.Exec(ctx, `DROP TABLE marketing_campaigns CASCADE`)
	require.NoError(t, err)

	t.Setenv("ADOPT_BASELINE", "1")
	err = Migrate(ctx, pool, realMigrationsEntry)
	require.Error(t, err)
	require.Contains(t, err.Error(), "partially migrated")
	require.Empty(t, ledger(t, pool), "nothing may be recorded")
	require.False(t, exists(t, pool, "station_devices"))
}

// --- A5 / A6 / A10 + discovery, on fixtures ----------------------------------

// TestMigrate_ChecksumMismatch covers A5: editing an applied migration halts and
// writes nothing.
func TestMigrate_ChecksumMismatch(t *testing.T) {
	pool := freshDB(t)
	ctx := context.Background()
	t.Setenv("ADOPT_BASELINE", "")

	entry := writeFixtures(t, map[string]string{
		"001_alpha.sql": "CREATE TABLE alpha (id INT PRIMARY KEY);\n",
	})
	require.NoError(t, Migrate(ctx, pool, entry))
	before := ledger(t, pool)
	require.Len(t, before, 1)

	require.NoError(t, os.WriteFile(entry, []byte("CREATE TABLE alpha (id INT PRIMARY KEY, extra INT);\n"), 0o644))
	err := Migrate(ctx, pool, entry)
	require.Error(t, err, "A5: must halt")
	require.Contains(t, err.Error(), "checksum mismatch")
	require.Equal(t, before, ledger(t, pool), "A5: no writes")
}

// TestMigrate_FixtureFailures is the table-driven set: A6 (a migration with a
// syntax error rolls back and stops the run), A10 (half a transaction wrapper),
// and the discovery guards.
func TestMigrate_FixtureFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      map[string]string
		wantErr    string
		wantTables []string // must NOT exist afterwards
		wantLedger []string // versions that must be recorded
	}{
		{
			name: "A6_syntax_error_rolls_back_and_halts",
			files: map[string]string{
				"001_alpha.sql": "CREATE TABLE alpha (id INT PRIMARY KEY);\n",
				"002_broken.sql": "CREATE TABLE beta (id INT PRIMARY KEY);\n" +
					"THIS IS NOT SQL;\n",
				"003_gamma.sql": "CREATE TABLE gamma (id INT PRIMARY KEY);\n",
			},
			wantErr:    "rolled back",
			wantTables: []string{"beta", "gamma"},
			wantLedger: []string{"001"},
		},
		{
			name: "A10_begin_without_commit",
			files: map[string]string{
				"001_alpha.sql": "BEGIN;\nCREATE TABLE alpha (id INT PRIMARY KEY);\n",
			},
			wantErr:    "does not end with COMMIT;",
			wantTables: []string{"alpha"},
		},
		{
			name: "A10_commit_without_begin",
			files: map[string]string{
				"001_alpha.sql": "CREATE TABLE alpha (id INT PRIMARY KEY);\nCOMMIT;\n",
			},
			wantErr:    "without a matching leading BEGIN;",
			wantTables: []string{"alpha"},
		},
		{
			name: "duplicate_version_prefix",
			files: map[string]string{
				"001_alpha.sql":  "CREATE TABLE alpha (id INT PRIMARY KEY);\n",
				"001_hotfix.sql": "CREATE TABLE hotfix (id INT PRIMARY KEY);\n",
			},
			wantErr:    "duplicate migration version 001",
			wantTables: []string{"alpha", "hotfix"},
		},
		{
			name: "unversioned_file_is_not_silently_skipped",
			files: map[string]string{
				"001_alpha.sql": "CREATE TABLE alpha (id INT PRIMARY KEY);\n",
				"scratch.sql":   "CREATE TABLE scratch (id INT PRIMARY KEY);\n",
			},
			wantErr:    "does not match NNN_name.sql",
			wantTables: []string{"alpha", "scratch"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := freshDB(t)
			ctx := context.Background()
			t.Setenv("ADOPT_BASELINE", "")

			err := Migrate(ctx, pool, writeFixtures(t, tc.files))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)

			for _, tbl := range tc.wantTables {
				require.False(t, exists(t, pool, tbl), "%s must not exist after halt", tbl)
			}
			l := ledger(t, pool)
			require.Len(t, l, len(tc.wantLedger))
			for _, v := range tc.wantLedger {
				require.Contains(t, l, v)
			}
		})
	}
}

// TestStripTxWrapper is a pure unit check of the ends-only strip rule: it never
// inspects the middle of a file, so a $$ ... BEGIN ... END; $$ body is untouched.
func TestStripTxWrapper(t *testing.T) {
	body := "CREATE FUNCTION f() RETURNS TRIGGER AS $$\nBEGIN\n RETURN NEW;\nEND;\n$$ LANGUAGE plpgsql;"
	out, stripped, err := stripTxWrapper("-- header\n\nBEGIN;\n" + body + "\nCOMMIT;\n")
	require.NoError(t, err)
	require.True(t, stripped)
	require.NotContains(t, out, "BEGIN;\nCREATE FUNCTION")
	require.Contains(t, out, body, "the plpgsql body must be untouched")
	require.NotContains(t, strings.TrimSpace(out), "\nCOMMIT;")

	out, stripped, err = stripTxWrapper("CREATE TABLE a (id INT);\n")
	require.NoError(t, err)
	require.False(t, stripped)
	require.Contains(t, out, "CREATE TABLE a")

	// A leading block comment means BEGIN; is not the first meaningful line;
	// that must fail loudly rather than silently skip the strip.
	_, _, err = stripTxWrapper("/* header */\nBEGIN;\nCREATE TABLE a (id INT);\nCOMMIT;\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without a matching leading BEGIN;")
}
