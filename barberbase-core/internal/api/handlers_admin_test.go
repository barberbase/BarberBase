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
	"barberbase-core/internal/config"
	"barberbase-core/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
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

func TestCreateStaffMember_Integration(t *testing.T) {
	s, pool, tenantID, locationID, staffID, _ := setupAdminTestServer(t)
	defer pool.Close()

	// 1. request-otp with a phone never inserted → 401 (validates no self-registration)
	{
		otpBody := RequestStaffOTPJSONBody{
			PhoneNumber: "+919876543210",
		}
		otpBodyBytes, _ := json.Marshal(otpBody)
		reqOTP := httptest.NewRequest(http.MethodPost, "/auth/staff/request-otp", bytes.NewReader(otpBodyBytes))
		recOTP := httptest.NewRecorder()
		s.RequestStaffOTP(recOTP, reqOTP)
		if recOTP.Code != http.StatusUnauthorized {
			t.Fatalf("Expected 401 Unauthorized for unregistered phone, got %d. Body: %s", recOTP.Code, recOTP.Body.String())
		}
	}

	// 2. Insert via CreateStaffMember, then request-otp with that phone → 200
	{
		createBody := CreateStaffMemberJSONRequestBody{
			Name:        "Test Staff Member",
			PhoneNumber: "+919876543210",
			Role:        CreateStaffMemberJSONBodyRoleBarber,
		}
		createBodyBytes, _ := json.Marshal(createBody)
		reqCreate := httptest.NewRequest(http.MethodPost, "/admin/staff", bytes.NewReader(createBodyBytes))
		ctxCreate := reqCreate.Context()
		ctxCreate = context.WithValue(ctxCreate, auth.CtxTenantID, tenantID.String())
		ctxCreate = context.WithValue(ctxCreate, auth.CtxLocationID, locationID.String())
		ctxCreate = context.WithValue(ctxCreate, auth.CtxStaffMemberID, staffID.String())
		ctxCreate = context.WithValue(ctxCreate, auth.CtxRole, "manager")
		reqCreate = reqCreate.WithContext(ctxCreate)

		recCreate := httptest.NewRecorder()
		s.CreateStaffMember(recCreate, reqCreate)
		if recCreate.Code != http.StatusCreated {
			t.Fatalf("Expected 201 Created for staff creation, got %d, body: %s", recCreate.Code, recCreate.Body.String())
		}

		otpBody := RequestStaffOTPJSONBody{
			PhoneNumber: "+919876543210",
		}
		otpBodyBytes, _ := json.Marshal(otpBody)
		reqOTP2 := httptest.NewRequest(http.MethodPost, "/auth/staff/request-otp", bytes.NewReader(otpBodyBytes))
		recOTP2 := httptest.NewRecorder()
		s.RequestStaffOTP(recOTP2, reqOTP2)
		if recOTP2.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK for registered phone, got %d, body: %s", recOTP2.Code, recOTP2.Body.String())
		}
	}

	// 3. Duplicate phone on second create → 409
	{
		createBody := CreateStaffMemberJSONRequestBody{
			Name:        "Another Staff",
			PhoneNumber: "+919876543210", // duplicate phone
			Role:        CreateStaffMemberJSONBodyRoleBarber,
		}
		createBodyBytes, _ := json.Marshal(createBody)
		reqDup := httptest.NewRequest(http.MethodPost, "/admin/staff", bytes.NewReader(createBodyBytes))
		ctxDup := reqDup.Context()
		ctxDup = context.WithValue(ctxDup, auth.CtxTenantID, tenantID.String())
		ctxDup = context.WithValue(ctxDup, auth.CtxLocationID, locationID.String())
		ctxDup = context.WithValue(ctxDup, auth.CtxStaffMemberID, staffID.String())
		ctxDup = context.WithValue(ctxDup, auth.CtxRole, "manager")
		reqDup = reqDup.WithContext(ctxDup)

		recDup := httptest.NewRecorder()
		s.CreateStaffMember(recDup, reqDup)
		if recDup.Code != http.StatusConflict {
			t.Fatalf("Expected 409 Conflict for duplicate phone, got %d, body: %s", recDup.Code, recDup.Body.String())
		}

		var errResp map[string]interface{}
		_ = json.Unmarshal(recDup.Body.Bytes(), &errResp)
		if errResp["code"] != "PHONE_ALREADY_EXISTS" {
			t.Fatalf("Expected error code PHONE_ALREADY_EXISTS, got %v", errResp["code"])
		}
	}

	// 4. barber role JWT → 403 on create attempt
	{
		createBody := CreateStaffMemberJSONRequestBody{
			Name:        "Barber Created Staff",
			PhoneNumber: "+919876543211",
			Role:        CreateStaffMemberJSONBodyRoleBarber,
		}
		createBodyBytes, _ := json.Marshal(createBody)
		reqBarber := httptest.NewRequest(http.MethodPost, "/admin/staff", bytes.NewReader(createBodyBytes))
		ctxBarber := reqBarber.Context()
		ctxBarber = context.WithValue(ctxBarber, auth.CtxTenantID, tenantID.String())
		ctxBarber = context.WithValue(ctxBarber, auth.CtxLocationID, locationID.String())
		ctxBarber = context.WithValue(ctxBarber, auth.CtxStaffMemberID, staffID.String())
		ctxBarber = context.WithValue(ctxBarber, auth.CtxRole, "barber") // Role is barber
		reqBarber = reqBarber.WithContext(ctxBarber)

		recBarber := httptest.NewRecorder()
		s.CreateStaffMember(recBarber, reqBarber)
		if recBarber.Code != http.StatusForbidden {
			t.Fatalf("Expected 403 Forbidden for barber role, got %d, body: %s", recBarber.Code, recBarber.Body.String())
		}
	}

	// 5. Body-supplied tenant_id has no effect; inserted row uses JWT tenant_id
	{
		bodyWithFakeTenant := map[string]interface{}{
			"name":         "Ignored Tenant Staff",
			"phone_number": "+919876543212",
			"role":         "barber",
			"tenant_id":    uuid.New().String(), // Supplying fake tenant_id
		}
		bodyWithFakeTenantBytes, _ := json.Marshal(bodyWithFakeTenant)
		reqFake := httptest.NewRequest(http.MethodPost, "/admin/staff", bytes.NewReader(bodyWithFakeTenantBytes))
		ctxFake := reqFake.Context()
		ctxFake = context.WithValue(ctxFake, auth.CtxTenantID, tenantID.String()) // JWT has correct tenantID
		ctxFake = context.WithValue(ctxFake, auth.CtxLocationID, locationID.String())
		ctxFake = context.WithValue(ctxFake, auth.CtxStaffMemberID, staffID.String())
		ctxFake = context.WithValue(ctxFake, auth.CtxRole, "owner")
		reqFake = reqFake.WithContext(ctxFake)

		recFake := httptest.NewRecorder()
		s.CreateStaffMember(recFake, reqFake)
		if recFake.Code != http.StatusCreated {
			t.Fatalf("Expected 201 Created, got %d", recFake.Code)
		}

		var dbTenantID uuid.UUID
		err := pool.QueryRow(context.Background(), "SELECT tenant_id FROM staff_members WHERE phone_number = $1", "+919876543212").Scan(&dbTenantID)
		if err != nil {
			t.Fatalf("Failed to query inserted staff member's tenant_id: %v", err)
		}
		if dbTenantID != tenantID {
			t.Fatalf("Expected staff member tenant_id to be %s (from JWT), got %s", tenantID, dbTenantID)
		}
	}

	// 6. Role values owner and anything outside [manager, barber] in the request body → 422
	{
		bodyOwner := map[string]interface{}{
			"name":         "Invalid Role Staff",
			"phone_number": "+919876543213",
			"role":         "owner", // Role is owner
		}
		bodyOwnerBytes, _ := json.Marshal(bodyOwner)
		reqOwner := httptest.NewRequest(http.MethodPost, "/admin/staff", bytes.NewReader(bodyOwnerBytes))
		ctxOwner := reqOwner.Context()
		ctxOwner = context.WithValue(ctxOwner, auth.CtxTenantID, tenantID.String())
		ctxOwner = context.WithValue(ctxOwner, auth.CtxLocationID, locationID.String())
		ctxOwner = context.WithValue(ctxOwner, auth.CtxStaffMemberID, staffID.String())
		ctxOwner = context.WithValue(ctxOwner, auth.CtxRole, "manager")
		reqOwner = reqOwner.WithContext(ctxOwner)

		recOwner := httptest.NewRecorder()
		s.CreateStaffMember(recOwner, reqOwner)
		if recOwner.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Expected 422 Unprocessable Entity for role 'owner', got %d", recOwner.Code)
		}

		var errResp map[string]interface{}
		_ = json.Unmarshal(recOwner.Body.Bytes(), &errResp)
		if errResp["code"] != "INVALID_ROLE" {
			t.Fatalf("Expected error code INVALID_ROLE, got %v", errResp["code"])
		}
	}
}

func TestProvisionTenant_Integration(t *testing.T) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	// Setup Server and Config
	cfg, err := config.Load()
	require.NoError(t, err)

	// Set test PLATFORM_ADMIN_KEY
	originalAdminKey := cfg.PlatformAdminKey
	cfg.PlatformAdminKey = "super-secret-test-platform-admin-key"
	defer func() {
		cfg.PlatformAdminKey = originalAdminKey
	}()

	pool, err := repository.InitPool(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

	s := &Server{
		Pool:   pool,
		Bhejna: mockBhejna{},
		Config: cfg,
	}

	// Helper to truncate db
	cleanDB := func() {
		_, err := pool.Exec(ctx, "TRUNCATE tenants, locations, staff_members, service_categories, service_groups, service_variants, visits, visit_services, webhook_events, staff_otps, location_status_overrides CASCADE")
		require.NoError(t, err)
	}

	// 1. Success Provisioning & check PIN consistency & OTP request
	t.Run("Successful tenant provisioning", func(t *testing.T) {
		cleanDB()

		body := map[string]interface{}{
			"tenant_name":   "New Salon",
			"tenant_slug":   "new-salon",
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210",
			"location_name": "New Salon Koramangala",
			"location_slug": "new-salon/koramangala",
			"address":       "123 Road",
			"timezone":      "Asia/Kolkata",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "super-secret-test-platform-admin-key")
		rec := httptest.NewRecorder()

		s.ProvisionTenant(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var resp struct {
			TenantID           string `json:"tenant_id"`
			LocationID         string `json:"location_id"`
			OwnerStaffMemberID string `json:"owner_staff_member_id"`
			ArrivalPin         string `json:"arrival_pin"`
		}
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		require.NotEmpty(t, resp.TenantID)
		require.NotEmpty(t, resp.LocationID)
		require.NotEmpty(t, resp.OwnerStaffMemberID)
		require.Len(t, resp.ArrivalPin, 6)

		// Verify database state
		var ownerPhone string
		err = pool.QueryRow(ctx, "SELECT owner_phone_number FROM tenants WHERE id = $1", resp.TenantID).Scan(&ownerPhone)
		require.NoError(t, err)
		require.Equal(t, "+919876543210", ownerPhone)

		var locationSlug, arrivalPinPlain, arrivalPinHash string
		err = pool.QueryRow(ctx, "SELECT slug, arrival_pin_plain, arrival_pin_hash FROM locations WHERE id = $1", resp.LocationID).Scan(&locationSlug, &arrivalPinPlain, &arrivalPinHash)
		require.NoError(t, err)
		require.Equal(t, "new-salon/koramangala", locationSlug)
		require.Equal(t, resp.ArrivalPin, arrivalPinPlain)

		// PIN plain and hash consistency: verify hash using bcrypt
		err = bcrypt.CompareHashAndPassword([]byte(arrivalPinHash), []byte(resp.ArrivalPin))
		require.NoError(t, err)

		// Owner can immediately request OTP
		otpBody := map[string]interface{}{
			"phone_number": "+919876543210",
		}
		otpJsonBody, _ := json.Marshal(otpBody)
		reqOTP := httptest.NewRequest(http.MethodPost, "/auth/staff/request-otp", bytes.NewReader(otpJsonBody))
		recOTP := httptest.NewRecorder()
		s.RequestStaffOTP(recOTP, reqOTP)
		require.Equal(t, http.StatusOK, recOTP.Code)
	})

	// 2. Wrong PLATFORM_ADMIN_KEY -> 401
	t.Run("Wrong Platform Admin Key", func(t *testing.T) {
		cleanDB()

		body := map[string]interface{}{
			"tenant_name":   "New Salon",
			"tenant_slug":   "new-salon",
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210",
			"location_name": "New Salon Koramangala",
			"location_slug": "new-salon/koramangala",
		}
		jsonBody, _ := json.Marshal(body)

		// Create router to test middleware integration
		r := chi.NewRouter()
		r.Route("/v1", func(r chi.Router) {
			r.With(s.PlatformAdminKeyMiddleware).Post("/admin/setup", s.ProvisionTenant)
		})

		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "wrong-key")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	// 3. Duplicate tenant_slug -> 409 + rollback (zero rows created)
	t.Run("Duplicate tenant slug rollback", func(t *testing.T) {
		cleanDB()

		// Create first tenant manually
		tenantID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'First', 'first-slug', '+919999999999')", tenantID)
		require.NoError(t, err)

		body := map[string]interface{}{
			"tenant_name":   "Second Salon",
			"tenant_slug":   "first-slug", // duplicate tenant slug
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210",
			"location_name": "Second Salon Koramangala",
			"location_slug": "first-slug/koramangala",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "super-secret-test-platform-admin-key")
		rec := httptest.NewRecorder()

		s.ProvisionTenant(rec, req)
		require.Equal(t, http.StatusConflict, rec.Code)

		var errResp map[string]interface{}
		err = json.Unmarshal(rec.Body.Bytes(), &errResp)
		require.NoError(t, err)
		require.Equal(t, "TENANT_SLUG_CONFLICT", errResp["code"])

		// Verify rollback: no new location or staff member was inserted
		var locationCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM locations WHERE slug = 'first-slug/koramangala'").Scan(&locationCount)
		require.NoError(t, err)
		require.Equal(t, 0, locationCount)

		var staffCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM staff_members WHERE phone_number = '+919876543210'").Scan(&staffCount)
		require.NoError(t, err)
		require.Equal(t, 0, staffCount)
	})

	// 4. Duplicate location_slug -> 409 + zero rows
	t.Run("Duplicate location slug rollback", func(t *testing.T) {
		cleanDB()

		// Create first tenant and location manually with "second-slug/loc-slug"
		tenantID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'First', 'first-slug', '+919999999999')", tenantID)
		require.NoError(t, err)

		locationID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'Location', 'second-slug/loc-slug')", locationID, tenantID)
		require.NoError(t, err)

		body := map[string]interface{}{
			"tenant_name":   "Second Salon",
			"tenant_slug":   "second-slug",
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210",
			"location_name": "Second Salon Koramangala",
			"location_slug": "second-slug/loc-slug", // duplicate location slug
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "super-secret-test-platform-admin-key")
		rec := httptest.NewRecorder()

		s.ProvisionTenant(rec, req)
		require.Equal(t, http.StatusConflict, rec.Code)

		var errResp map[string]interface{}
		err = json.Unmarshal(rec.Body.Bytes(), &errResp)
		require.NoError(t, err)
		require.Equal(t, "LOCATION_SLUG_CONFLICT", errResp["code"])

		// Verify rollback: no new tenant or staff member was inserted
		var tenantCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM tenants WHERE slug = 'second-slug'").Scan(&tenantCount)
		require.NoError(t, err)
		require.Equal(t, 0, tenantCount)

		var staffCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM staff_members WHERE phone_number = '+919876543210'").Scan(&staffCount)
		require.NoError(t, err)
		require.Equal(t, 0, staffCount)
	})

	// 5. Duplicate owner_phone -> 409 + zero rows
	t.Run("Duplicate owner phone rollback", func(t *testing.T) {
		cleanDB()

		// Create first tenant, location, and owner staff manually
		tenantID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO tenants (id, name, slug, owner_phone_number) VALUES ($1, 'First', 'first-slug', '+919999999999')", tenantID)
		require.NoError(t, err)

		locationID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'Location', 'first-slug/loc-slug')", locationID, tenantID)
		require.NoError(t, err)

		ownerID := uuid.New()
		_, err = pool.Exec(ctx, "INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active) VALUES ($1, $2, $3, 'Owner', '+919876543210', 'owner', true)", ownerID, tenantID, locationID)
		require.NoError(t, err)

		body := map[string]interface{}{
			"tenant_name":   "Second Salon",
			"tenant_slug":   "second-slug",
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210", // duplicate owner phone
			"location_name": "Second Salon Koramangala",
			"location_slug": "second-slug/loc-slug",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "super-secret-test-platform-admin-key")
		rec := httptest.NewRecorder()

		s.ProvisionTenant(rec, req)
		require.Equal(t, http.StatusConflict, rec.Code)

		var errResp map[string]interface{}
		err = json.Unmarshal(rec.Body.Bytes(), &errResp)
		require.NoError(t, err)
		require.Equal(t, "OWNER_PHONE_CONFLICT", errResp["code"])

		// Verify rollback: no new tenant or location was inserted
		var tenantCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM tenants WHERE slug = 'second-slug'").Scan(&tenantCount)
		require.NoError(t, err)
		require.Equal(t, 0, tenantCount)

		var locationCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM locations WHERE slug = 'second-slug/loc-slug'").Scan(&locationCount)
		require.NoError(t, err)
		require.Equal(t, 0, locationCount)
	})

	// 6. Invalid location slug prefix -> 422
	t.Run("Invalid location slug prefix", func(t *testing.T) {
		cleanDB()

		body := map[string]interface{}{
			"tenant_name":   "New Salon",
			"tenant_slug":   "new-salon",
			"owner_name":    "Owner Name",
			"owner_phone":   "+919876543210",
			"location_name": "New Salon Koramangala",
			"location_slug": "wrong-prefix/koramangala", // invalid prefix
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/setup", bytes.NewReader(jsonBody))
		req.Header.Set("X-Platform-Admin-Key", "super-secret-test-platform-admin-key")
		rec := httptest.NewRecorder()

		s.ProvisionTenant(rec, req)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

		var errResp map[string]interface{}
		err = json.Unmarshal(rec.Body.Bytes(), &errResp)
		require.NoError(t, err)
		require.Equal(t, "INVALID_LOCATION_SLUG", errResp["code"])
	})
}

