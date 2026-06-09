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
		SELECT id, queue_version, business_date, status
		FROM queue_sessions
		WHERE location_id = $1
		  AND tenant_id = $2
		  AND business_date = CURRENT_DATE
		  AND status <> 'archived'`, locationID, tenantID).Scan(&sessionID, &queueVersion, &businessDate, &sessionStatus)

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
	w.WriteHeader(http.StatusNotImplemented)
}

func (s *Server) SetShopStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
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
