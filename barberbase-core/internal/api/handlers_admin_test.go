package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupAdminTestServer(t *testing.T) (*Server, *pgxpool.Pool, uuid.UUID, uuid.UUID, uuid.UUID, string) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	pool, err := repository.InitPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to initialize pool: %v", err)
	}

	// Clean up everything with CASCADE to prevent foreign key issues
	_, err = pool.Exec(ctx, "TRUNCATE tenants, locations, staff_members, service_categories, service_groups, service_variants, visits, visit_services, webhook_events, staff_otps CASCADE")
	if err != nil {
		t.Fatalf("Failed to truncate tables: %v", err)
	}
	pool.Close() // Close this pool so setupTestServer can initialize its own pool safely.

	return setupTestServer(t)
}

func TestAdminServices_RoleGate(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupAdminTestServer(t)
	defer pool.Close()

	// 1. GET with barber role -> 403
	{
		req := newStaffRequestWithRole(http.MethodGet, fmt.Sprintf("/admin/locations/%s/services", locationID), tenantID, locationID, staffID, "barber")
		rec := httptest.NewRecorder()
		s.GetAdminLocationsLocationIdServices(rec, req, locationID)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected GET 403 Forbidden for barber role, got %d", rec.Code)
		}
	}

	// 2. POST with barber role -> 403
	{
		body := map[string]interface{}{
			"category_name":    "Hair",
			"group_name":       "Fade",
			"variant_name":     "Low Fade",
			"duration_minutes": 30,
			"price_paise":      1500,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/locations/%s/services", locationID), bytes.NewReader(jsonBody))
		// Inject auth context manually since NewRequest overrides context
		ctx := req.Context()
		ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
		ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
		ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
		ctx = context.WithValue(ctx, auth.CtxRole, "barber")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		s.CreateServiceVariant(rec, req, locationID)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected POST 403 Forbidden for barber role, got %d", rec.Code)
		}
	}

	// 3. PATCH with barber role -> 403
	{
		variantID := uuid.New()
		body := map[string]interface{}{
			"price_paise": 2000,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/admin/locations/%s/services/%s", locationID, variantID), bytes.NewReader(jsonBody))
		ctx := req.Context()
		ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
		ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
		ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
		ctx = context.WithValue(ctx, auth.CtxRole, "barber")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		s.UpdateServiceVariant(rec, req, locationID, variantID)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Expected PATCH 403 Forbidden for barber role, got %d", rec.Code)
		}
	}
}

func TestAdminServices_LocationTenantIsolation(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupAdminTestServer(t)
	defer pool.Close()

	// Create another tenant's location
	otherTenantID := uuid.New()
	otherLocationID := uuid.New()
	ctx := context.Background()
	_, err := pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, $2, $3, $4)", otherTenantID, "Other Tenant", "other-tenant", "+918888888888")
	if err != nil {
		t.Fatalf("Failed to insert other tenant: %v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $4)", otherLocationID, otherTenantID, "Other Location", "other-location")
	if err != nil {
		t.Fatalf("Failed to insert other location: %v", err)
	}

	// Request with manager role, but accessing other location ID (which belongs to other tenant) -> should return 404
	{
		req := newStaffRequestWithRole(http.MethodGet, fmt.Sprintf("/admin/locations/%s/services", otherLocationID), tenantID, locationID, staffID, "manager")
		rec := httptest.NewRecorder()
		s.GetAdminLocationsLocationIdServices(rec, req, otherLocationID)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("Expected 404 Not Found for location of different tenant, got %d", rec.Code)
		}
	}
}

func TestAdminServices_CreateVariant_Success_And_Conflict(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupAdminTestServer(t)
	defer pool.Close()

	// 1. Successful creation
	body := map[string]interface{}{
		"category_name":        "Hair",
		"category_gender":      "men",
		"group_name":           "Fade",
		"variant_name":         "Mid Fade",
		"duration_minutes":     30,
		"price_paise":          1250,
		"allow_walk_in":        true,
		"allow_appointment":    true,
		"requires_appointment": false,
		"is_popular":           true,
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/locations/%s/services", locationID), bytes.NewReader(jsonBody))
	ctx := req.Context()
	ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
	ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
	ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
	ctx = context.WithValue(ctx, auth.CtxRole, "owner")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.CreateServiceVariant(rec, req, locationID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	var resp ServiceVariant
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if resp.Name != "Mid Fade" {
		t.Errorf("Expected name 'Mid Fade', got %s", resp.Name)
	}
	if resp.PricePaise != 1250 {
		t.Errorf("Expected price_paise 1250, got %d", resp.PricePaise)
	}

	// 2. Duplicate variant name in same group -> should fail with 409
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/locations/%s/services", locationID), bytes.NewReader(jsonBody))
	ctx2 := req2.Context()
	ctx2 = context.WithValue(ctx2, auth.CtxTenantID, tenantID.String())
	ctx2 = context.WithValue(ctx2, auth.CtxLocationID, locationID.String())
	ctx2 = context.WithValue(ctx2, auth.CtxStaffMemberID, staffID.String())
	ctx2 = context.WithValue(ctx2, auth.CtxRole, "owner")
	req2 = req2.WithContext(ctx2)

	rec2 := httptest.NewRecorder()
	s.CreateServiceVariant(rec2, req2, locationID)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("Expected 409 Conflict, got %d. Response: %s", rec2.Code, rec2.Body.String())
	}
}

func TestAdminServices_UpdateVariant_PATCH_Validation_And_Immutability(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupAdminTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	// Create a variant first using DB query directly
	catID := uuid.New()
	grpID := uuid.New()
	varID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO service_categories (id, tenant_id, location_id, name, gender)
		VALUES ($1, $2, $3, 'Skin', 'unisex')`, catID, tenantID, locationID)
	if err != nil {
		t.Fatalf("Failed to insert category: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO service_groups (id, tenant_id, location_id, category_id, name)
		VALUES ($1, $2, $3, $4, 'Facial')`, grpID, tenantID, locationID, catID)
	if err != nil {
		t.Fatalf("Failed to insert group: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO service_variants (id, tenant_id, location_id, group_id, name, duration_minutes, price_paise, allow_walk_in, allow_appointment, requires_appointment)
		VALUES ($1, $2, $3, $4, 'Gold Facial', 45, 1000, true, true, false)`, varID, tenantID, locationID, grpID)
	if err != nil {
		t.Fatalf("Failed to insert variant: %v", err)
	}

	// Create a visit and record its service snapshot in visit_services
	customerID := uuid.New()
	visitID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO customers (id, tenant_id, phone_number, name)
		VALUES ($1, $2, '+919999999911', 'Customer C')`, customerID, tenantID)
	if err != nil {
		t.Fatalf("Failed to insert customer: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO visits (id, tenant_id, location_id, customer_id, entry_type, status, party_size, total_duration_minutes)
		VALUES ($1, $2, $3, $4, 'walk_in', 'active', 1, 45)`, visitID, tenantID, locationID, customerID)
	if err != nil {
		t.Fatalf("Failed to insert visit: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO visit_services (visit_id, service_variant_id, variant_name_snapshot, group_name_snapshot, category_name_snapshot, duration_minutes_snapshot, price_paise_snapshot)
		VALUES ($1, $2, 'Gold Facial', 'Facial', 'Skin', 45, 1000)`, visitID, varID)
	if err != nil {
		t.Fatalf("Failed to insert visit_services snapshot: %v", err)
	}

	// PATCH variant price to 1500
	body := map[string]interface{}{
		"price_paise": 1500,
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/admin/locations/%s/services/%s", locationID, varID), bytes.NewReader(jsonBody))
	ctxReq := req.Context()
	ctxReq = context.WithValue(ctxReq, auth.CtxTenantID, tenantID.String())
	ctxReq = context.WithValue(ctxReq, auth.CtxLocationID, locationID.String())
	ctxReq = context.WithValue(ctxReq, auth.CtxStaffMemberID, staffID.String())
	ctxReq = context.WithValue(ctxReq, auth.CtxRole, "owner")
	req = req.WithContext(ctxReq)

	rec := httptest.NewRecorder()
	s.UpdateServiceVariant(rec, req, locationID, varID)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Response: %s", rec.Code, rec.Body.String())
	}

	// Verify price is updated in DB
	var dbPrice int
	err = pool.QueryRow(ctx, "SELECT price_paise FROM service_variants WHERE id = $1", varID).Scan(&dbPrice)
	if err != nil {
		t.Fatalf("Failed to query variant price: %v", err)
	}
	if dbPrice != 1500 {
		t.Errorf("Expected variant price to be 1500, got %d", dbPrice)
	}

	// Verify visit_services snapshot is unchanged
	var snapshotPrice int
	err = pool.QueryRow(ctx, "SELECT price_paise_snapshot FROM visit_services WHERE visit_id = $1", visitID).Scan(&snapshotPrice)
	if err != nil {
		t.Fatalf("Failed to query snapshot price: %v", err)
	}
	if snapshotPrice != 1000 {
		t.Errorf("Expected snapshot price to remain 1000, got %d", snapshotPrice)
	}

	// Verify booking rules remain unchanged
	var allowWalkIn, allowAppt, reqAppt bool
	err = pool.QueryRow(ctx, "SELECT allow_walk_in, allow_appointment, requires_appointment FROM service_variants WHERE id = $1", varID).Scan(&allowWalkIn, &allowAppt, &reqAppt)
	if err != nil {
		t.Fatalf("Failed to query variant booking rules: %v", err)
	}
	if !allowWalkIn || !allowAppt || reqAppt {
		t.Errorf("Expected booking rules to remain unchanged (true, true, false), got (%t, %t, %t)", allowWalkIn, allowAppt, reqAppt)
	}
}
