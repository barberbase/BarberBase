package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

	// A1: one row per migration file, all applied, in order.
	first := ledger(t, pool)
	require.Len(t, first, countMigrationFiles(t), "A1: expected exactly one ledger row per migration")
	require.Contains(t, first, "001")
	require.Contains(t, first, "002")
	require.Contains(t, first, "003")
	require.Contains(t, first, "004")
	require.True(t, first["001"].appliedAt.Before(first["002"].appliedAt) ||
		first["001"].appliedAt.Equal(first["002"].appliedAt), "A1: 001 must be applied before 002")
	require.True(t, exists(t, pool, "tenants"), "A1: 001 objects must exist")
	require.True(t, exists(t, pool, "queue_sessions"), "A1: 001 objects must exist")
	require.True(t, exists(t, pool, "station_devices"), "A1: 002 objects must exist")
	require.True(t, exists(t, pool, "media_assets"), "A1: 003 objects must exist")
	require.True(t, exists(t, pool, "catalog_style_templates"), "A1: 003 objects must exist")
	require.True(t, exists(t, pool, "staff_tiers"), "A1: 004 objects must exist")
	require.True(t, exists(t, pool, "service_variant_tier_prices"), "A1: 004 objects must exist")

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
			require.Len(t, l, countMigrationFiles(t))
			// 001 adopted, never executed: duration_ms is 0, and re-executing it
			// inside a transaction would have failed on "relation already exists".
			require.Equal(t, 0, l["001"].duration, "001 must be adopted, not executed")
			require.Equal(t, fileSHA(t, "../../migrations/001_complete_schema.sql"), l["001"].checksum)
			require.True(t, exists(t, pool, "station_devices"), "002 must have been applied")
			// A9: 003 applies on top of an adopted baseline without error.
			require.True(t, exists(t, pool, "media_assets"), "A9: 003 must apply over an adopted baseline")
			require.True(t, exists(t, pool, "staff_tiers"), "A9: 004 must apply over an adopted baseline")
			require.Contains(t, l, "003")
			require.Contains(t, l, "004")

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

// --- M1: 003_media_and_catalog ------------------------------------------------

// countMigrationFiles is the expected ledger size: one row per .sql file on
// disk. Derived rather than hardcoded so adding 004 does not break A1/A3.
func countMigrationFiles(t *testing.T) int {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(realMigrationsEntry), "*.sql"))
	require.NoError(t, err)
	return len(paths)
}

const migration003 = "../../migrations/003_media_and_catalog.sql"

// pgErrCode returns the SQLSTATE of a Postgres error, or "" if it is not one.
// Constraint assertions compare codes, never message text.
func pgErrCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

const (
	sqlstateUniqueViolation = "23505"
	sqlstateCheckViolation  = "23514"
)

// mediaFixture is a seeded location with one service variant and one staff
// member — the FK targets every media_assets row needs.
type mediaFixture struct {
	tenantID  uuid.UUID
	locID     uuid.UUID
	variantID uuid.UUID
	staffID   uuid.UUID
}

// seedMediaFixture walks the 001 chain: tenant → location → category → group →
// variant, plus a staff member.
func seedMediaFixture(t *testing.T, pool *pgxpool.Pool) mediaFixture {
	t.Helper()
	ctx := context.Background()
	var f mediaFixture
	suffix := uuid.NewString()[:8]

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('M1 Tenant', $1, '+919999900000') RETURNING id`, "m1-"+suffix).Scan(&f.tenantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, 'M1 Location') RETURNING id`, f.tenantID, "m1loc-"+suffix).Scan(&f.locID))

	var categoryID, groupID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_categories (tenant_id, location_id, name, gender)
		VALUES ($1, $2, 'Hair', 'men') RETURNING id`, f.tenantID, f.locID).Scan(&categoryID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_groups (tenant_id, location_id, category_id, name)
		VALUES ($1, $2, $3, 'Fade') RETURNING id`, f.tenantID, f.locID, categoryID).Scan(&groupID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_variants (tenant_id, location_id, group_id, name, duration_minutes, price_paise)
		VALUES ($1, $2, $3, 'Mid Fade', 30, 25000) RETURNING id`,
		f.tenantID, f.locID, groupID).Scan(&f.variantID))

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role)
		VALUES ($1, $2, 'M1 Barber', $3, 'barber') RETURNING id`,
		f.tenantID, f.locID, "+9199"+suffix).Scan(&f.staffID))
	return f
}

// insertAsset inserts one media_assets row and returns the error verbatim so the
// caller can assert on its SQLSTATE.
func insertAsset(pool *pgxpool.Pool, f mediaFixture, purpose, status string, isPrimary bool,
	variantID, staffID *uuid.UUID, key, hash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO media_assets
			(tenant_id, location_id, purpose, service_variant_id, staff_member_id,
			 r2_key, content_hash, is_primary, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		f.tenantID, f.locID, purpose, variantID, staffID, key, hash, isPrimary, status).Scan(&id)
	return id, err
}

// migratedPool is a scratch database with every migration applied.
func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	t.Setenv("ADOPT_BASELINE", "")
	pool := freshDB(t)
	require.NoError(t, Migrate(context.Background(), pool, realMigrationsEntry))
	return pool
}

// TestMigration003_NoTransactionControl is A3: the runner owns the transaction,
// so 003 must not carry its own BEGIN;/COMMIT;.
func TestMigration003_NoTransactionControl(t *testing.T) {
	raw, err := os.ReadFile(migration003)
	require.NoError(t, err)

	_, stripped, err := stripTxWrapper(string(raw))
	require.NoError(t, err)
	require.False(t, stripped, "A3: 003 must have no transaction wrapper to strip")

	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.ToUpper(strings.TrimSpace(line))
		require.NotEqual(t, "BEGIN;", trimmed, "A3: bare BEGIN; at line %d", i+1)
		require.NotEqual(t, "COMMIT;", trimmed, "A3: bare COMMIT; at line %d", i+1)
	}
}

// TestMigration003_PartialUniqueIndexes is A4. Every "one live X" rule is a
// partial index over status='ready', so superseded rows may coexist.
func TestMigration003_PartialUniqueIndexes(t *testing.T) {
	pool := migratedPool(t)
	f := seedMediaFixture(t, pool)

	t.Run("one_ready_primary_per_variant", func(t *testing.T) {
		_, err := insertAsset(pool, f, "service_ref", "ready", true, &f.variantID, nil, "k/p1", "h1")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "service_ref", "ready", true, &f.variantID, nil, "k/p2", "h2")
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: second ready primary must be rejected")
	})

	t.Run("pending_and_archived_coexist_with_ready", func(t *testing.T) {
		_, err := insertAsset(pool, f, "service_ref", "pending", true, &f.variantID, nil, "k/p3", "h3")
		require.NoError(t, err, "A4: a pending primary may sit alongside a ready one")
		_, err = insertAsset(pool, f, "service_ref", "archived", true, &f.variantID, nil, "k/p4", "h4")
		require.NoError(t, err, "A4: an archived primary may sit alongside a ready one")
	})

	t.Run("one_ready_logo_per_location", func(t *testing.T) {
		_, err := insertAsset(pool, f, "location_logo", "ready", false, nil, nil, "k/l1", "h5")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "location_logo", "ready", false, nil, nil, "k/l2", "h6")
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: second ready logo must be rejected")
	})

	t.Run("one_ready_avatar_per_staff", func(t *testing.T) {
		_, err := insertAsset(pool, f, "staff_avatar", "ready", false, nil, &f.staffID, "k/a1", "h7")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "staff_avatar", "ready", false, nil, &f.staffID, "k/a2", "h8")
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: second ready avatar must be rejected")
	})

	t.Run("same_bytes_twice_on_one_variant", func(t *testing.T) {
		_, err := insertAsset(pool, f, "service_ref", "ready", false, &f.variantID, nil, "k/d1", "dup")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "service_ref", "pending", false, &f.variantID, nil, "k/d2", "dup")
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: duplicate content_hash per variant rejected")
	})

	t.Run("location_cover_is_unconstrained", func(t *testing.T) {
		_, err := insertAsset(pool, f, "location_cover", "ready", false, nil, nil, "k/c1", "h9")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "location_cover", "ready", false, nil, nil, "k/c2", "h10")
		require.NoError(t, err, "covers are deliberately not limited to one per location")
	})
}

// TestMigration003_PurposeChecks is A5: the purpose/FK pairing is total in both
// directions.
func TestMigration003_PurposeChecks(t *testing.T) {
	pool := migratedPool(t)
	f := seedMediaFixture(t, pool)

	for _, tc := range []struct {
		name      string
		purpose   string
		variantID *uuid.UUID
		staffID   *uuid.UUID
	}{
		{"service_ref_without_variant", "service_ref", nil, nil},
		{"location_logo_with_variant", "location_logo", &f.variantID, nil},
		{"location_cover_with_variant", "location_cover", &f.variantID, nil},
		{"staff_avatar_without_staff", "staff_avatar", nil, nil},
		{"location_logo_with_staff", "location_logo", nil, &f.staffID},
		{"service_ref_with_staff_and_no_variant", "service_ref", nil, &f.staffID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := insertAsset(pool, f, tc.purpose, "ready", false, tc.variantID, tc.staffID,
				"k/"+tc.name, "h-"+tc.name)
			require.Equal(t, sqlstateCheckViolation, pgErrCode(err), "A5: %s must violate a CHECK", tc.name)
		})
	}

	t.Run("valid_pairings_accepted", func(t *testing.T) {
		_, err := insertAsset(pool, f, "service_ref", "ready", false, &f.variantID, nil, "k/ok1", "ok1")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "staff_avatar", "ready", false, nil, &f.staffID, "k/ok2", "ok2")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "location_logo", "ready", false, nil, nil, "k/ok3", "ok3")
		require.NoError(t, err)
	})
}

// TestMigration003_Cascades is A6 and A7.
func TestMigration003_Cascades(t *testing.T) {
	ctx := context.Background()

	count := func(t *testing.T, pool *pgxpool.Pool, where string, arg any) int {
		t.Helper()
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM media_assets WHERE `+where, arg).Scan(&n))
		return n
	}

	t.Run("A6_variant_delete_cascades", func(t *testing.T) {
		pool := migratedPool(t)
		f := seedMediaFixture(t, pool)
		_, err := insertAsset(pool, f, "service_ref", "ready", true, &f.variantID, nil, "k/v1", "v1")
		require.NoError(t, err)
		require.Equal(t, 1, count(t, pool, "service_variant_id = $1", f.variantID))

		_, err = pool.Exec(ctx, `DELETE FROM service_variants WHERE id = $1`, f.variantID)
		require.NoError(t, err)
		require.Zero(t, count(t, pool, "service_variant_id = $1", f.variantID))
	})

	t.Run("A6_staff_delete_cascades", func(t *testing.T) {
		pool := migratedPool(t)
		f := seedMediaFixture(t, pool)
		_, err := insertAsset(pool, f, "staff_avatar", "ready", false, nil, &f.staffID, "k/s1", "s1")
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `DELETE FROM staff_members WHERE id = $1`, f.staffID)
		require.NoError(t, err)
		require.Zero(t, count(t, pool, "staff_member_id = $1", f.staffID))
	})

	t.Run("A6_location_delete_cascades", func(t *testing.T) {
		pool := migratedPool(t)
		f := seedMediaFixture(t, pool)
		_, err := insertAsset(pool, f, "service_ref", "ready", true, &f.variantID, nil, "k/L1", "L1")
		require.NoError(t, err)
		_, err = insertAsset(pool, f, "location_logo", "ready", false, nil, nil, "k/L2", "L2")
		require.NoError(t, err)
		require.Equal(t, 2, count(t, pool, "location_id = $1", f.locID))

		// staff_members.location_id has no ON DELETE CASCADE (001:216), so the
		// staff row must go first — that constraint predates this migration.
		_, err = pool.Exec(ctx, `DELETE FROM staff_members WHERE location_id = $1`, f.locID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, f.locID)
		require.NoError(t, err, "the locations <-> media_assets FK cycle must not block the delete")
		require.Zero(t, count(t, pool, "location_id = $1", f.locID))
	})

	t.Run("A7_logo_delete_nulls_fk_and_keeps_key", func(t *testing.T) {
		pool := migratedPool(t)
		f := seedMediaFixture(t, pool)
		assetID, err := insertAsset(pool, f, "location_logo", "ready", false, nil, nil, "r2/logo.webp", "lg")
		require.NoError(t, err)

		_, err = pool.Exec(ctx,
			`UPDATE locations SET logo_asset_id = $1, logo_key = 'r2/logo.webp' WHERE id = $2`,
			assetID, f.locID)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, assetID)
		require.NoError(t, err)

		var gotAsset *uuid.UUID
		var gotKey *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT logo_asset_id, logo_key FROM locations WHERE id = $1`, f.locID).Scan(&gotAsset, &gotKey))
		require.Nil(t, gotAsset, "A7: logo_asset_id must be set NULL")
		require.NotNil(t, gotKey, "A7: logo_key must survive the delete")
		require.Equal(t, "r2/logo.webp", *gotKey, "A7: the denormalised key is the whole point")
	})
}

// TestMigration003_CatalogUniqueness is A8. The catalog is platform-global, so
// its uniqueness is on the taxonomy tuple, not on a tenant.
func TestMigration003_CatalogUniqueness(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()

	insert := func(gender string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO catalog_style_templates
				(category_name, group_name, variant_name, gender, default_duration_minutes)
			VALUES ('Hair', 'Fade', 'Mid Fade', $1, 30)`, gender)
		return err
	}

	require.NoError(t, insert("men"))
	require.Equal(t, sqlstateUniqueViolation, pgErrCode(insert("men")),
		"A8: duplicate (category, group, variant, gender) must be rejected")
	require.NoError(t, insert("women"), "A8: the same triple under another gender is a distinct row")
	require.NoError(t, insert("unisex"))

	// Platform-global by construction: no tenant_id column to scope by.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'catalog_style_templates' AND column_name = 'tenant_id'`).Scan(&n))
	require.Zero(t, n, "catalog_style_templates must stay platform-global (no tenant_id)")

	_, err := pool.Exec(ctx, `
		INSERT INTO catalog_style_templates
			(category_name, group_name, variant_name, gender, default_duration_minutes)
		VALUES ('Hair', 'Fade', 'Zero Duration', 'men', 0)`)
	require.Equal(t, sqlstateCheckViolation, pgErrCode(err), "default_duration_minutes must be positive")
}

// --- T1: 004_staff_tiers -------------------------------------------------------

const migration004 = "../../migrations/004_staff_tiers.sql"

const sqlstateFKViolation = "23503"

// tierFixture extends mediaFixture with a queue session, so the dispatch and
// visit_services cases have somewhere to hang rows.
type tierFixture struct {
	mediaFixture
	sessionID uuid.UUID
}

func seedTierFixture(t *testing.T, pool *pgxpool.Pool) tierFixture {
	t.Helper()
	f := tierFixture{mediaFixture: seedMediaFixture(t, pool)}
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date)
		VALUES ($1, $2, DATE '2099-01-01') RETURNING id`,
		f.tenantID, f.locID).Scan(&f.sessionID))
	return f
}

func insertTier(pool *pgxpool.Pool, f tierFixture, name string, rank int, isDefault, isActive bool) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO staff_tiers (tenant_id, location_id, name, rank, is_default, is_active)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		f.tenantID, f.locID, name, rank, isDefault, isActive).Scan(&id)
	return id, err
}

// TestMigration004_NoTransactionControl is A2.
func TestMigration004_NoTransactionControl(t *testing.T) {
	raw, err := os.ReadFile(migration004)
	require.NoError(t, err)

	_, stripped, err := stripTxWrapper(string(raw))
	require.NoError(t, err)
	require.False(t, stripped, "A2: 004 must have no transaction wrapper to strip")

	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.ToUpper(strings.TrimSpace(line))
		require.NotEqual(t, "BEGIN;", trimmed, "A2: bare BEGIN; at line %d", i+1)
		require.NotEqual(t, "COMMIT;", trimmed, "A2: bare COMMIT; at line %d", i+1)
	}
}

// TestMigration004_TierConstraints is A3 and A4.
func TestMigration004_TierConstraints(t *testing.T) {
	pool := migratedPool(t)
	f := seedTierFixture(t, pool)

	t.Run("A3_one_live_default_per_location", func(t *testing.T) {
		_, err := insertTier(pool, f, "Junior", 10, true, true)
		require.NoError(t, err)
		_, err = insertTier(pool, f, "Senior", 20, true, true)
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A3: second live default rejected")

		// A retired tier may still carry is_default — the index is partial on is_active.
		_, err = insertTier(pool, f, "Retired", 5, true, false)
		require.NoError(t, err, "A3: an inactive default may coexist with the live one")
	})

	t.Run("A4_name_and_rank_unique_per_location", func(t *testing.T) {
		_, err := insertTier(pool, f, "Junior", 99, false, true)
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: duplicate name rejected")
		_, err = insertTier(pool, f, "Expert", 10, false, true)
		require.Equal(t, sqlstateUniqueViolation, pgErrCode(err), "A4: duplicate rank rejected")
	})

	t.Run("A4_same_name_and_rank_under_another_location", func(t *testing.T) {
		other := seedTierFixture(t, pool)
		_, err := insertTier(pool, other, "Junior", 10, true, true)
		require.NoError(t, err, "A4: uniqueness is scoped to the location")
	})
}

// TestMigration004_TierPrices is A5: sparse overrides, with NULL duration
// meaning inherit.
func TestMigration004_TierPrices(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	f := seedTierFixture(t, pool)
	tierID, err := insertTier(pool, f, "Senior", 20, true, true)
	require.NoError(t, err)

	insertPrice := func(price int, duration *int) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO service_variant_tier_prices
				(tenant_id, location_id, service_variant_id, tier_id, price_paise, duration_minutes)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			f.tenantID, f.locID, f.variantID, tierID, price, duration)
		return err
	}

	require.NoError(t, insertPrice(45000, nil), "A5: NULL duration means inherit the variant's")
	require.Equal(t, sqlstateUniqueViolation, pgErrCode(insertPrice(50000, nil)),
		"A5: one override row per (variant, tier)")

	// A second tier so the negative cases are not masked by the UNIQUE above.
	tier2, err := insertTier(pool, f, "Expert", 30, false, true)
	require.NoError(t, err)
	negative := func(price int, duration *int) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO service_variant_tier_prices
				(tenant_id, location_id, service_variant_id, tier_id, price_paise, duration_minutes)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			f.tenantID, f.locID, f.variantID, tier2, price, duration)
		return err
	}
	zero := 0
	require.Equal(t, sqlstateCheckViolation, pgErrCode(negative(-1, nil)), "A5: negative price rejected")
	require.Equal(t, sqlstateCheckViolation, pgErrCode(negative(1000, &zero)), "A5: zero duration rejected")

	// is_offered=false is how a tier is excluded, not a NULL price.
	_, err = pool.Exec(ctx, `
		INSERT INTO service_variant_tier_prices
			(tenant_id, location_id, service_variant_id, tier_id, price_paise, is_offered)
		VALUES ($1, $2, $3, $4, 0, false)`, f.tenantID, f.locID, f.variantID, tier2)
	require.NoError(t, err, "A5: a not-offered row is a real row with a price")
}

// TestMigration004_TierDeleteBehaviour is A6. Only tier_prices cascade; the
// other three FKs have no ON DELETE clause and therefore block. Asserted as
// observed behaviour, reported as a finding — the DDL is not changed.
func TestMigration004_TierDeleteBehaviour(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	f := seedTierFixture(t, pool)

	tierID, err := insertTier(pool, f, "Senior", 20, true, true)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO service_variant_tier_prices
			(tenant_id, location_id, service_variant_id, tier_id, price_paise)
		VALUES ($1, $2, $3, $4, 45000)`, f.tenantID, f.locID, f.variantID, tierID)
	require.NoError(t, err)

	// Referenced by a barber: NO ACTION, so the delete must be refused.
	_, err = pool.Exec(ctx, `UPDATE staff_members SET tier_id = $1 WHERE id = $2`, tierID, f.staffID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM staff_tiers WHERE id = $1`, tierID)
	require.Equal(t, sqlstateFKViolation, pgErrCode(err),
		"A6: staff_members.tier_id has no ON DELETE, so the tier delete is refused")

	// Detach the barber; now only the cascading reference remains.
	_, err = pool.Exec(ctx, `UPDATE staff_members SET tier_id = NULL WHERE id = $1`, f.staffID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM staff_tiers WHERE id = $1`, tierID)
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM service_variant_tier_prices WHERE tier_id = $1`, tierID).Scan(&n))
	require.Zero(t, n, "A6: tier_prices cascade with the tier")
}

// expectedDispatchIndexDef is 001:837-839 as pg_indexes normalises it. T1
// deliberately leaves this index alone; asserting the exact string is how we
// notice if a later unit changes it by accident.
const expectedDispatchIndexDef = "CREATE INDEX idx_queue_dispatch ON public.queue_entries " +
	"USING btree (queue_session_id, priority_group, sort_key) " +
	"WHERE ((is_dispatchable = true) AND ((state)::text = 'waiting'::text))"

// TestMigration004_DispatchIndexUnchanged is A7. T1 does not touch
// idx_queue_dispatch: measured at 50k rows, the definition from 001 already
// serves T3's tier predicate with an ordered Index Scan and no Sort, while
// moving required_tier_id into the key turns it into a Seq Scan + Sort. The
// plan below is the evidence that T3 needs no index work — quote it, don't
// assume it.
func TestMigration004_DispatchIndexUnchanged(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	f := seedTierFixture(t, pool)

	var indexDef string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_queue_dispatch'`).Scan(&indexDef))
	require.Equal(t, expectedDispatchIndexDef, indexDef, "A7: idx_queue_dispatch must be untouched by 004")
	require.NotContains(t, indexDef, "required_tier_id", "A7: the tier column must not be in the dispatch key")
	t.Logf("A7 index definition: %s", indexDef)

	tierID, err := insertTier(pool, f, "Senior", 20, true, true)
	require.NoError(t, err)

	// A dedicated connection: seeding 50k rows exceeds the pool's 5s
	// statement_timeout, which is correct for request traffic and wrong here.
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET statement_timeout = '120s'`)
	require.NoError(t, err)

	// 50k dispatchable entries. Below roughly this volume the planner seq-scans
	// whatever indexes exist, so a smaller table proves nothing either way.
	_, err = conn.Exec(ctx, `
		WITH v AS (
			INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
			SELECT $1, $2, 'walk_in', 30 FROM generate_series(1, 50000)
			RETURNING id
		), numbered AS (
			SELECT id, row_number() OVER () AS n FROM v
		)
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, is_dispatchable,
			 priority_group, sort_key, required_tier_id)
		SELECT id, $3, n, 'waiting', true, 100, n,
		       CASE WHEN n % 2 = 0 THEN $4::uuid ELSE NULL END
		FROM numbered`, f.tenantID, f.locID, f.sessionID, tierID)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `ANALYZE queue_entries`)
	require.NoError(t, err)

	// Exactly the shape T3 will emit.
	rows, err := conn.Query(ctx, `
		EXPLAIN SELECT id FROM queue_entries
		WHERE queue_session_id = $1
		  AND is_dispatchable = true AND state = 'waiting'
		  AND (required_tier_id IS NULL OR required_tier_id = $2)
		ORDER BY priority_group, sort_key
		LIMIT 1`, f.sessionID, tierID)
	require.NoError(t, err)

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	rows.Close()
	require.NoError(t, rows.Err())

	t.Logf("A7 Call-Next plan over 50k rows:\n%s", plan.String())
	require.Contains(t, plan.String(), "Index Scan using idx_queue_dispatch",
		"A7: the dispatch query must walk the index in order")
	require.NotContains(t, plan.String(), "Sort", "A7: an ordered index walk must need no sort")
	require.NotContains(t, plan.String(), "Seq Scan", "A7: a sequential scan here would sink Call Next under load")
	require.Contains(t, plan.String(), "required_tier_id",
		"A7: the tier predicate must be applied as a per-row filter")
}

// TestMigration004_VisitServicesSupersede is A8 and the A10 back-compat check.
func TestMigration004_VisitServicesSupersede(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	f := seedTierFixture(t, pool)

	var visitID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 30) RETURNING id`, f.tenantID, f.locID).Scan(&visitID))

	// A10: an insert naming no tier columns must still work — every new column
	// is nullable or defaulted.
	var originalID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visit_services
			(visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot,
			 category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot)
		VALUES ($1, $2, 'Mid Fade', 'Fade', 'Hair', 30, 25000) RETURNING id`,
		visitID, f.variantID).Scan(&originalID))

	// A10: same for queue_entries.
	var entryVisit uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 30) RETURNING id`, f.tenantID, f.locID).Scan(&entryVisit))
	_, err := pool.Exec(ctx, `
		INSERT INTO queue_entries (visit_id, queue_session_id, token_number)
		VALUES ($1, $2, 1)`, entryVisit, f.sessionID)
	require.NoError(t, err, "A10: pre-tier inserts must still succeed")

	// A8: upgrade is append-plus-tombstone, never a mutation (Law 10).
	tierID, err := insertTier(pool, f, "Expert", 30, true, true)
	require.NoError(t, err)
	var replacementID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visit_services
			(visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot,
			 category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot,
			 tier_id, tier_name_snapshot, supersedes_id)
		VALUES ($1, $2, 'Mid Fade', 'Fade', 'Hair', 30, 45000, $3, 'Expert', $4) RETURNING id`,
		visitID, f.variantID, tierID, originalID).Scan(&replacementID))
	_, err = pool.Exec(ctx, `UPDATE visit_services SET superseded_at = NOW() WHERE id = $1`, originalID)
	require.NoError(t, err)

	var total int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM visit_services WHERE visit_id = $1`, visitID).Scan(&total))
	require.Equal(t, 2, total, "A8: the tombstone and its replacement both persist")

	var liveID uuid.UUID
	var livePrice int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id, price_paise_snapshot FROM visit_services
		WHERE visit_id = $1 AND superseded_at IS NULL`, visitID).Scan(&liveID, &livePrice))
	require.Equal(t, replacementID, liveID, "A8: only the replacement is live")
	require.Equal(t, 45000, livePrice)

	var supersedes uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT supersedes_id FROM visit_services WHERE id = $1`, replacementID).Scan(&supersedes))
	require.Equal(t, originalID, supersedes, "A8: the replacement points back at what it replaced")
}
