package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

