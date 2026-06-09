package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"reflect"
	"barberbase-core/internal/auth"
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		},
		// Idempotency key scoped to OTP ID
		IdempotencyKey: fmt.Sprintf("barberbase:otp:%s", otpID.String()),
	}

	_, err = s.Bhejna.SendTemplate(ctx, tenantID, locationID, bhejnaReq)
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

	http.SetCookie(w, &http.Cookie{
		Name:     "bb_access",
		Value:    accessToken,
		Path:     "/",
		MaxAge:   900,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	w.WriteHeader(http.StatusOK)
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
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) GetStaffMembers(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) UpdateBarberStatus(w http.ResponseWriter, r *http.Request, staffId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) PushCallNext(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) SubscribePush(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) AddWalkIn(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
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
		TenantID:      tenantID,
		LocationID:    locationID,
		StaffMemberID: staffMemberID,
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
	realtimeVal := reflect.ValueOf(s).Elem().FieldByName("Realtime")
	if realtimeVal.IsValid() && !realtimeVal.IsNil() {
		method := realtimeVal.MethodByName("Broadcast")
		if method.IsValid() {
			method.Call([]reflect.Value{
				reflect.ValueOf(locationID),
				reflect.ValueOf(output.QueueVersion),
			})
		}
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
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) StaffConfirmArrival(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) MarkNoShow(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) ReactivateEntry(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) ReassignBarber(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) SkipEntry(w http.ResponseWriter, r *http.Request, entryId UUIDv7) {
	w.WriteHeader(http.StatusNotImplemented)
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
	realtimeVal := reflect.ValueOf(s).Elem().FieldByName("Realtime")
	if realtimeVal.IsValid() && !realtimeVal.IsNil() {
		method := realtimeVal.MethodByName("Broadcast")
		if method.IsValid() {
			method.Call([]reflect.Value{
				reflect.ValueOf(locationID),
				reflect.ValueOf(result.QueueVersion),
			})
		}
	}

	respondJSON(w, http.StatusOK, toQueueEntryStaffJSON(entryView))
}

func (s *Server) GetQueueSnapshot(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) GetStaffShopStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) SetShopStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}
