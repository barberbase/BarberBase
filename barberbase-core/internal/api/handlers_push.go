package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/push"
	"barberbase-core/internal/realtime"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"
)

// Declare a package-level sync.Map for push rate limiters.
// Key: string (staff_member_id as UUID string) → *rate.Limiter
var pushRateLimiters sync.Map

// getPushRateLimiter returns the rate.Limiter for the given staff member ID.
func getPushRateLimiter(staffMemberID string) *rate.Limiter {
	v, _ := pushRateLimiters.LoadOrStore(
		staffMemberID,
		rate.NewLimiter(rate.Every(3*time.Second), 1),
	)
	return v.(*rate.Limiter)
}

// SubscribePush implements operationId: subscribePush (POST /v1/staff/push/subscribe)
func (s *Server) SubscribePush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract staffMemberID (UUID) from JWT context.
	staffMemberIDStr := auth.StaffMemberIDFromCtx(ctx)
	staffMemberID, err := uuid.Parse(staffMemberIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member id claim",
		})
		return
	}

	// 2. Decode request body
	var body SubscribePushJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "invalid request body",
		})
		return
	}

	// All three are required; missing any -> 400.
	if body.Endpoint == "" || body.P256dh == "" || body.Auth == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "endpoint, p256dh, and auth are all required",
		})
		return
	}

	// 3. Execute database UPDATE
	result, err := s.Pool.Exec(ctx, `
		UPDATE staff_members
		SET push_endpoint = $1,
		    push_p256dh   = $2,
		    push_auth     = $3,
		    push_enabled  = true,
		    updated_at    = NOW()
		WHERE id = $4
		  AND is_active = true`,
		body.Endpoint, body.P256dh, body.Auth, staffMemberID,
	)
	if err != nil {
		log.Printf("[Error] SubscribePush update failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// 4. Check RowsAffected()
	if result.RowsAffected() == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{
			"code":    "NOT_FOUND",
			"message": "staff member not found or inactive",
		})
		return
	}

	w.WriteHeader(http.StatusNoContent) // 204
}

// PushCallNext implements operationId: pushCallNext (POST /v1/staff/push/call-next)
func (s *Server) PushCallNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Step 1 — Extract token
	token := r.Header.Get("X-Push-Action-Token")
	if token == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "missing X-Push-Action-Token header",
		})
		return
	}

	// Step 2 — Verify PAT
	hmacSecret := []byte(s.Config.HMACSecret)
	claims, err := push.VerifyPAT(hmacSecret, token)
	if err != nil {
		if errors.Is(err, push.ErrWrongCommand) {
			respondJSON(w, http.StatusForbidden, map[string]string{
				"code":    "FORBIDDEN",
				"message": "wrong command scope",
			})
			return
		}
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": err.Error(),
		})
		return
	}

	staffMemberID, err := uuid.Parse(claims.StaffMemberID)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid staff member ID in token",
		})
		return
	}

	locationID, err := uuid.Parse(claims.LocationID)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid location ID in token",
		})
		return
	}

	// Step 3 — Rate limit
	limiter := getPushRateLimiter(claims.StaffMemberID)
	if !limiter.Allow() {
		w.WriteHeader(http.StatusTooManyRequests) // 429
		return
	}

	// Step 4 — Resolve tenant
	var tenantID uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		SELECT tenant_id
		FROM staff_members
		WHERE id = $1
		  AND is_active = true`,
		staffMemberID,
	).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"code":    "UNAUTHORIZED",
				"message": "staff member deactivated or not found",
			})
			return
		}
		log.Printf("[Error] Resolve tenant ID failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Step 5 — Call the shared call-next domain function
	output, err := queue.CallNext(ctx, s.Pool, queue.CallNextParams{
		TenantID:      tenantID,
		LocationID:    locationID,
		StaffMemberID: staffMemberID,
	})

	if err != nil {
		var noDispErr queue.ErrNoDispatchable
		if errors.As(err, &noDispErr) {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"code":                 "NO_DISPATCHABLE_CUSTOMERS",
				"message":              "No arrived dispatchable customers at this location",
				"error":                "no_dispatchable_customers",
				"waiting_remote_count": noDispErr.WaitingRemoteCount,
			})
			return
		}
		if errors.Is(err, queue.ErrSessionNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]any{
				"code":                 "NO_ACTIVE_SESSION",
				"message":              "No active queue session for today",
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
		log.Printf("[Error] PushCallNext CallNext failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal",
		})
		return
	}

	// Broadcast SSE (after transaction commit, Law 8)
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: output.QueueVersion,
		})
	}

	// Step 6 — Build response
	var waitingArrivedCount int
	errQuery := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) AS waiting_arrived_count
		FROM queue_entries qe
		JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		WHERE qs.location_id = $1
		  AND qs.business_date = CURRENT_DATE
		  AND qe.state = 'waiting'
		  AND qe.is_dispatchable = true
		  AND qe.presence_state = 'arrived'`,
		locationID,
	).Scan(&waitingArrivedCount)
	if errQuery != nil {
		log.Printf("[Error] Query waiting arrived count failed: %v", errQuery)
		waitingArrivedCount = 0
	}

	estimatedWaitMinutes := waitingArrivedCount * 20

	respondJSON(w, http.StatusOK, map[string]any{
		"called_entry":           toQueueEntryStaffJSON(output.Entry),
		"waiting_arrived_count":   waitingArrivedCount,
		"estimated_wait_minutes": estimatedWaitMinutes,
	})
}
