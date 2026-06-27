package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// Helper to respond with JSON
func respondAdminJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// GetAdminLocationsLocationIdServices lists all services (full catalog)
func (s *Server) GetAdminLocationsLocationIdServices(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()

	// 1. Role Gate
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	// 2. Location ownership and display mode fetch
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var serviceDisplayMode string
	err = s.Pool.QueryRow(ctx, `
		SELECT service_display_mode FROM locations
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`, locationId, tenantID).Scan(&serviceDisplayMode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondAdminJSON(w, http.StatusNotFound, map[string]string{"error": "location not found"})
			return
		}
		log.Printf("[Error] Failed to query location: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 3. Fetch services hierarchy
	repo := &repository.ServiceRepository{Pool: s.Pool}
	categories, err := repo.ListServicesForAdmin(ctx, tenantID.String(), locationId.String())
	if err != nil {
		log.Printf("[Error] ListServicesForAdmin failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 4. Map repository rows -> generated OpenAPI types
	apiCategories := make([]ServiceCategory, len(categories))
	for i, c := range categories {
		cID, _ := uuid.Parse(c.ID)
		
		apiGroups := make([]ServiceGroup, len(c.Groups))
		for j, g := range c.Groups {
			gID, _ := uuid.Parse(g.ID)
			
			apiVariants := make([]ServiceVariant, len(g.Variants))
			for k, v := range g.Variants {
				vID, _ := uuid.Parse(v.ID)
				isPopularVal := v.IsPopular
				
				apiVariants[k] = ServiceVariant{
					Id:                  vID,
					Name:                v.Name,
					Description:         v.Description,
					DurationMinutes:     v.DurationMinutes,
					PricePaise:          v.PricePaise,
					AllowWalkIn:         v.AllowWalkIn,
					AllowAppointment:    v.AllowAppointment,
					RequiresAppointment: v.RequiresAppointment,
					IsPopular:           &isPopularVal,
				}
			}

			apiGroups[j] = ServiceGroup{
				Id:          gID,
				Name:        g.Name,
				Description: g.Description,
				Variants:    apiVariants,
			}
		}

		sortOrderVal := c.SortOrder
		genderVal := ServiceCategoryGender(c.Gender)

		apiCategories[i] = ServiceCategory{
			Id:        cID,
			Name:      c.Name,
			Gender:    genderVal,
			SortOrder: &sortOrderVal,
			Groups:    apiGroups,
		}
	}

	resp := ServiceCatalog{
		LocationId:  locationId,
		DisplayMode: ServiceCatalogDisplayMode(serviceDisplayMode),
		Categories:  apiCategories,
	}

	respondAdminJSON(w, http.StatusOK, resp)
}

// CreateServiceVariant creates a new service variant
func (s *Server) CreateServiceVariant(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()

	// 1. Role Gate
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	// 2. Location ownership validation
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var dummy string
	err = s.Pool.QueryRow(ctx, `
		SELECT id FROM locations
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`, locationId, tenantID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondAdminJSON(w, http.StatusNotFound, map[string]string{"error": "location not found"})
			return
		}
		log.Printf("[Error] Failed to query location: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 3. Decode request body
	var body CreateServiceVariantJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Validate required fields
	if body.CategoryName == "" || body.GroupName == "" || body.VariantName == "" {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "missing required parameters"})
		return
	}
	if body.DurationMinutes < 1 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "duration_minutes must be >= 1"})
		return
	}
	if body.PricePaise < 0 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "price_paise must be >= 0"})
		return
	}

	// Determine defaults
	gender := "unisex"
	if body.CategoryGender != nil && *body.CategoryGender != "" {
		g := string(*body.CategoryGender)
		if g != "men" && g != "women" && g != "unisex" {
			respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid category_gender"})
			return
		}
		gender = g
	}

	allowWalkIn := true
	if body.AllowWalkIn != nil {
		allowWalkIn = *body.AllowWalkIn
	}

	allowAppointment := true
	if body.AllowAppointment != nil {
		allowAppointment = *body.AllowAppointment
	}

	requiresAppointment := false
	if body.RequiresAppointment != nil {
		requiresAppointment = *body.RequiresAppointment
	}

	isPopular := false
	if body.IsPopular != nil {
		isPopular = *body.IsPopular
	}

	// 4. Create variant in repository
	repo := &repository.ServiceRepository{Pool: s.Pool}
	params := repository.CreateServiceVariantParams{
		CategoryName:        body.CategoryName,
		CategoryGender:      gender,
		GroupName:           body.GroupName,
		VariantName:         body.VariantName,
		DurationMinutes:     body.DurationMinutes,
		PricePaise:          body.PricePaise,
		AllowWalkIn:         allowWalkIn,
		AllowAppointment:    allowAppointment,
		RequiresAppointment: requiresAppointment,
		IsPopular:           isPopular,
	}

	variant, err := repo.CreateServiceVariant(ctx, tenantID.String(), locationId.String(), params)
	if err != nil {
		if errors.Is(err, repository.ErrVariantExists) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{"error": "variant name already exists in this group"})
			return
		}
		log.Printf("[Error] CreateServiceVariant failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 5. Map created variant to ServiceVariant and respond
	vID, _ := uuid.Parse(variant.ID)
	isPopularVal := variant.IsPopular

	resp := ServiceVariant{
		Id:                  vID,
		Name:                variant.Name,
		Description:         variant.Description,
		DurationMinutes:     variant.DurationMinutes,
		PricePaise:          variant.PricePaise,
		AllowWalkIn:         variant.AllowWalkIn,
		AllowAppointment:    variant.AllowAppointment,
		RequiresAppointment: variant.RequiresAppointment,
		IsPopular:           &isPopularVal,
	}

	respondAdminJSON(w, http.StatusCreated, resp)
}

// UpdateServiceVariant updates a service variant
func (s *Server) UpdateServiceVariant(w http.ResponseWriter, r *http.Request, locationId UUIDv7, variantId UUIDv7) {
	ctx := r.Context()

	// 1. Role Gate
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	// 2. Location ownership validation
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var dummy string
	err = s.Pool.QueryRow(ctx, `
		SELECT id FROM locations
		WHERE id = $1 AND tenant_id = $2 AND is_active = true
	`, locationId, tenantID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondAdminJSON(w, http.StatusNotFound, map[string]string{"error": "location not found"})
			return
		}
		log.Printf("[Error] Failed to query location: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 3. Decode request body
	var body UpdateServiceVariantJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Validate inputs
	if body.DurationMinutes != nil && *body.DurationMinutes < 1 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "duration_minutes must be >= 1"})
		return
	}
	if body.PricePaise != nil && *body.PricePaise < 0 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "price_paise must be >= 0"})
		return
	}

	// 4. Update variant in repository
	repo := &repository.ServiceRepository{Pool: s.Pool}
	params := repository.UpdateServiceVariantParams{
		VariantName:     body.VariantName,
		DurationMinutes: body.DurationMinutes,
		PricePaise:      body.PricePaise,
		IsActive:        body.IsActive,
		IsPopular:       body.IsPopular,
	}

	variant, err := repo.UpdateServiceVariant(ctx, tenantID.String(), locationId.String(), variantId.String(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondAdminJSON(w, http.StatusNotFound, map[string]string{"error": "variant not found"})
			return
		}
		if errors.Is(err, repository.ErrVariantExists) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{"error": "variant name already exists in this group"})
			return
		}
		log.Printf("[Error] UpdateServiceVariant failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// 5. Map updated variant and respond
	vID, _ := uuid.Parse(variant.ID)
	isPopularVal := variant.IsPopular

	resp := ServiceVariant{
		Id:                  vID,
		Name:                variant.Name,
		Description:         variant.Description,
		DurationMinutes:     variant.DurationMinutes,
		PricePaise:          variant.PricePaise,
		AllowWalkIn:         variant.AllowWalkIn,
		AllowAppointment:    variant.AllowAppointment,
		RequiresAppointment: variant.RequiresAppointment,
		IsPopular:           &isPopularVal,
	}

	respondAdminJSON(w, http.StatusOK, resp)
}

// CreateStaffMember adds a new staff member
func (s *Server) CreateStaffMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract JWT claims from context
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"code": "FORBIDDEN", "message": "insufficient role"})
		return
	}

	locationIDStr := auth.LocationIDFromCtx(ctx)
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"code": "FORBIDDEN", "message": "insufficient role"})
		return
	}

	callerRole := auth.RoleFromCtx(ctx)

	// 2. Role Gate: barber role is forbidden
	if callerRole == "barber" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"code": "FORBIDDEN", "message": "insufficient role"})
		return
	}

	// 3. Decode JSON request body
	var body CreateStaffMemberJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_REQUEST", "message": "invalid JSON body"})
		return
	}

	// 4. Validate required fields are not empty/missing
	if body.Name == "" || body.PhoneNumber == "" || body.Role == "" {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_REQUEST", "message": "missing or empty required fields: name, phone_number, role"})
		return
	}

	// 5. Validate phone number matches E.164 pattern
	phoneRegex := regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	if !phoneRegex.MatchString(string(body.PhoneNumber)) {
		respondAdminJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_PHONE", "message": "phone_number must be E.164"})
		return
	}

	// 6. Validate role is exactly "manager" or "barber"
	roleStr := string(body.Role)
	if roleStr != "manager" && roleStr != "barber" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_ROLE", "message": "role must be manager or barber"})
		return
	}

	// 7. Validate name is non-empty after strings.TrimSpace and len <= 100
	trimmedName := strings.TrimSpace(body.Name)
	if trimmedName == "" || len(trimmedName) > 100 {
		respondAdminJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_NAME", "message": "name must be non-empty and less than or equal to 100 characters"})
		return
	}

	// 8. Insert staff member via repository
	insertParams := repository.InsertStaffMemberParams{
		TenantID:    tenantID,
		LocationID:  locationID,
		Name:        trimmedName,
		PhoneNumber: string(body.PhoneNumber),
		Role:        roleStr,
	}

	_, err = repository.InsertStaffMember(ctx, s.Pool, insertParams)
	if err != nil {
		if errors.Is(err, repository.ErrPhoneAlreadyExists) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{"code": "PHONE_ALREADY_EXISTS", "message": "A staff member with this phone number already exists"})
			return
		}
		log.Printf("[Error] InsertStaffMember failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_SERVER_ERROR", "message": "internal server error"})
		return
	}

	// 9. Success response with 201 Created and no body
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) PlatformAdminKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Platform-Admin-Key")
		if key == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		expectedKey := s.Config.PlatformAdminKey
		if expectedKey == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if subtle.ConstantTimeCompare([]byte(key), []byte(expectedKey)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func generateArrivalPIN() (string, error) {
	const digits = "0123456789"
	var pin []byte
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	for _, b := range bytes {
		pin = append(pin, digits[int(b)%len(digits)])
	}
	return string(pin), nil
}

func (s *Server) ProvisionTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body ProvisionTenantJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	// 1. Validation
	if strings.TrimSpace(body.TenantName) == "" || len(body.TenantName) > 255 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "tenant_name is required and must be <= 255 chars",
		})
		return
	}

	slugRegex := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !slugRegex.MatchString(body.TenantSlug) || len(body.TenantSlug) > 100 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "tenant_slug must be URL-safe and <= 100 chars",
		})
		return
	}

	if strings.TrimSpace(body.OwnerName) == "" || len(body.OwnerName) > 100 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "owner_name is required and must be <= 100 chars",
		})
		return
	}

	phoneRegex := regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	if !phoneRegex.MatchString(string(body.OwnerPhone)) {
		respondAdminJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code":    "INVALID_PHONE",
			"message": "owner_phone must be in E.164 format",
		})
		return
	}

	if strings.TrimSpace(body.LocationName) == "" || len(body.LocationName) > 255 {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "location_name is required and must be <= 255 chars",
		})
		return
	}

	if !strings.HasPrefix(body.LocationSlug, body.TenantSlug+"/") {
		respondAdminJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code":    "INVALID_LOCATION_SLUG",
			"message": "location_slug must be prefixed with tenant_slug",
		})
		return
	}

	// 2. Generate secure 6-digit arrival PIN
	plainPin, err := generateArrivalPIN()
	if err != nil {
		log.Printf("[Error] Failed to generate arrival PIN: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "failed to generate arrival PIN",
		})
		return
	}

	// 3. Hash the PIN using bcrypt
	pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(plainPin), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[Error] Failed to bcrypt hash arrival PIN: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "failed to hash arrival PIN",
		})
		return
	}
	pinHash := string(pinHashBytes)

	// 4. Invoke repository layer inside transaction
	var address *string
	if body.Address != nil {
		address = body.Address
	}
	timezone := "Asia/Kolkata"
	if body.Timezone != nil && *body.Timezone != "" {
		timezone = *body.Timezone
	}

	params := repository.ProvisionTenantParams{
		TenantName:      strings.TrimSpace(body.TenantName),
		TenantSlug:      strings.TrimSpace(body.TenantSlug),
		OwnerName:       strings.TrimSpace(body.OwnerName),
		OwnerPhone:      string(body.OwnerPhone),
		LocationName:    strings.TrimSpace(body.LocationName),
		LocationSlug:    strings.TrimSpace(body.LocationSlug),
		Address:         address,
		Timezone:        timezone,
		ArrivalPinPlain: plainPin,
		ArrivalPinHash:  pinHash,
	}

	result, err := repository.ProvisionTenant(ctx, s.Pool, params)
	if err != nil {
		if errors.Is(err, repository.ErrTenantSlugConflict) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{
				"code":    "TENANT_SLUG_CONFLICT",
				"message": "tenant_slug already exists",
			})
			return
		}
		if errors.Is(err, repository.ErrLocationSlugConflict) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{
				"code":    "LOCATION_SLUG_CONFLICT",
				"message": "location_slug already exists",
			})
			return
		}
		if errors.Is(err, repository.ErrOwnerPhoneConflict) {
			respondAdminJSON(w, http.StatusConflict, map[string]string{
				"code":    "OWNER_PHONE_CONFLICT",
				"message": "owner_phone already exists",
			})
			return
		}

		log.Printf("[Error] ProvisionTenant transaction failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 5. Respond with 201 Created and IDs + plain PIN
	resp := map[string]string{
		"tenant_id":             result.TenantID.String(),
		"location_id":           result.LocationID.String(),
		"owner_staff_member_id": result.OwnerStaffMemberID.String(),
		"arrival_pin":           plainPin,
	}
	respondAdminJSON(w, http.StatusCreated, resp)
}

// ConnectWhatsAppModeB connects a shop's own WABA
func (s *Server) ConnectWhatsAppModeB(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()

	// 1. Auth & Role Gate
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, ErrorResponse{Code: "FORBIDDEN", Message: "insufficient role"})
		return
	}

	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, ErrorResponse{Code: "FORBIDDEN", Message: "insufficient role"})
		return
	}

	// 2. Parse request body
	var body ConnectWhatsAppModeBJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, ErrorResponse{Code: "INVALID_REQUEST", Message: "invalid JSON body"})
		return
	}

	// Validate fields
	if body.BhejnaConfigVersion != "1" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "unsupported_config_version",
			Message: "bhejna_config_version must be \"1\"",
		})
		return
	}
	if body.WhatsappStatus != "ACTIVE" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "whatsapp_not_active",
			Message: "whatsapp_status must be ACTIVE",
		})
		return
	}
	if body.QualityRating != nil && *body.QualityRating == "RED" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "quality_rating_red",
			Message: "quality rating must not be RED",
		})
		return
	}

	phoneRegex := regexp.MustCompile(`^\+[1-9]\d{9,14}$`)
	if !phoneRegex.MatchString(string(body.PhoneNumber)) {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "invalid_phone_number",
			Message: "phone_number must be E.164",
		})
		return
	}
	if body.ApiKey == "" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "missing_api_key",
			Message: "api_key is required",
		})
		return
	}
	if body.WebhookSecret == "" {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "missing_webhook_secret",
			Message: "webhook_secret is required",
		})
		return
	}

	// 3. Test-send (credential validation)
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	bhejnaAPIURL := os.Getenv("BHEJNA_API_URL")
	if bhejnaAPIURL == "" {
		bhejnaAPIURL = "https://bhejna-api.codenxtlab.tech"
	}
	reqURL := bhejnaAPIURL + "/v1/account"
	req, err := http.NewRequestWithContext(timeoutCtx, "GET", reqURL, nil)
	if err != nil {
		respondAdminJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to create test-send request"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+body.ApiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "bhejna_unreachable",
			Message: "Could not reach Bhejna API",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "bhejna_auth_failed",
			Message: "Bhejna API key is invalid",
		})
		return
	}
	if resp.StatusCode >= 500 {
		respondAdminJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
			Code:    "bhejna_unreachable",
			Message: "Could not reach Bhejna API",
		})
		return
	}

	// 4. AES-256-GCM encrypt
	encryptedKey, err := bhejna.AESGCMEncrypt(body.ApiKey, s.Config.AESEncryptionKey)
	if err != nil {
		respondAdminJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "encryption failed"})
		return
	}
	encryptedSecret, err := bhejna.AESGCMEncrypt(body.WebhookSecret, s.Config.AESEncryptionKey)
	if err != nil {
		respondAdminJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "encryption failed"})
		return
	}

	// 5. Repository update
	err = repository.ConnectModeBWhatsApp(ctx, s.Pool, uuid.UUID(locationId), tenantID, string(body.PhoneNumber), encryptedKey, encryptedSecret)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondAdminJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "location not found or wrong tenant"})
			return
		}
		log.Printf("[Error] ConnectModeBWhatsApp failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "internal server error"})
		return
	}

	// 6. Return response
	webhookURL := "https://api.barberbase.in/v1/webhooks/bhejna/loc/" + locationId.String()
	respondAdminJSON(w, http.StatusOK, map[string]interface{}{
		"whatsapp_mode": "own_number",
		"webhook_url":   webhookURL,
	})
}

// DisconnectWhatsAppModeB disconnects a shop's WABA and reverts to shared platform mode
func (s *Server) DisconnectWhatsAppModeB(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()

	// Role Gate
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, ErrorResponse{Code: "FORBIDDEN", Message: "insufficient role"})
		return
	}

	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, ErrorResponse{Code: "FORBIDDEN", Message: "insufficient role"})
		return
	}

	err = repository.DisconnectModeBWhatsApp(ctx, s.Pool, uuid.UUID(locationId), tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondAdminJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "location not found or wrong tenant"})
			return
		}
		log.Printf("[Error] DisconnectModeBWhatsApp failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "internal server error"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RegenerateArrivalPin implements POST /admin/locations/{location_id}/arrival-pin/regenerate
func (s *Server) RegenerateArrivalPin(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()

	tenantIDStr := auth.TenantIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondAdminJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant claim"})
		return
	}

	locationID := uuid.UUID(locationId)

	plainPin, err := generateArrivalPIN()
	if err != nil {
		log.Printf("[Error] RegenerateArrivalPin: failed to generate PIN: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "failed to generate PIN"})
		return
	}

	pinHashBytes, err := bcrypt.GenerateFromPassword([]byte(plainPin), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[Error] RegenerateArrivalPin: bcrypt failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "failed to hash PIN"})
		return
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE locations
		SET arrival_pin_plain = $1,
		    arrival_pin_hash  = $2,
		    updated_at        = NOW()
		WHERE id = $3
		  AND tenant_id = $4`,
		plainPin, string(pinHashBytes), locationID, tenantID,
	)
	if err != nil {
		log.Printf("[Error] RegenerateArrivalPin: db update failed: %v", err)
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	if tag.RowsAffected() == 0 {
		respondAdminJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND", "message": "location not found or wrong tenant"})
		return
	}

	respondAdminJSON(w, http.StatusOK, map[string]string{"arrival_pin": plainPin})
}
