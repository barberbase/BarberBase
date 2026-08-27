package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ledgerDDL creates the applied-migration ledger. Runner-embedded rather than a
// 000_*.sql file: a file in migrations/ would itself be discovered as a
// migration and would need the ledger it is trying to create.
const ledgerDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT PRIMARY KEY,
    checksum    TEXT NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_ms INT NOT NULL
);`

// baselineVersion is the version adopted by ADOPT_BASELINE, and baselineSentinel
// the table whose presence proves it was applied.
const (
	baselineVersion  = "001"
	baselineSentinel = "queue_sessions"
)

var (
	migrationFileRe = regexp.MustCompile(`^(\d+)_.*\.sql$`)
	createTableRe   = regexp.MustCompile(`(?im)^CREATE TABLE (?:IF NOT EXISTS )?([a-z_]+)`)
)

type migration struct {
	version  string
	path     string
	checksum string // lowercase hex sha256 of the raw file bytes, pre-strip
	raw      []byte
	body     string // what actually executes: raw minus any BEGIN;/COMMIT; wrapper
	stripped bool
}

// Migrate brings the database up to date with the migrations directory
// containing migrationFilePath, tracking applied state in schema_migrations.
//
// Each pending migration runs in its own transaction with its ledger row
// written in that same transaction, so a partial apply is impossible. Any
// failure halts immediately; later migrations do not run.
//
// Set ADOPT_BASELINE=1 (exactly "1") to adopt an existing 001 baseline: the 001
// ledger row is recorded without executing 001. Never implicit.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrationFilePath string) error {
	// ponytail: ADOPT_BASELINE is read here, not passed in, so cmd/server/main.go
	// stays untouched. Exact "1" only — "true"/"yes"/a typo must fail closed.
	adopt := os.Getenv("ADOPT_BASELINE") == "1"
	log.Printf("Migrate: ADOPT_BASELINE resolved to %t (raw=%q)", adopt, os.Getenv("ADOPT_BASELINE"))

	if _, err := pool.Exec(ctx, ledgerDDL); err != nil {
		return fmt.Errorf("failed to create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, pool)
	if err != nil {
		return err
	}

	dir := filepath.Dir(migrationFilePath)
	migrations, err := discover(dir)
	if err != nil {
		return err
	}

	if adopt && len(applied) > 0 {
		log.Printf("Migrate: WARNING ADOPT_BASELINE=1 but schema_migrations already has %d row(s); "+
			"nothing to adopt. Unset it.", len(applied))
	}

	adopted := ""
	if len(applied) == 0 {
		baselinePresent, err := tableExists(ctx, pool, baselineSentinel)
		if err != nil {
			return err
		}
		if baselinePresent {
			if !adopt {
				return fmt.Errorf("table %q exists but schema_migrations is empty: this database "+
					"predates the ledger. Re-run with ADOPT_BASELINE=1 to record %s without "+
					"executing it. Nothing was changed", baselineSentinel, baselineVersion)
			}
			if err := adoptBaseline(ctx, pool, migrations); err != nil {
				return err
			}
			applied[baselineVersion] = migrationChecksum(migrations, baselineVersion)
			adopted = baselineVersion
		}
	}

	for _, m := range migrations {
		if m.version == adopted {
			continue // already logged as adopted
		}
		prev, seen := applied[m.version]
		if seen {
			if prev != m.checksum {
				return fmt.Errorf("checksum mismatch for migration %s (%s): ledger has %s, file is %s. "+
					"An applied migration was edited. Halting; nothing was changed",
					m.version, m.path, prev, m.checksum)
			}
			log.Printf("Migrate: version=%s status=skipped", m.version)
			continue
		}
		if err := apply(ctx, pool, m); err != nil {
			return err
		}
	}

	log.Println("Migrate: up to date.")
	return nil
}

func loadApplied(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]string{}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("failed to scan schema_migrations row: %w", err)
		}
		applied[version] = checksum
	}
	return applied, rows.Err()
}

// discover reads every .sql file in dir, in version order. Every .sql file must
// be a well-formed migration and every version must be unique — an unreadable
// name or a duplicate version is a hard error here, before anything executes,
// never a silent skip or a mid-run ledger PK collision.
func discover(dir string) ([]migration, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, fmt.Errorf("failed to list migrations in %s: %w", dir, err)
	}

	seen := map[string]string{}
	migrations := make([]migration, 0, len(paths))
	for _, p := range paths {
		match := migrationFileRe.FindStringSubmatch(filepath.Base(p))
		if match == nil {
			return nil, fmt.Errorf("migration file %s does not match NNN_name.sql; "+
				"refusing to guess whether it should run", p)
		}
		version := match[1]
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %s: %s and %s", version, other, p)
		}
		seen[version] = p

		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", p, err)
		}
		sum := sha256.Sum256(raw)

		body, stripped, err := stripTxWrapper(string(raw))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", p, err)
		}

		migrations = append(migrations, migration{
			version:  version,
			path:     p,
			checksum: hex.EncodeToString(sum[:]),
			raw:      raw,
			body:     body,
			stripped: stripped,
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// stripTxWrapper removes a self-supplied BEGIN;/COMMIT; wrapper so the runner
// owns the transaction (001 has one and is frozen). Only the first and last
// meaningful lines are examined — never the middle — so a $$ ... BEGIN ... END; $$
// plpgsql body is a non-issue without needing a SQL lexer. A half-wrapper is an
// error rather than a guess.
func stripTxWrapper(content string) (string, bool, error) {
	lines := strings.Split(content, "\n")

	meaningful := func(s string) bool {
		t := strings.TrimSpace(s)
		return t != "" && !strings.HasPrefix(t, "--")
	}

	first, last := -1, -1
	for i, l := range lines {
		if meaningful(l) {
			first = i
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if meaningful(lines[i]) {
			last = i
			break
		}
	}
	if first == -1 || first == last {
		return content, false, nil
	}

	hasBegin := strings.EqualFold(strings.TrimSpace(lines[first]), "BEGIN;")
	hasCommit := strings.EqualFold(strings.TrimSpace(lines[last]), "COMMIT;")
	switch {
	case hasBegin && hasCommit:
		out := make([]string, 0, len(lines)-2)
		out = append(out, lines[:first]...)
		out = append(out, lines[first+1:last]...)
		out = append(out, lines[last+1:]...)
		return strings.Join(out, "\n"), true, nil
	case hasBegin:
		return "", false, fmt.Errorf("starts with BEGIN; but does not end with COMMIT;. " +
			"The runner owns the transaction; remove the transaction control")
	case hasCommit:
		return "", false, fmt.Errorf("ends with COMMIT; without a matching leading BEGIN;. " +
			"The runner owns the transaction; remove the transaction control")
	}
	return content, false, nil
}

// apply runs one migration and records it atomically. Nothing but the four
// statements happens between Begin and Commit — bytes are already read and
// hashed — because idle_in_transaction_session_timeout is 10s.
func apply(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	start := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migration %s: failed to begin transaction: %w", m.version, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, m.body); err != nil {
		return fmt.Errorf("migration %s (%s) failed and was rolled back; later migrations "+
			"were not run: %w", m.version, m.path, err)
	}

	// Law-7-shaped: the ledger row is written in the same transaction as the
	// schema change it records. A partial apply is impossible.
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, checksum, duration_ms) VALUES ($1, $2, $3)`,
		m.version, m.checksum, int(time.Since(start).Milliseconds()),
	); err != nil {
		return fmt.Errorf("migration %s: failed to record ledger row: %w", m.version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migration %s: failed to commit: %w", m.version, err)
	}

	log.Printf("Migrate: version=%s duration_ms=%d status=applied wrapper_stripped=%t",
		m.version, time.Since(start).Milliseconds(), m.stripped)
	return nil
}

// adoptBaseline records 001 without executing it, for databases that predate the
// ledger. It refuses a partially-migrated database: every table 001 creates must
// already exist, or the ledger row would be a lie.
func adoptBaseline(ctx context.Context, pool *pgxpool.Pool, migrations []migration) error {
	var base *migration
	for i := range migrations {
		if migrations[i].version == baselineVersion {
			base = &migrations[i]
			break
		}
	}
	if base == nil {
		return fmt.Errorf("ADOPT_BASELINE=1 but no migration with version %s was found", baselineVersion)
	}

	want := []string{}
	for _, m := range createTableRe.FindAllStringSubmatch(string(base.raw), -1) {
		want = append(want, strings.ToLower(m[1]))
	}
	if len(want) == 0 {
		return fmt.Errorf("ADOPT_BASELINE=1 but no CREATE TABLE found in %s", base.path)
	}

	var missing []string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(t), '{}')
		FROM unnest($1::text[]) AS t
		WHERE to_regclass('public.' || quote_ident(t)) IS NULL`, want,
	).Scan(&missing); err != nil {
		return fmt.Errorf("failed to verify baseline completeness: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("refusing to adopt baseline %s: partially migrated database, "+
			"%d of %d tables missing (%s). Nothing was changed",
			baselineVersion, len(missing), len(want), strings.Join(missing, ", "))
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO schema_migrations (version, checksum, duration_ms) VALUES ($1, $2, 0)`,
		base.version, base.checksum,
	); err != nil {
		return fmt.Errorf("failed to record adopted baseline %s: %w", base.version, err)
	}

	log.Printf("Migrate: version=%s duration_ms=0 status=adopted (not executed) wrapper_stripped=%t",
		base.version, base.stripped)
	return nil
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.' || quote_ident($1)) IS NOT NULL`, name,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check if table %q exists: %w", name, err)
	}
	return exists, nil
}

func migrationChecksum(migrations []migration, version string) string {
	for _, m := range migrations {
		if m.version == version {
			return m.checksum
		}
	}
	return ""
}
