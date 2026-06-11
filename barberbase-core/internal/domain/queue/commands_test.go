package queue

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupQueueTest(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}

	pool, err := repository.InitPool(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	err = repository.Migrate(ctx, pool, "../../../migrations/001_complete_schema.sql")
	require.NoError(t, err)

	// Clean tables
	_, err = pool.Exec(ctx, "TRUNCATE tenants, locations, staff_members, queue_sessions, queue_entries, customers, visits, outbox_events, webhook_events, staff_otps, visit_charges CASCADE")
	require.NoError(t, err)

	tenantID := uuid.New()
	locationID := uuid.New()
	barberID := uuid.New()
	variantID := uuid.New()

	// Seed tenant, location, staff, variants
	_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'Test Tenant', 'test-tenant', '+919999999999')", tenantID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug, queue_routing_mode, timezone) VALUES ($1, $2, 'Test Location', 'test-location', 'pooled', 'Asia/Kolkata')", locationID, tenantID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, status, is_active) VALUES ($1, $2, $3, 'Test Staff', '+919000000001', 'barber', 'idle', true)", barberID, tenantID, locationID)
	require.NoError(t, err)

	// Seed service variant catalog items
	catID := uuid.New()
	groupID := uuid.New()
	_, err = pool.Exec(ctx, "INSERT INTO service_categories (id, tenant_id, location_id, name, gender) VALUES ($1, $2, $3, 'Haircut', 'unisex')", catID, tenantID, locationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO service_groups (id, tenant_id, location_id, category_id, name) VALUES ($1, $2, $3, $4, 'Haircuts')", groupID, tenantID, locationID, catID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO service_variants (id, tenant_id, location_id, group_id, name, duration_minutes, price_paise) VALUES ($1, $2, $3, $4, 'Standard Haircut', 30, 50000)", variantID, tenantID, locationID, groupID)
	require.NoError(t, err)

	return pool, tenantID, locationID, barberID, variantID
}

func seedTestQueueSession(t *testing.T, pool *pgxpool.Pool, tenantID, locationID uuid.UUID) uuid.UUID {
	ctx := context.Background()
	sessionID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO queue_sessions (id, tenant_id, location_id, business_date, status, queue_version, last_token_number)
		VALUES ($1, $2, $3, CURRENT_DATE, 'active', 0, 0)`, sessionID, tenantID, locationID)
	require.NoError(t, err)
	return sessionID
}

func seedTestQueueEntry(t *testing.T, pool *pgxpool.Pool, tenantID, locationID, sessionID uuid.UUID, presence string) (uuid.UUID, uuid.UUID) {
	ctx := context.Background()
	visitID := uuid.New()
	entryID := uuid.New()
	cID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, $3, 'Customer')`, cID, tenantID, "+91"+uuid.New().String()[:10])
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 30)`, visitID, tenantID, locationID, cID)
	require.NoError(t, err)

	var token int
	err = pool.QueryRow(ctx, `
		UPDATE queue_sessions
		SET last_token_number = last_token_number + 1
		WHERE id = $1
		RETURNING last_token_number`, sessionID).Scan(&token)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO queue_entries (id, visit_id, queue_session_id, customer_id, token_number, state, presence_state, is_dispatchable, priority_group, sort_key, remote_joined_at)
		VALUES ($1, $2, $3, $4, $5, 'waiting', $6, true, 100, EXTRACT(EPOCH FROM NOW())::BIGINT, NOW())`,
		entryID, visitID, sessionID, cID, token, presence)
	require.NoError(t, err)

	return entryID, visitID
}

func TestCompleteVisitAndCheckout_Rollback(t *testing.T) {
	pool, tenantID, locationID, barberID, variantID := setupQueueTest(t)
	ctx := context.Background()

	// Ensure staff has push_enabled = true
	_, err := pool.Exec(ctx, "UPDATE staff_members SET push_enabled = true WHERE id = $1", barberID)
	require.NoError(t, err)

	sessionID := seedTestQueueSession(t, pool, tenantID, locationID)
	entryID, visitID := seedTestQueueEntry(t, pool, tenantID, locationID, sessionID, "arrived")

	// Update queue entry state to in_progress (required for checkout)
	_, err = pool.Exec(ctx, "UPDATE queue_entries SET state = 'in_progress' WHERE id = $1", entryID)
	require.NoError(t, err)

	// Seed visit service
	_, err = pool.Exec(ctx, `
		INSERT INTO visit_services (id, visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot, category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot, sort_order)
		VALUES ($1, $2, $3, 'Standard Haircut', 'Haircuts', 'Haircut', 30, 50000, 0)`,
		uuid.New(), visitID, variantID)
	require.NoError(t, err)

	// Setup params with invalid payment total to trigger rollback
	params := CheckoutParams{
		EntryID:             entryID,
		TenantID:            tenantID,
		LocationID:          locationID,
		CallerStaffID:       barberID,
		BusinessDate:        time.Now(),
		DiscountAmountPaise: 0,
		Payments: []CheckoutPaymentLine{
			{
				AmountPaise: 40000, // should be 50000
				Method:      "cash",
			},
		},
	}

	_, err = CompleteVisitAndCheckout(ctx, pool, params)
	require.Error(t, err)

	// Verify that NO outbox event of type 'web_push.send' was written
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE type = 'web_push.send'").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestCompleteVisitAndCheckout_NoPushEnabledStaff(t *testing.T) {
	pool, tenantID, locationID, barberID, variantID := setupQueueTest(t)
	ctx := context.Background()

	// Ensure all staff has push_enabled = false
	_, err := pool.Exec(ctx, "UPDATE staff_members SET push_enabled = false WHERE location_id = $1", locationID)
	require.NoError(t, err)

	sessionID := seedTestQueueSession(t, pool, tenantID, locationID)
	entryID, visitID := seedTestQueueEntry(t, pool, tenantID, locationID, sessionID, "arrived")

	// Update queue entry state to in_progress (required for checkout)
	_, err = pool.Exec(ctx, "UPDATE queue_entries SET state = 'in_progress', assigned_barber_id = $2 WHERE id = $1", entryID, barberID)
	require.NoError(t, err)

	// Seed visit service
	_, err = pool.Exec(ctx, `
		INSERT INTO visit_services (id, visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot, category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot, sort_order)
		VALUES ($1, $2, $3, 'Standard Haircut', 'Haircuts', 'Haircut', 30, 50000, 0)`,
		uuid.New(), visitID, variantID)
	require.NoError(t, err)

	// Setup valid params
	params := CheckoutParams{
		EntryID:             entryID,
		TenantID:            tenantID,
		LocationID:          locationID,
		CallerStaffID:       barberID,
		BusinessDate:        time.Now(),
		DiscountAmountPaise: 0,
		Payments: []CheckoutPaymentLine{
			{
				AmountPaise: 50000,
				Method:      "cash",
			},
		},
	}

	_, err = CompleteVisitAndCheckout(ctx, pool, params)
	require.NoError(t, err)

	// Verify that NO outbox event of type 'web_push.send' was written
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events WHERE type = 'web_push.send'").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestCompleteVisitAndCheckout_WithPushEnabledStaff(t *testing.T) {
	pool, tenantID, locationID, barberID, variantID := setupQueueTest(t)
	ctx := context.Background()

	// Ensure one staff has push_enabled = true
	_, err := pool.Exec(ctx, "UPDATE staff_members SET push_enabled = true WHERE id = $1", barberID)
	require.NoError(t, err)

	sessionID := seedTestQueueSession(t, pool, tenantID, locationID)
	entryID, visitID := seedTestQueueEntry(t, pool, tenantID, locationID, sessionID, "arrived")

	// Update queue entry state to in_progress (required for checkout)
	_, err = pool.Exec(ctx, "UPDATE queue_entries SET state = 'in_progress', assigned_barber_id = $2 WHERE id = $1", entryID, barberID)
	require.NoError(t, err)

	// Seed visit service
	_, err = pool.Exec(ctx, `
		INSERT INTO visit_services (id, visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot, category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot, sort_order)
		VALUES ($1, $2, $3, 'Standard Haircut', 'Haircuts', 'Haircut', 30, 50000, 0)`,
		uuid.New(), visitID, variantID)
	require.NoError(t, err)

	// Setup valid params
	params := CheckoutParams{
		EntryID:             entryID,
		TenantID:            tenantID,
		LocationID:          locationID,
		CallerStaffID:       barberID,
		BusinessDate:        time.Now(),
		DiscountAmountPaise: 0,
		Payments: []CheckoutPaymentLine{
			{
				AmountPaise: 50000,
				Method:      "cash",
			},
		},
	}

	_, err = CompleteVisitAndCheckout(ctx, pool, params)
	require.NoError(t, err)

	// Verify that exactly one outbox event of type 'web_push.send' was written
	var count int
	var dbTenantID uuid.UUID
	var payloadBytes []byte
	err = pool.QueryRow(ctx, "SELECT COUNT(*), tenant_id, payload FROM outbox_events WHERE type = 'web_push.send' GROUP BY tenant_id, payload").Scan(&count, &dbTenantID, &payloadBytes)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, tenantID, dbTenantID)

	// Verify payload contains correct location_id and tenant_id
	var payload struct {
		LocationID string `json:"location_id"`
		TenantID   string `json:"tenant_id"`
	}
	err = json.Unmarshal(payloadBytes, &payload)
	require.NoError(t, err)
	require.Equal(t, locationID.String(), payload.LocationID)
	require.Equal(t, tenantID.String(), payload.TenantID)
}
