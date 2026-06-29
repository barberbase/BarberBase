package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/domain/presence"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/realtime"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"golang.org/x/crypto/bcrypt"
)

// RequestStaffOTP handles POST /auth/staff/request-otp
func (s *Server) RequestStaffOTP(w http.ResponseWriter, r *http.Request) {
	var body RequestStaffOTPJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	phone := body.PhoneNumber
	if phone == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "phone_number is required",
		})
		return
	}

	// 1. Rate Limiting: Max 3 requests per 10 minutes.
	// AllowOTPRequest returns false if the limit is exceeded.
	if !auth.AllowOTPRequest(phone) {
		respondJSON(w, http.StatusTooManyRequests, map[string]string{
			"code":    "RATE_LIMITED",
			"message": "Too many OTP requests. Try again later.",
		})
		return
	}

	ctx := r.Context()

	// 2. Select active staff member
	var staffID uuid.UUID
	var tenantID uuid.UUID
	var locationID uuid.UUID
	err := s.Pool.QueryRow(ctx,
		"SELECT id, tenant_id, location_id FROM staff_members WHERE phone_number=$1 AND is_active=true LIMIT 1",
		phone,
	).Scan(&staffID, &tenantID, &locationID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Do NOT leak whether the phone number is missing versus inactive
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"code":    "UNAUTHORIZED",
				"message": "invalid or expired OTP",
			})
			return
		}
		log.Printf("[Error] database query failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 3. Generate 6-digit OTP code using crypto/rand
	nBig, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		log.Printf("[Error] failed to generate secure OTP: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}
	otpCode := fmt.Sprintf("%06d", nBig.Int64())

	// 4. Hash OTP with bcrypt (cost=10)
	hash, err := bcrypt.GenerateFromPassword([]byte(otpCode), 10)
	if err != nil {
		log.Printf("[Error] bcrypt hash failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 5. Database Transaction to delete prior and insert new OTP
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		log.Printf("[Error] transaction begin failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "DELETE FROM staff_otps WHERE phone_number=$1", phone)
	if err != nil {
		log.Printf("[Error] failed to delete prior OTPs: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	var otpID uuid.UUID
	err = tx.QueryRow(ctx,
		"INSERT INTO staff_otps(phone_number, otp_hash, expires_at) VALUES($1, $2, NOW()+INTERVAL '5 minutes') RETURNING id",
		phone, string(hash),
	).Scan(&otpID)
	if err != nil {
		log.Printf("[Error] failed to insert OTP: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[Error] transaction commit failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 6. Call Bhejna client to send bb_staff_otp template
	bhejnaReq := bhejna.SendTemplateReq{
		To:           phone,
		TemplateCode: "bb_staff_otp",
		Language:     "en",
		Components: []bhejna.TemplateComponent{
			{
				Type: "body",
				Parameters: []bhejna.TemplateParameter{
					{
						Type: "text",
						Text: otpCode,
					},
				},
			},
			{
				Type:    "button",
				SubType: "url",
				Index:   0,
				Parameters: []bhejna.TemplateParameter{
					{
						Type: "text",
						Text: otpCode,
					},
				},
			},
		},
		// Idempotency key scoped to OTP ID
		IdempotencyKey: fmt.Sprintf("barberbase:otp:%s", otpID.String()),
	}

	_, err = s.Bhejna.SendTemplate(ctx, uuid.Nil, uuid.Nil, bhejnaReq)
	if err != nil {
		// Log but return 200 anyway. Do NOT rollback.
		log.Printf("[Warning] Bhejna delivery failed: %v", err)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":            "OTP sent to your WhatsApp",
		"expires_in_seconds": 300,
	})
}

// VerifyStaffOTP handles POST /auth/staff/verify-otp
func (s *Server) VerifyStaffOTP(w http.ResponseWriter, r *http.Request) {
	var body VerifyStaffOTPJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	phone := body.PhoneNumber
	otp := body.Otp

	if phone == "" || otp == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "phone_number and otp are required",
		})
		return
	}

	ctx := r.Context()

	// Verify OTP inside single transaction
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		log.Printf("[Error] transaction begin failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}
	defer tx.Rollback(ctx)

	var otpID uuid.UUID
	var otpHash string
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT id, otp_hash, attempts FROM staff_otps 
		 WHERE phone_number=$1 AND consumed_at IS NULL AND expires_at > NOW() 
		 ORDER BY created_at DESC LIMIT 1 FOR UPDATE`,
		phone,
	).Scan(&otpID, &otpHash, &attempts)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"code":    "UNAUTHORIZED",
				"message": "invalid or expired OTP",
			})
			return
		}
		log.Printf("[Error] query row FOR UPDATE failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	if attempts >= 5 {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid or expired OTP",
		})
		return
	}

	// Increment attempts
	_, err = tx.Exec(ctx, "UPDATE staff_otps SET attempts=attempts+1 WHERE id=$1", otpID)
	if err != nil {
		log.Printf("[Error] failed to increment attempts: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Verify bcrypt hash
	bcryptErr := bcrypt.CompareHashAndPassword([]byte(otpHash), []byte(otp))
	if bcryptErr != nil {
		// Commit the attempt increment and fail with 401
		if err := tx.Commit(ctx); err != nil {
			log.Printf("[Error] failed to commit incremented attempts: %v", err)
		}
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid or expired OTP",
		})
		return
	}

	// Mark consumed
	_, err = tx.Exec(ctx, "UPDATE staff_otps SET consumed_at=NOW() WHERE id=$1", otpID)
	if err != nil {
		log.Printf("[Error] failed to set consumed_at: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Check if active staff member exists
	var staffID uuid.UUID
	var tenantID uuid.UUID
	var locationID uuid.UUID
	var role string
	var name string
	err = tx.QueryRow(ctx,
		"SELECT id, tenant_id, location_id, role, name FROM staff_members WHERE phone_number=$1 AND is_active=true LIMIT 1",
		phone,
	).Scan(&staffID, &tenantID, &locationID, &role, &name)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"code":    "UNAUTHORIZED",
				"message": "invalid or expired OTP",
			})
			return
		}
		log.Printf("[Error] failed to fetch staff: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[Error] transaction commit failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Issue Access + Refresh JWT tokens
	secret := []byte(s.Config.JWTSecret)
	accessToken, refreshToken, err := auth.GenerateAccessAndRefreshTokens(secret, tenantID.String(), locationID.String(), staffID.String(), role)
	if err != nil {
		log.Printf("[Error] failed to generate tokens: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	streamToken, err := auth.GenerateStreamToken(secret, tenantID.String(), locationID.String(), staffID.String(), role)
	if err != nil {
		log.Printf("[Error] failed to generate stream token: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Set Cookies
	http.SetCookie(w, &http.Cookie{
		Name:     "bb_access",
		Value:    accessToken,
		Path:     "/",
		MaxAge:   900,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "bb_refresh",
		Value:    refreshToken,
		Path:     "/v1/auth/staff/refresh",
		MaxAge:   2592000,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	respondJSON(w, http.StatusOK, map[string]string{
		"staff_member_id": staffID.String(),
		"name":            name,
		"role":            role,
		"location_id":     locationID.String(),
		"tenant_id":       tenantID.String(),
		"access_token":    accessToken,
		"refresh_token":   refreshToken,
		"stream_token":    streamToken,
	})
}

// RefreshStaffToken handles POST /auth/staff/refresh
func (s *Server) RefreshStaffToken(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("bb_refresh")
	if err != nil {
		respondUnauthorized(w)
		return
	}

	secret := []byte(s.Config.JWTSecret)
	claims, err := auth.ParseAndVerifyRefreshToken(cookie.Value, secret)
	if err != nil {
		respondUnauthorized(w)
		return
	}

	staffID := claims.Subject

	ctx := r.Context()
	var tenantID uuid.UUID
	var locationID uuid.UUID
	var role string
	err = s.Pool.QueryRow(ctx,
		"SELECT tenant_id, location_id, role FROM staff_members WHERE id=$1 AND is_active=true",
		staffID,
	).Scan(&tenantID, &locationID, &role)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondUnauthorized(w)
			return
		}
		log.Printf("[Error] query staff during refresh failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Generate only new access token
	accessToken, _, err := auth.GenerateAccessAndRefreshTokens(secret, tenantID.String(), locationID.String(), staffID, role)
	if err != nil {
		log.Printf("[Error] failed to generate access token on refresh: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	streamToken, err := auth.GenerateStreamToken(secret, tenantID.String(), locationID.String(), staffID, role)
	if err != nil {
		log.Printf("[Error] failed to generate stream token on refresh: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "bb_access",
		Value:    accessToken,
		Path:     "/",
		MaxAge:   900,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	respondJSON(w, http.StatusOK, map[string]string{
		"stream_token": streamToken,
	})
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"unauthorized"}`))
}

// ---------------------------------------------------------------------------
// STUBS / PLACEHOLDERS FOR REMAINING STAFF / QUEUE ENDPOINTS
// ---------------------------------------------------------------------------

func (s *Server) GetDailyAnalytics(w http.ResponseWriter, r *http.Request, params GetDailyAnalyticsParams) {
	ctx := r.Context()

	// 1. Extract role from JWT context -> if role is 'barber', return 403.
	role := auth.RoleFromCtx(ctx)
	if role == "barber" {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"code":    "FORBIDDEN",
			"message": "barber role is not allowed to access daily analytics",
		})
		return
	}

	// 2. Extract tenant_id and location_id from JWT context (never from query string — Law 11).
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	// 3. Fetch location timezone
	var tz string
	err = s.Pool.QueryRow(ctx, "SELECT timezone FROM locations WHERE id = $1 AND is_active = true", locationID).Scan(&tz)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": "location not found",
			})
			return
		}
		log.Printf("[Error] Fetching location timezone failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 4. Parse ?date param if present (format 2006-01-02). Invalid format -> 400. If absent, default to today in location timezone.
	var businessDate time.Time
	if dateStr := r.URL.Query().Get("date"); dateStr != "" {
		parsedDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{
				"code":    "INVALID_REQUEST",
				"message": "invalid date format, must be YYYY-MM-DD",
			})
			return
		}
		businessDate = parsedDate
	} else {
		loc, errLoc := time.LoadLocation(tz)
		if errLoc != nil {
			loc = time.UTC
		}
		nowInTz := time.Now().In(loc)
		businessDate = time.Date(nowInTz.Year(), nowInTz.Month(), nowInTz.Day(), 0, 0, 0, 0, nowInTz.Location())
	}

	// 5. Call r.Visit.GetDailyAnalytics(ctx, locationID, tenantID, businessDate)
	rRepo := &repository.VisitRepository{Pool: s.Pool}
	res, err := rRepo.GetDailyAnalytics(ctx, locationID, tenantID, businessDate)
	if err != nil {
		log.Printf("[Error] GetDailyAnalytics failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 6. Map DailyAnalyticsResult to the DailyAnalytics OpenAPI schema
	var apiBreakdown []struct {
		AverageServiceMinutes *int `json:"average_service_minutes,omitempty"`

		// BarberId UUID v7 (timestamp-sortable)
		BarberId   *UUIDv7 `json:"barber_id,omitempty"`
		BarberName *string `json:"barber_name,omitempty"`

		// RevenuePaise Monetary amount in paise (100 paise = 1 INR)
		RevenuePaise    *Paise `json:"revenue_paise,omitempty"`
		VisitsCompleted *int   `json:"visits_completed,omitempty"`
	}

	for _, ba := range res.BarberBreakdown {
		barberID := UUIDv7(ba.BarberID)
		barberName := ba.BarberName
		revenuePaise := Paise(ba.RevenuePaise)
		visitsCompleted := ba.VisitsCompleted

		apiBreakdown = append(apiBreakdown, struct {
			AverageServiceMinutes *int `json:"average_service_minutes,omitempty"`

			// BarberId UUID v7 (timestamp-sortable)
			BarberId   *UUIDv7 `json:"barber_id,omitempty"`
			BarberName *string `json:"barber_name,omitempty"`

			// RevenuePaise Monetary amount in paise (100 paise = 1 INR)
			RevenuePaise    *Paise `json:"revenue_paise,omitempty"`
			VisitsCompleted *int   `json:"visits_completed,omitempty"`
		}{
			AverageServiceMinutes: ba.AverageServiceMinutes,
			BarberId:              &barberID,
			BarberName:            &barberName,
			RevenuePaise:          &revenuePaise,
			VisitsCompleted:       &visitsCompleted,
		})
	}

	if apiBreakdown == nil {
		apiBreakdown = make([]struct {
			AverageServiceMinutes *int `json:"average_service_minutes,omitempty"`

			// BarberId UUID v7 (timestamp-sortable)
			BarberId   *UUIDv7 `json:"barber_id,omitempty"`
			BarberName *string `json:"barber_name,omitempty"`

			// RevenuePaise Monetary amount in paise (100 paise = 1 INR)
			RevenuePaise    *Paise `json:"revenue_paise,omitempty"`
			VisitsCompleted *int   `json:"visits_completed,omitempty"`
		}, 0)
	}

	noShowVal := res.NoShowCount
	cancelledVal := res.CancelledCount

	resp := DailyAnalytics{
		AverageWaitMinutes: res.AverageWaitMinutes,
		BarberBreakdown:    apiBreakdown,
		BusinessDate:       openapi_types.Date{Time: businessDate},
		CancelledCount:     &cancelledVal,
		NoShowCount:        &noShowVal,
		TotalRevenuePaise:  Paise(res.TotalRevenuePaise),
		TotalVisits:        res.TotalVisits,
	}

	// 7. Return 200
	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) GetStaffMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromCtx(ctx)
	locationID := auth.LocationIDFromCtx(ctx)

	if tenantID == "" || locationID == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "UNAUTHORIZED", "message": "unauthorized",
		})
		return
	}

	members, err := repository.ListStaffMembers(ctx, s.Pool, tenantID, locationID)
	if err != nil {
		log.Printf("[Error] ListStaffMembers failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	// Map to OpenAPI response shape
	staffList := make([]StaffMember, 0, len(members))
	for _, row := range members {
		id, err := uuid.Parse(row.ID)
		if err != nil {
			log.Printf("[Warn] skipping staff row with bad id %q: %v", row.ID, err)
			continue
		}
		m := StaffMember{
			Id:     id,
			Name:   row.Name,
			Role:   StaffMemberRole(row.Role),
			Status: StaffMemberStatus(row.Status),
		}
		if row.CurrentEntryID != nil {
			if eid, err := uuid.Parse(*row.CurrentEntryID); err == nil {
				m.CurrentEntryId = &eid
			}
			// bad entry id: leave CurrentEntryId nil rather than failing the row
		}
		staffList = append(staffList, m)
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"staff": staffList})
}

func (s *Server) UpdateBarberStatus(w http.ResponseWriter, r *http.Request, staffId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}


func (s *Server) AddWalkIn(w http.ResponseWriter, r *http.Request) {
	// addWalkInBody extends generated AddWalkInJSONBody with optional idempotency_key for dedup
	var body struct {
		AddWalkInJSONBody
		IdempotencyKey *string `json:"idempotency_key,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_BODY",
			"message": "failed to decode request body",
		})
		return
	}
	if len(body.VariantIds) == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_VARIANTS",
			"message": "at least one variant_id is required",
		})
		return
	}

	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant id claim"})
		return
	}
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid location id claim"})
		return
	}

	var maxQueueSize int
	err = s.Pool.QueryRow(ctx, `
		SELECT max_total_queue_size FROM locations WHERE id = $1 AND tenant_id = $2
	`, locationID, tenantID).Scan(&maxQueueSize)
	if errors.Is(err, pgx.ErrNoRows) {
		respondJSON(w, http.StatusNotFound, map[string]string{"code": "LOCATION_NOT_FOUND", "message": "location not found"})
		return
	}
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "failed to resolve location"})
		return
	}

	// ponytail: server-side key if frontend omits; two sequential taps = two entries (correct behavior)
	idempotencyKey := uuid.New().String()
	if body.IdempotencyKey != nil && *body.IdempotencyKey != "" {
		idempotencyKey = *body.IdempotencyKey
	}

	variantIDs := make([]uuid.UUID, len(body.VariantIds))
	for i, v := range body.VariantIds {
		variantIDs[i] = uuid.UUID(v)
	}

	var normPhone *string
	if body.PhoneNumber != nil && string(*body.PhoneNumber) != "" {
		p := repository.NormalizeE164(string(*body.PhoneNumber))
		normPhone = &p
	}

	partySize := 1
	if body.PartySize != nil {
		partySize = *body.PartySize
	}

	var requestedBarberID *uuid.UUID
	if body.RequestedBarberId != nil {
		bid := uuid.UUID(*body.RequestedBarberId)
		requestedBarberID = &bid
	}

	var result *queue.JoinQueueResult
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		result, err = queue.JoinQueue(ctx, tx, queue.JoinQueueParams{
			TenantID:          tenantID,
			LocationID:        locationID,
			VariantIDs:        variantIDs,
			PartySize:         partySize,
			CustomerName:      body.CustomerName,
			PhoneNumber:       normPhone,
			RequestedBarberID: requestedBarberID,
			IdempotencyKey:    idempotencyKey,
			InitiatedVia:      "staff_dashboard",
			MaxQueueSize:      maxQueueSize,
			HMACSecret:        []byte(s.Config.HMACSecret),
			BhejnaFromPhone:   s.Config.BhejnaFromPhone,
		})
		return err
	})

	if errTx != nil {
		var alreadyErr *queue.ErrAlreadyInQueue
		if errors.As(errTx, &alreadyErr) {
			respondJSON(w, http.StatusConflict, map[string]string{
				"code":    "ALREADY_IN_QUEUE",
				"message": "customer already has an active entry in the queue",
			})
			return
		}
		if errors.Is(errTx, queue.ErrQueueFull) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "QUEUE_FULL",
				"message": "queue is at capacity",
			})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidVariants) || errors.Is(errTx, queue.ErrInactiveVariant) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "INVALID_VARIANTS",
				"message": errTx.Error(),
			})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "LOCK_TIMEOUT",
				"message": "lock timeout, retry",
			})
			return
		}
		log.Printf("[Error] AddWalkIn failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	entryView, err := repository.GetEntryStaffView(ctx, s.Pool, result.QueueEntryID)
	if err != nil {
		log.Printf("[Error] AddWalkIn: failed to fetch entry view: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Law 8: SSE after commit
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: result.NewQueueVersion,
		})
	}

	respondJSON(w, http.StatusCreated, toQueueEntryStaffJSON(entryView))
}

func (s *Server) CallNextCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	staffMemberIDStr := auth.StaffMemberIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	staffMemberID, err := uuid.Parse(staffMemberIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member id claim",
		})
		return
	}

	// 2. Call queue.CallNext
	output, err := queue.CallNext(ctx, s.Pool, queue.CallNextParams{
		TenantID:        tenantID,
		LocationID:      locationID,
		StaffMemberID:   staffMemberID,
		BhejnaFromPhone: s.Config.BhejnaFromPhone,
		HMACSecret:      []byte(s.Config.HMACSecret),
	})

	// 3. Switch on error
	if err != nil {
		var noDispErr queue.ErrNoDispatchable
		if errors.As(err, &noDispErr) {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"error":                "no_dispatchable_customers",
				"waiting_remote_count": noDispErr.WaitingRemoteCount,
			})
			return
		}
		if errors.Is(err, queue.ErrSessionNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"error":                "no_active_session",
				"waiting_remote_count": 0,
			})
			return
		}
		if errors.Is(err, queue.ErrLockTimeout) {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "lock_timeout_retry",
			})
			return
		}
		// Generic internal error
		log.Printf("[Error] CallNext failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal",
		})
		return
	}

	// Nil-error path: Broadcast SSE (after transaction commit, Law 8)
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: output.QueueVersion,
		})
	}

	// Return 200 with mapped QueueEntryStaff
	respondJSON(w, http.StatusOK, toQueueEntryStaffJSON(output.Entry))
}

func toQueueEntryStaffJSON(row *repository.QueueEntryStaffRow) QueueEntryStaff {
	if row == nil {
		return QueueEntryStaff{}
	}

	var services []struct {
		DurationMinutes *int    `json:"duration_minutes,omitempty"`
		Name            *string `json:"name,omitempty"`
		PricePaise      *Paise  `json:"price_paise,omitempty"`
	}

	for _, s := range row.Services {
		d := s.DurationMinutes
		n := s.Name
		p := s.PricePaise
		services = append(services, struct {
			DurationMinutes *int    `json:"duration_minutes,omitempty"`
			Name            *string `json:"name,omitempty"`
			PricePaise      *Paise  `json:"price_paise,omitempty"`
		}{
			DurationMinutes: &d,
			Name:            &n,
			PricePaise:      &p,
		})
	}

	var notes *[]string
	if len(row.CustomerNotes) > 0 {
		notesCopy := make([]string, len(row.CustomerNotes))
		copy(notesCopy, row.CustomerNotes)
		notes = &notesCopy
	}

	var res QueueEntryStaff
	res.Id = row.ID
	res.TokenNumber = row.TokenNumber
	res.State = QueueEntryStaffState(row.State)
	res.PresenceState = QueueEntryStaffPresenceState(row.PresenceState)
	res.IsDispatchable = row.IsDispatchable
	res.TotalDurationMinutes = row.TotalDurationMinutes
	res.PartySize = row.PartySize
	res.RequestedBarberId = row.RequestedBarberID
	res.AssignedBarberId = row.AssignedBarberID
	res.JoinedAt = row.JoinedAt
	res.CalledAt = row.CalledAt
	res.StartedAt = row.StartedAt
	res.StaleWarning = row.StaleWarning
	res.Services = services

	res.Customer.Id = row.CustomerID
	res.Customer.Name = row.CustomerName
	res.Customer.PhoneMasked = row.CustomerPhone
	res.Customer.VisitCount = row.CustomerVisitCount
	res.Customer.Notes = notes

	return res
}

func (s *Server) CompleteService(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()

	// Extract claims
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	staffMemberIDStr := auth.StaffMemberIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	staffMemberID, err := uuid.Parse(staffMemberIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member id claim",
		})
		return
	}

	// Decode body
	var req CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	// Validate Entry ID
	if uuid.UUID(req.QueueEntryId) != uuid.UUID(entryId) {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code":    "ID_MISMATCH",
			"message": "entry id in path does not match body",
		})
		return
	}

	// Compute business date
	var tz string
	err = s.Pool.QueryRow(ctx, "SELECT timezone FROM locations WHERE id = $1 AND tenant_id = $2", locationID, tenantID).Scan(&tz)
	if err != nil {
		tz = "Asia/Kolkata"
	}
	loc, errLoc := time.LoadLocation(tz)
	if errLoc != nil {
		loc, _ = time.LoadLocation("Asia/Kolkata")
	}
	businessDate := time.Now().In(loc)

	// Map products
	var domainProducts []queue.CheckoutProductItem
	if req.ProductLineItems != nil {
		for _, p := range *req.ProductLineItems {
			domainProducts = append(domainProducts, queue.CheckoutProductItem{
				ProductID: uuid.UUID(p.ProductId),
				Quantity:  p.Quantity,
			})
		}
	}

	// Map payments
	var domainPayments []queue.CheckoutPaymentLine
	for _, p := range req.PaymentLines {
		domainPayments = append(domainPayments, queue.CheckoutPaymentLine{
			Method:              string(p.Method),
			AmountPaise:         int(p.AmountPaise),
			ProviderReferenceID: p.ProviderReferenceId,
		})
	}

	// Map discount
	var discount int
	if req.DiscountAmountPaise != nil {
		discount = int(*req.DiscountAmountPaise)
	}

	params := queue.CheckoutParams{
		EntryID:             uuid.UUID(entryId),
		TenantID:            tenantID,
		LocationID:          locationID,
		CallerStaffID:       staffMemberID,
		BusinessDate:        businessDate,
		DiscountAmountPaise: discount,
		DiscountReason:      req.DiscountReason,
		Products:            domainProducts,
		Payments:            domainPayments,
	}

	res, err := queue.CompleteVisitAndCheckout(ctx, s.Pool, params)
	if err != nil {
		if errors.Is(err, queue.ErrInvalidTransition) ||
			errors.Is(err, queue.ErrPaymentMismatch) ||
			errors.Is(err, queue.ErrInvalidDiscount) ||
			errors.Is(err, queue.ErrProductNotFound) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "VALIDATION_FAILED",
				"message": err.Error(),
			})
			return
		}
		if errors.Is(err, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": err.Error(),
			})
			return
		}
		log.Printf("[Error] CompleteVisitAndCheckout failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: res.NewQueueVersion,
		})
	}

	var subtotal *int
	if res.SubtotalPaise != 0 {
		v := res.SubtotalPaise
		subtotal = &v
	}
	var discountAmount *int
	if res.DiscountPaise != 0 {
		v := res.DiscountPaise
		discountAmount = &v
	}

	resp := CheckoutResponse{
		VisitId:             UUIDv7(res.VisitID),
		SubtotalAmountPaise: subtotal,
		DiscountAmountPaise: discountAmount,
		TotalAmountPaise:    res.TotalPaise,
		PaymentStatus:       CheckoutResponsePaymentStatus(res.PaymentStatus),
		FeedbackScheduled:   res.FeedbackScheduled,
	}

	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) MarkNoShow(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant id claim"})
		return
	}
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid location id claim"})
		return
	}
	entryID := uuid.UUID(entryId)

	var queueVersion int
	var alreadyNoShow bool
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		// Resolve session (tenant+location scoped)
		var sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT qs.id FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qe.id = $1 AND qs.tenant_id = $2 AND qs.location_id = $3
		`, entryID, tenantID, locationID).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return queue.ErrEntryNotFound
			}
			return fmt.Errorf("lookup session: %w", err)
		}
		// Law 1: session lock first
		if _, err := tx.Exec(ctx, `SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM queue_entries WHERE id = $1 FOR UPDATE`, entryID).Scan(&state); err != nil {
			return fmt.Errorf("lock entry: %w", err)
		}
		if state == "no_show" {
			alreadyNoShow = true
			return nil
		}
		if state != "called" {
			return queue.ErrInvalidStateTransition
		}
		if _, err := tx.Exec(ctx, `
			UPDATE queue_entries SET state = 'no_show', is_dispatchable = false WHERE id = $1
		`, entryID); err != nil {
			return fmt.Errorf("mark no_show: %w", err)
		}
		return tx.QueryRow(ctx, `
			UPDATE queue_sessions SET queue_version = queue_version+1 WHERE id = $1 RETURNING queue_version
		`, sessionID).Scan(&queueVersion)
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND", "message": "queue entry not found"})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidStateTransition) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_STATE_TRANSITION", "message": "entry must be in 'called' state"})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "LOCK_TIMEOUT", "message": "lock timeout, retry"})
			return
		}
		log.Printf("[Error] MarkNoShow failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	if alreadyNoShow {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Law 8
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: queueVersion,
		})
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) ReactivateEntry(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant id claim"})
		return
	}
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid location id claim"})
		return
	}
	entryID := uuid.UUID(entryId)

	var queueVersion int
	var alreadyActive bool
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT qs.id FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qe.id = $1 AND qs.tenant_id = $2 AND qs.location_id = $3
		`, entryID, tenantID, locationID).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return queue.ErrEntryNotFound
			}
			return fmt.Errorf("lookup session: %w", err)
		}
		// Law 1: session lock first
		if _, err := tx.Exec(ctx, `SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		var state, presenceState string
		if err := tx.QueryRow(ctx, `
			SELECT state, presence_state FROM queue_entries WHERE id = $1 FOR UPDATE
		`, entryID).Scan(&state, &presenceState); err != nil {
			return fmt.Errorf("lock entry: %w", err)
		}
		// Double-tap: already reactivated
		if state == "waiting" && presenceState == "arrived" {
			alreadyActive = true
			return nil
		}
		// Source: (waiting+snoozed) OR skipped
		if !((state == "waiting" && presenceState == "snoozed") || state == "skipped") {
			return queue.ErrInvalidStateTransition
		}

		// Read notify_when_people_ahead from location
		var N int
		if err := tx.QueryRow(ctx, `SELECT notify_when_people_ahead FROM locations WHERE id = $1`, locationID).Scan(&N); err != nil {
			return fmt.Errorf("read notify_when_people_ahead: %w", err)
		}

		// Fetch reactivated entry's duration for wait-minutes calculation below
		var reactivatedDurationMinutes int
		if err := tx.QueryRow(ctx, `
			SELECT v.total_duration_minutes FROM visits v
			JOIN queue_entries qe ON v.id = qe.visit_id
			WHERE qe.id = $1
		`, entryID).Scan(&reactivatedDurationMinutes); err != nil {
			return fmt.Errorf("read reactivated duration: %w", err)
		}

		// W-list: ordered dispatchable waiting entries excluding the entry being reactivated.
		// Extended to include visit data needed to build the bb_queue_delayed notification.
		type wEntry struct {
			id              uuid.UUID
			sortKey         int64
			priorityGroup   int
			tokenNumber     int
			presenceState   string
			phone           *string
			customerID      *uuid.UUID
			visitID         uuid.UUID
			magicLinkExpiry *time.Time
			durationMinutes int
		}
		rows, err := tx.Query(ctx, `
			SELECT qe.id, qe.sort_key, qe.priority_group, qe.token_number, qe.presence_state,
			       c.phone_number, qe.customer_id, v.id, v.magic_link_expires_at,
			       v.total_duration_minutes
			FROM queue_entries qe
			LEFT JOIN customers c ON c.id = qe.customer_id
			LEFT JOIN visits v ON v.id = qe.visit_id
			WHERE qe.queue_session_id = $1
			  AND qe.is_dispatchable = true AND qe.state = 'waiting'
			  AND qe.id != $2
			ORDER BY qe.priority_group ASC, qe.sort_key ASC, qe.token_number ASC
		`, sessionID, entryID)
		if err != nil {
			return fmt.Errorf("build W list: %w", err)
		}
		defer rows.Close()
		var W []wEntry
		for rows.Next() {
			var e wEntry
			if err := rows.Scan(&e.id, &e.sortKey, &e.priorityGroup, &e.tokenNumber, &e.presenceState,
				&e.phone, &e.customerID, &e.visitID, &e.magicLinkExpiry, &e.durationMinutes); err != nil {
				return err
			}
			W = append(W, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// Compute placement — sort_key stays in same unit as existing entries (MAX+1 is unit-agnostic)
		var newSortKey int64
		newPriorityGroup := 100
		var displaced *wEntry
		if len(W) <= N {
			// Back of queue: sort after last dispatchable entry, unit-agnostic
			if len(W) > 0 {
				newSortKey = W[len(W)-1].sortKey + 1
			} else {
				newSortKey = time.Now().Unix()
			}
		} else {
			t := W[N] // 0-indexed: position N+1 in the current W
			displaced = &t
			newSortKey = t.sortKey - 1
			newPriorityGroup = t.priorityGroup
		}

		// Reactivate entry
		if _, err := tx.Exec(ctx, `
			UPDATE queue_entries
			SET state = 'waiting', presence_state = 'arrived', is_dispatchable = true,
			    sort_key = $2, priority_group = $3, reactivated_at = NOW()
			WHERE id = $1
		`, entryID, newSortKey, newPriorityGroup); err != nil {
			return fmt.Errorf("reactivate entry: %w", err)
		}

		// Law 6: audit staff arrival
		if _, err := tx.Exec(ctx, `
			INSERT INTO arrival_attempts (tenant_id, location_id, queue_entry_id, method, success)
			VALUES ($1, $2, $3, 'staff', true)
		`, tenantID, locationID, entryID); err != nil {
			return fmt.Errorf("insert arrival attempt: %w", err)
		}

		// Notify displaced entry T (Law 7: inside tx)
		if displaced != nil && displaced.phone != nil &&
			(displaced.presenceState == "remote" || displaced.presenceState == "notified" || displaced.presenceState == "on_the_way") {

			var locationName string
			_ = tx.QueryRow(ctx, `SELECT name FROM locations WHERE id = $1`, locationID).Scan(&locationName)

			// new_estimated_wait_minutes = reactivated entry + W[0..N-1] (entries now ahead of displaced T)
			waitMinutes := reactivatedDurationMinutes
			for i := 0; i < N && i < len(W); i++ {
				waitMinutes += W[i].durationMinutes
			}

			type compParam struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			type bodyComp struct {
				Type       string      `json:"type"`
				Parameters []compParam `json:"parameters"`
			}
			type btnComp struct {
				Type       string      `json:"type"`
				SubType    string      `json:"sub_type"`
				Index      int         `json:"index"`
				Parameters []compParam `json:"parameters"`
			}
			components := []interface{}{
				bodyComp{
					Type: "body",
					Parameters: []compParam{
						{Type: "text", Text: locationName},                               // {{1}} shop_name
						{Type: "text", Text: fmt.Sprintf("%d", displaced.tokenNumber)},  // {{2}} token_number
						{Type: "text", Text: fmt.Sprintf("%d", waitMinutes)},            // {{3}} estimated_wait_minutes
					},
				},
			}
			// Add button only when we have data to regenerate the magic link.
			// A phone-reachable remote customer always has all three — if any is nil/zero,
			// something minted the entry wrong upstream; log it and send without the button.
			if displaced.customerID != nil && displaced.visitID != uuid.Nil && displaced.magicLinkExpiry != nil {
				mlToken := queue.GenerateMagicLinkToken(
					displaced.customerID.String(),
					locationID.String(),
					displaced.visitID.String(),
					*displaced.magicLinkExpiry,
					[]byte(s.Config.HMACSecret),
				)
				components = append(components, btnComp{
					Type:    "button",
					SubType: "url",
					Index:   0,
					Parameters: []compParam{{Type: "text", Text: mlToken}},
				})
			} else {
				log.Printf("[Warn] ReactivateEntry: phone-reachable displaced entry %s has no customer/visit data — bb_queue_delayed sent without button (data integrity check needed)", displaced.id)
			}

			notifPayload := map[string]interface{}{
				"template_code":     "bb_queue_delayed",
				"to":                *displaced.phone,
				"location_id":       locationID.String(),
				"language":          "en",
				"notification_type": "queue_delayed",
				"components":        components,
			}
			payloadBytes, err := json.Marshal(notifPayload)
			if err != nil {
				return fmt.Errorf("marshal delayed notification: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events (tenant_id, type, payload, process_after)
				VALUES ($1, 'notification.send', $2, NOW())
			`, tenantID, payloadBytes); err != nil {
				return fmt.Errorf("insert delayed notification outbox: %w", err)
			}
		}

		return tx.QueryRow(ctx, `
			UPDATE queue_sessions SET queue_version = queue_version+1 WHERE id = $1 RETURNING queue_version
		`, sessionID).Scan(&queueVersion)
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND", "message": "queue entry not found"})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidStateTransition) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "INVALID_STATE_TRANSITION",
				"message": "entry must be in (waiting+snoozed) or skipped state",
			})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "LOCK_TIMEOUT", "message": "lock timeout, retry"})
			return
		}
		log.Printf("[Error] ReactivateEntry failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	if alreadyActive {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Law 8
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: queueVersion,
		})
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) ReassignBarber(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	var body ReassignBarberJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_BODY", "message": "failed to decode request body"})
		return
	}
	newBarberID := uuid.UUID(body.NewBarberId)
	if newBarberID == uuid.Nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_BARBER", "message": "new_barber_id is required"})
		return
	}

	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant id claim"})
		return
	}
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid location id claim"})
		return
	}
	entryID := uuid.UUID(entryId)

	// Validate barber is active in this tenant+location (outside tx — read-only check)
	var barberExists bool
	err = s.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM staff_members WHERE id = $1 AND tenant_id = $2 AND location_id = $3 AND is_active = true)
	`, newBarberID, tenantID, locationID).Scan(&barberExists)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	if !barberExists {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_BARBER", "message": "barber not found or inactive in this location"})
		return
	}

	var queueVersion int
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT qs.id FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qe.id = $1 AND qs.tenant_id = $2 AND qs.location_id = $3
		`, entryID, tenantID, locationID).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return queue.ErrEntryNotFound
			}
			return fmt.Errorf("lookup session: %w", err)
		}
		// Law 1: session lock first
		if _, err := tx.Exec(ctx, `SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM queue_entries WHERE id = $1 FOR UPDATE`, entryID).Scan(&state); err != nil {
			return fmt.Errorf("lock entry: %w", err)
		}
		if state != "waiting" && state != "called" {
			return queue.ErrInvalidStateTransition
		}
		if _, err := tx.Exec(ctx, `
			UPDATE queue_entries SET assigned_barber_id = $2 WHERE id = $1
		`, entryID, newBarberID); err != nil {
			return fmt.Errorf("reassign barber: %w", err)
		}
		return tx.QueryRow(ctx, `
			UPDATE queue_sessions SET queue_version = queue_version+1 WHERE id = $1 RETURNING queue_version
		`, sessionID).Scan(&queueVersion)
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND", "message": "queue entry not found"})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidStateTransition) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_STATE_TRANSITION", "message": "entry must be in waiting or called state"})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "LOCK_TIMEOUT", "message": "lock timeout, retry"})
			return
		}
		log.Printf("[Error] ReassignBarber failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	// Law 8
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: queueVersion,
		})
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) SkipEntry(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid tenant id claim"})
		return
	}
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid location id claim"})
		return
	}
	entryID := uuid.UUID(entryId)

	var queueVersion int
	var alreadySkipped bool
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var sessionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT qs.id FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qe.id = $1 AND qs.tenant_id = $2 AND qs.location_id = $3
		`, entryID, tenantID, locationID).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return queue.ErrEntryNotFound
			}
			return fmt.Errorf("lookup session: %w", err)
		}
		// Law 1: session lock first
		if _, err := tx.Exec(ctx, `SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM queue_entries WHERE id = $1 FOR UPDATE`, entryID).Scan(&state); err != nil {
			return fmt.Errorf("lock entry: %w", err)
		}
		if state == "skipped" {
			alreadySkipped = true
			return nil
		}
		if state != "waiting" && state != "called" {
			return queue.ErrInvalidStateTransition
		}
		if _, err := tx.Exec(ctx, `
			UPDATE queue_entries SET state = 'skipped', is_dispatchable = false WHERE id = $1
		`, entryID); err != nil {
			return fmt.Errorf("skip entry: %w", err)
		}
		return tx.QueryRow(ctx, `
			UPDATE queue_sessions SET queue_version = queue_version+1 WHERE id = $1 RETURNING queue_version
		`, sessionID).Scan(&queueVersion)
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND", "message": "queue entry not found"})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidStateTransition) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "INVALID_STATE_TRANSITION", "message": "entry must be in waiting or called state"})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "LOCK_TIMEOUT", "message": "lock timeout, retry"})
			return
		}
		log.Printf("[Error] SkipEntry failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR", "message": "internal server error"})
		return
	}
	if alreadySkipped {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Law 8
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: queueVersion,
		})
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) StartService(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	staffMemberIDStr := auth.StaffMemberIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	barberID, err := uuid.Parse(staffMemberIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member id claim",
		})
		return
	}

	entryID := uuid.UUID(entryId)

	var result *queue.StartServiceResult
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var txErr error
		result, txErr = queue.StartService(ctx, tx, entryID, barberID, tenantID)
		return txErr
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrInvalidStateTransition) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "INVALID_STATE_TRANSITION",
				"message": errTx.Error(),
			})
			return
		}
		if errors.Is(errTx, queue.ErrDirectStartNotArrived) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "DIRECT_START_NOT_ARRIVED",
				"message": errTx.Error(),
			})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "LOCK_TIMEOUT",
				"message": "lock timeout, retry",
			})
			return
		}
		if errors.Is(errTx, queue.ErrEntryNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": "queue entry not found",
			})
			return
		}
		log.Printf("[Error] StartService failed: %v", errTx)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Fetch full QueueEntryStaff view using existing repository function
	entryView, err := repository.GetEntryStaffView(ctx, s.Pool, entryID)
	if err != nil {
		log.Printf("[Error] failed to fetch queue entry staff view: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Law 8: SSE broadcast fires AFTER COMMIT, never before
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: result.QueueVersion,
		})
	}

	respondJSON(w, http.StatusOK, toQueueEntryStaffJSON(entryView))
}

func maskPhone(phone string) string {
	if len(phone) < 4 {
		return phone
	}
	if len(phone) >= 10 {
		return phone[:len(phone)-10] + " XXXX XX" + phone[len(phone)-4:]
	}
	return strings.Repeat("X", len(phone)-4) + phone[len(phone)-4:]
}

func (s *Server) GetQueueSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	// 1. Query queue session
	var sessionID uuid.UUID
	var queueVersion int
	var businessDate time.Time
	var sessionStatus string

	err = s.Pool.QueryRow(ctx, `
		SELECT qs.id, qs.queue_version, qs.business_date, qs.status
		FROM queue_sessions qs
		JOIN locations l ON l.id = qs.location_id
		WHERE qs.location_id = $1
		  AND qs.tenant_id = $2
		  AND qs.business_date = (NOW() AT TIME ZONE l.timezone)::DATE
		  AND qs.status <> 'archived'`, locationID, tenantID).Scan(&sessionID, &queueVersion, &businessDate, &sessionStatus)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active session today: return 200 with empty snapshot
			var timezone string
			errTz := s.Pool.QueryRow(ctx, "SELECT timezone FROM locations WHERE id = $1 AND tenant_id = $2", locationID, tenantID).Scan(&timezone)
			if errTz != nil {
				timezone = "Asia/Kolkata"
			}
			loc, errLoc := time.LoadLocation(timezone)
			if errLoc != nil {
				loc = time.UTC
			}
			todayTime := time.Now().In(loc)

			respondJSON(w, http.StatusOK, QueueSnapshot{
				QueueSessionId: uuid.Nil,
				QueueVersion:   0,
				BusinessDate:   openapi_types.Date{Time: todayTime},
				SessionStatus:  "closed",
				Entries:        []QueueEntryStaff{},
			})
			return
		}

		log.Printf("[Error] GetQueueSnapshot select session failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 2. Query entries
	rows, err := s.Pool.Query(ctx, `
		SELECT
			qe.id,
			qe.token_number,
			qe.state,
			qe.presence_state,
			qe.is_dispatchable,
			qe.requested_barber_id,
			qe.assigned_barber_id,
			qe.remote_joined_at  AS joined_at,
			qe.called_at,
			qe.started_at,
			qe.stale_warning,
			v.id        AS visit_id,
			v.party_size,
			v.total_duration_minutes,
			c.id        AS customer_id,
			c.name      AS customer_name,
			c.phone_number AS customer_phone
		FROM queue_entries qe
		JOIN  visits    v  ON v.id  = qe.visit_id
		LEFT JOIN customers c ON c.id = qe.customer_id
							 AND c.merged_into_customer_id IS NULL
		WHERE qe.queue_session_id = $1
		  -- needs_review is NOT filtered here, but the session select above (status <> 'archived')
		  -- keeps it off the wire: needs_review is only ever written by end-of-day in the same tx
		  -- that archives the session (end_of_day.go:143,153). It is absent from the QueueEntryStaff
		  -- state enum (openapi.yaml). Before loosening that archived gate, add needs_review to the
		  -- enum and a dashboard reconciliation branch — otherwise it renders as an unhandled state.
		  AND qe.state NOT IN ('completed', 'cancelled', 'expired')
		ORDER BY
			CASE qe.state
				WHEN 'in_progress' THEN 1
				WHEN 'called'      THEN 2
				ELSE                    3
			END,
			qe.priority_group ASC,
			qe.sort_key       ASC`, sessionID)

	if err != nil {
		log.Printf("[Error] GetQueueSnapshot select entries failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}
	defer rows.Close()

	type scannedEntry struct {
		id                uuid.UUID
		tokenNumber       int
		state             string
		presenceState     string
		isDispatchable    bool
		requestedBarberID *uuid.UUID
		assignedBarberID  *uuid.UUID
		joinedAt          time.Time
		calledAt          *time.Time
		startedAt         *time.Time
		staleWarning      *string
		visitID           uuid.UUID
		partySize         int
		totalDuration     int
		customerID        *uuid.UUID
		customerName      *string
		customerPhone     *string
	}

	var scannedEntries []scannedEntry
	var visitIDs []uuid.UUID
	var customerIDs []uuid.UUID

	for rows.Next() {
		var se scannedEntry
		err = rows.Scan(
			&se.id,
			&se.tokenNumber,
			&se.state,
			&se.presenceState,
			&se.isDispatchable,
			&se.requestedBarberID,
			&se.assignedBarberID,
			&se.joinedAt,
			&se.calledAt,
			&se.startedAt,
			&se.staleWarning,
			&se.visitID,
			&se.partySize,
			&se.totalDuration,
			&se.customerID,
			&se.customerName,
			&se.customerPhone,
		)
		if err != nil {
			log.Printf("[Error] GetQueueSnapshot scan entry failed: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "INTERNAL_ERROR",
				"message": "internal server error",
			})
			return
		}
		scannedEntries = append(scannedEntries, se)
		visitIDs = append(visitIDs, se.visitID)
		if se.customerID != nil {
			customerIDs = append(customerIDs, *se.customerID)
		}
	}

	// 3. Batch fetch services
	servicesMap := make(map[uuid.UUID][]struct {
		DurationMinutes *int    `json:"duration_minutes,omitempty"`
		Name            *string `json:"name,omitempty"`
		PricePaise      *Paise  `json:"price_paise,omitempty"`
	})

	if len(visitIDs) > 0 {
		vsRows, err := s.Pool.Query(ctx, `
			SELECT
				vs.visit_id,
				vs.variant_name_snapshot  AS name,
				vs.duration_minutes_snapshot AS duration_minutes,
				vs.price_paise_snapshot   AS price_paise
			FROM visit_services vs
			WHERE vs.visit_id = ANY($1)
			ORDER BY vs.sort_order ASC`, visitIDs)
		if err != nil {
			log.Printf("[Error] GetQueueSnapshot select visit services failed: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "INTERNAL_ERROR",
				"message": "internal server error",
			})
			return
		}
		defer vsRows.Close()

		for vsRows.Next() {
			var visitID uuid.UUID
			var name string
			var duration int
			var price Paise
			if err := vsRows.Scan(&visitID, &name, &duration, &price); err != nil {
				log.Printf("[Error] GetQueueSnapshot scan visit service failed: %v", err)
				respondJSON(w, http.StatusInternalServerError, map[string]string{
					"code":    "INTERNAL_ERROR",
					"message": "internal server error",
				})
				return
			}
			n := name
			d := duration
			p := price
			servicesMap[visitID] = append(servicesMap[visitID], struct {
				DurationMinutes *int    `json:"duration_minutes,omitempty"`
				Name            *string `json:"name,omitempty"`
				PricePaise      *Paise  `json:"price_paise,omitempty"`
			}{
				DurationMinutes: &d,
				Name:            &n,
				PricePaise:      &p,
			})
		}
	}

	// 4. Batch fetch customer notes
	notesMap := make(map[uuid.UUID][]string)
	if len(customerIDs) > 0 {
		cnRows, err := s.Pool.Query(ctx, `
			SELECT cn.customer_id, cn.note
			FROM customer_notes cn
			WHERE cn.customer_id = ANY($1)
			  AND cn.deleted_at IS NULL
			  AND cn.visibility = 'staff'
			ORDER BY cn.created_at DESC`, customerIDs)
		if err != nil {
			log.Printf("[Error] GetQueueSnapshot select customer notes failed: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "INTERNAL_ERROR",
				"message": "internal server error",
			})
			return
		}
		defer cnRows.Close()

		for cnRows.Next() {
			var custID uuid.UUID
			var note string
			if err := cnRows.Scan(&custID, &note); err != nil {
				log.Printf("[Error] GetQueueSnapshot scan customer note failed: %v", err)
				respondJSON(w, http.StatusInternalServerError, map[string]string{
					"code":    "INTERNAL_ERROR",
					"message": "internal server error",
				})
				return
			}
			notesMap[custID] = append(notesMap[custID], note)
		}
	}

	// 5. Batch fetch per-location visit counts
	visitCountsMap := make(map[uuid.UUID]int)
	if len(customerIDs) > 0 {
		vcRows, err := s.Pool.Query(ctx, `
			SELECT
				v2.customer_id,
				COUNT(*) AS count
			FROM visits v2
			WHERE v2.customer_id = ANY($1)
			  AND v2.location_id = $2
			  AND v2.status = 'completed'
			GROUP BY v2.customer_id`, customerIDs, locationID)
		if err != nil {
			log.Printf("[Error] GetQueueSnapshot select visit counts failed: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "INTERNAL_ERROR",
				"message": "internal server error",
			})
			return
		}
		defer vcRows.Close()

		for vcRows.Next() {
			var custID uuid.UUID
			var count int
			if err := vcRows.Scan(&custID, &count); err != nil {
				log.Printf("[Error] GetQueueSnapshot scan visit count failed: %v", err)
				respondJSON(w, http.StatusInternalServerError, map[string]string{
					"code":    "INTERNAL_ERROR",
					"message": "internal server error",
				})
				return
			}
			visitCountsMap[custID] = count
		}
	}

	// 6. Assemble response
	entries := make([]QueueEntryStaff, 0, len(scannedEntries))
	for _, se := range scannedEntries {
		var qes QueueEntryStaff
		qes.Id = se.id
		qes.TokenNumber = se.tokenNumber
		qes.State = QueueEntryStaffState(se.state)
		qes.PresenceState = QueueEntryStaffPresenceState(se.presenceState)
		qes.IsDispatchable = se.isDispatchable
		qes.TotalDurationMinutes = se.totalDuration

		ps := se.partySize
		qes.PartySize = &ps

		qes.RequestedBarberId = se.requestedBarberID
		qes.AssignedBarberId = se.assignedBarberID
		qes.JoinedAt = se.joinedAt
		qes.CalledAt = se.calledAt
		qes.StartedAt = se.startedAt
		qes.StaleWarning = se.staleWarning

		// Services
		qes.Services = servicesMap[se.visitID]
		if qes.Services == nil {
			qes.Services = []struct {
				DurationMinutes *int    `json:"duration_minutes,omitempty"`
				Name            *string `json:"name,omitempty"`
				PricePaise      *Paise  `json:"price_paise,omitempty"`
			}{}
		}

		// Customer
		if se.customerID != nil {
			qes.Customer.Id = se.customerID
			qes.Customer.Name = se.customerName

			var phoneMaskedPtr *string
			if se.customerPhone != nil {
				m := maskPhone(*se.customerPhone)
				phoneMaskedPtr = &m
			}
			qes.Customer.PhoneMasked = phoneMaskedPtr

			vc := visitCountsMap[*se.customerID]
			qes.Customer.VisitCount = &vc

			notes := notesMap[*se.customerID]
			if notes == nil {
				notes = []string{}
			}
			qes.Customer.Notes = &notes
		} else {
			// Anonymous customer details
			qes.Customer.Id = nil
			qes.Customer.Name = se.customerName
			qes.Customer.PhoneMasked = nil
			vc := 0
			qes.Customer.VisitCount = &vc
			emptyNotes := []string{}
			qes.Customer.Notes = &emptyNotes
		}

		entries = append(entries, qes)
	}

	respondJSON(w, http.StatusOK, QueueSnapshot{
		QueueSessionId: sessionID,
		QueueVersion:   queueVersion,
		BusinessDate:   openapi_types.Date{Time: businessDate},
		SessionStatus:  QueueSnapshotSessionStatus(sessionStatus),
		Entries:        entries,
	})
}

func (s *Server) GetStaffShopStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	staffStatus, err := repository.GetStaffShopStatus(ctx, s.Pool, tenantID, locationID)
	if err != nil {
		log.Printf("[Error] GetStaffShopStatus failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "failed to fetch shop status",
		})
		return
	}

	loc, err := repository.GetLocationWithTenantSlug(ctx, s.Pool, locationIDStr)
	if err != nil || loc == nil {
		log.Printf("[Error] GetStaffShopStatus: slug lookup failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "failed to fetch location slugs",
		})
		return
	}

	resp := map[string]interface{}{
		"shop_status":            staffStatus.ShopStatus,
		"queue_session_status":   staffStatus.QueueSessionStatus,
		"manual_override_active": staffStatus.ManualOverrideActive,
		"tenant_slug":            loc.TenantSlug,
		"location_slug":          loc.Slug,
	}
	if staffStatus.OverrideExpiresAt != nil {
		resp["override_expires_at"] = staffStatus.OverrideExpiresAt.Format(time.RFC3339)
	}
	if staffStatus.ArrivalPin != nil {
		resp["arrival_pin"] = *staffStatus.ArrivalPin
	}

	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) SetShopStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)
	staffMemberIDStr := auth.StaffMemberIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	staffMemberID, err := uuid.Parse(staffMemberIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member id claim",
		})
		return
	}

	var req SetShopStatusJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresInMinutes != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresInMinutes) * time.Minute)
		expiresAt = &t
	}

	var modalAction *string
	if req.ModalAction != nil {
		ma := string(*req.ModalAction)
		modalAction = &ma
	}

	params := repository.SetShopStatusParams{
		TenantID:    tenantID,
		LocationID:  locationID,
		SetBy:       staffMemberID,
		Status:      string(req.Status),
		ExpiresAt:   expiresAt,
		Reason:      req.Reason,
		ModalAction: modalAction,
	}

	activeCount, err := repository.SetShopStatus(ctx, s.Pool, params)
	if err != nil {
		if errors.Is(err, repository.ErrActiveEntriesExist) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"code":               "ACTIVE_ENTRIES_EXIST",
				"message":            "cannot close shop with active entries without modal action",
				"active_entry_count": activeCount,
			})
			return
		}
		log.Printf("[Error] SetShopStatus failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "failed to update shop status",
		})
		return
	}

	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:       "shop_status_changed",
			LocationID: locationID.String(),
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) StaffConfirmArrival(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	ctx := r.Context()
	tenantIDStr := auth.TenantIDFromCtx(ctx)
	locationIDStr := auth.LocationIDFromCtx(ctx)

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid tenant id claim",
		})
		return
	}

	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location id claim",
		})
		return
	}

	entryID := uuid.UUID(entryId)

	errConfirm := s.Arrival.StaffConfirmArrival(ctx, entryID, tenantID, locationID)
	if errConfirm != nil {
		var arrErr *presence.ArrivalErr
		if errors.As(errConfirm, &arrErr) {
			respondJSON(w, arrErr.HTTPStatus, map[string]string{
				"code":    arrErr.Code,
				"message": arrErr.Message,
			})
			return
		}

		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": errConfirm.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func verifyCustomerSession(tokenStr string, secret []byte, expectedLocationID string) bool {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	macBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	h := hmac.New(sha256.New, secret)
	h.Write(payloadBytes)
	expectedMac := h.Sum(nil)
	if !hmac.Equal(macBytes, expectedMac) {
		return false
	}

	payloadStr := string(payloadBytes)
	fields := strings.Split(payloadStr, ":")
	if len(fields) != 4 {
		return false
	}

	locationID := fields[1]
	if !strings.EqualFold(locationID, expectedLocationID) {
		return false
	}

	expiresUnix, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiresUnix {
		return false
	}

	return true
}

func (s *Server) SubscribeToQueueStream(
	w http.ResponseWriter,
	r *http.Request,
	locationId UUIDv7,
	params SubscribeToQueueStreamParams,
) {
	token := params.Token

	authSucceeded := false

	// Attempt StaffJWT verification
	claims, err := auth.ParseAndVerifyToken(token, []byte(s.Config.JWTSecret))
	if err == nil {
		if strings.EqualFold(claims.LocationID, locationId.String()) {
			authSucceeded = true
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// If StaffJWT fails, attempt CustomerSession verification
	if !authSucceeded {
		if verifyCustomerSession(token, []byte(s.Config.HMACSecret), locationId.String()) {
			authSucceeded = true
		}
	}

	if !authSucceeded {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Set SSE response headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Flush immediately
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	// Subscribe
	ch := s.Manager.Subscribe(locationId.String())
	defer s.Manager.Unsubscribe(locationId.String(), ch)

	// Event loop
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
