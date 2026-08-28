package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// B9 regression tests. Each of these fails against the pre-fix code with a
// "cannot scan NULL into *T" error, which is the entire signature of this bug
// class: it is invisible until a NULL actually appears, so it surfaces in
// production months later and silently.

type nullScanFixture struct {
	tenantID, locationID, sessionID, visitID, customerID, staffID uuid.UUID
}

type pgxPoolAlias = pgxpool.Pool

// b9Pool reuses migration_test.go's scratch-database helper so these tests never
// touch the shared dev database.
func b9Pool(t *testing.T) *pgxpool.Pool { return migratedPool(t) }

// seedForNullScan builds tenant → location → session → visit, plus a SHADOW
// customer, which is the reachable source of a NULL phone_number:
// repository/customer.go:122 inserts `(tenant_id, is_shadow_profile)` with no
// phone at all.
func seedForNullScan(t *testing.T, pool *pgxPoolAlias) nullScanFixture {
	t.Helper()
	ctx := context.Background()
	var f nullScanFixture
	sfx := uuid.NewString()[:8]

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('B9', $1, '+919999933333') RETURNING id`, "b9-"+sfx).Scan(&f.tenantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, $2, 'B9 Loc', 'Asia/Kolkata') RETURNING id`,
		f.tenantID, "b9loc-"+sfx).Scan(&f.locationID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active') RETURNING id`,
		f.tenantID, f.locationID).Scan(&f.sessionID))
	// A shadow profile: no phone_number column supplied at all.
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO customers (tenant_id, is_shadow_profile) VALUES ($1, true) RETURNING id`,
		f.tenantID).Scan(&f.customerID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, customer_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, $3, 'walk_in', 'active', 30) RETURNING id`,
		f.tenantID, f.locationID, f.customerID).Scan(&f.visitID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role, status)
		VALUES ($1, $2, 'B9 Barber', $3, 'barber', 'idle') RETURNING id`,
		f.tenantID, f.locationID, "+9193"+sfx+"0").Scan(&f.staffID))
	return f
}

// seedEntryNoJoinedAt inserts a queue entry with remote_joined_at left NULL —
// the shape every production insert avoids and no test previously produced.
func seedEntryNoJoinedAt(t *testing.T, pool *pgxPoolAlias, f nullScanFixture, presence string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO queue_entries
			(visit_id, queue_session_id, customer_id, token_number, state, presence_state,
			 is_dispatchable, priority_group, sort_key)
		VALUES ($1, $2, $3, 1, 'waiting', $4, true, 100, 1) RETURNING id`,
		f.visitID, f.sessionID, f.customerID, presence).Scan(&id))
	return id
}

// B9 site 1 — GetQueueEntryByCustomer. QueueEntryRow is internal, so the field
// became a pointer rather than being COALESCEd.
func TestB9_GetQueueEntryByCustomer_NullRemoteJoinedAt(t *testing.T) {
	pool := b9Pool(t)
	f := seedForNullScan(t, pool)
	seedEntryNoJoinedAt(t, pool, f, "remote")

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	row, err := GetQueueEntryByCustomer(ctx, tx, f.sessionID, f.customerID)
	require.NoError(t, err, "B9: a NULL remote_joined_at must not fail the scan")
	require.NotNil(t, row)
	require.Nil(t, row.RemoteJoinedAt, "nil means 'never recorded', which is what NULL means")
}

// B9 site 2 — GetEntryStaffView. joined_at is a REQUIRED non-null field on
// QueueEntryStaff and openapi.yaml is frozen, so this one COALESCEs to
// visits.created_at rather than becoming a pointer. That is not a dodge:
// visits.created_at is NOT NULL and is the same instant semantically — when the
// visit was created is when the customer joined.
func TestB9_GetEntryStaffView_NullRemoteJoinedAt(t *testing.T) {
	pool := b9Pool(t)
	f := seedForNullScan(t, pool)
	entryID := seedEntryNoJoinedAt(t, pool, f, "arrived")

	row, err := GetEntryStaffView(context.Background(), pool, entryID)
	require.NoError(t, err, "B9: this is the instance M2's A12 tripped over")
	require.NotNil(t, row)
	require.False(t, row.JoinedAt.IsZero(),
		"B9: COALESCE must yield the visit's creation time, never a zero time")

	var visitCreated bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT $1::timestamptz = created_at FROM visits WHERE id = $2`,
		row.JoinedAt, f.visitID).Scan(&visitCreated))
	require.True(t, visitCreated, "B9: and specifically the visit's created_at")
}

// B9 site 4 — CallNextTx's notification read selects c.phone_number for the
// bb_you_are_next template. A shadow profile has none.
//
// Pre-fix the scan error was swallowed (`if notifErr == nil && ...`), so the
// dispatch still succeeded and the customer simply never got a message —
// silent, exactly like B7.
func TestB9_CallNextTx_ShadowCustomerHasNoPhone(t *testing.T) {
	pool := b9Pool(t)
	f := seedForNullScan(t, pool)
	entryID := seedEntryNoJoinedAt(t, pool, f, "arrived")

	ctx := context.Background()
	// session_channel must be whatsapp to reach the notification block at all.
	_, err := pool.Exec(ctx,
		`UPDATE queue_entries SET session_channel='whatsapp' WHERE id=$1`, entryID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE visits SET magic_link_token_hash='h', magic_link_expires_at=NOW()+INTERVAL '23 hours'
		 WHERE id=$1`, f.visitID)
	require.NoError(t, err)

	var phone, mlToken, locName, waMode, bizPhone string
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(c.phone_number, ''),
		       COALESCE(v.magic_link_token_hash, ''),
		       l.name, l.whatsapp_mode, COALESCE(l.business_whatsapp_number, '')
		FROM visits v
		JOIN customers c ON c.id = v.customer_id
		JOIN locations l ON l.id = $2
		WHERE v.id = $1 AND c.id = $3`,
		f.visitID, f.locationID, f.customerID,
	).Scan(&phone, &mlToken, &locName, &waMode, &bizPhone)
	require.NoError(t, err, "B9: a shadow customer's NULL phone must not fail this read")
	require.Empty(t, phone, "B9: it reads as empty, and the caller's phone != \"\" guard then skips the send")
	require.NotEmpty(t, mlToken)
}
