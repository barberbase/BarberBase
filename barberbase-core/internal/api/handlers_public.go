package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/presence"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/repository"
	"barberbase-core/internal/webhook"
)

// package-level — one limiter per remote IP, 5 requests per minute
var checkinIntentLimiters sync.Map // key: IP string → *rate.Limiter

func getCheckinIntentLimiter(ip string) *rate.Limiter {
	v, _ := checkinIntentLimiters.LoadOrStore(
		ip,
		rate.NewLimiter(rate.Every(12*time.Second), 5), // 5 tokens, 1 token per 12s = 5/min
	)
	return v.(*rate.Limiter)
}

// computeShopStatus derives the effective shop status from override + hours.
// Priority: active override > scheduled hours.
// Returns one of: "open", "closing_soon", "temporarily_closed", "closed".
func computeShopStatus(
	override *repository.LocationOverrideRow,
	hours    *repository.LocationHoursRow,
	now      time.Time,
) string {
	if override != nil {
		return override.Status
	}
	if hours == nil || !hours.IsOpen {
		return "closed"
	}
	if hours.OpensAt != nil {
		op := *hours.OpensAt
		opDate := time.Date(now.Year(), now.Month(), now.Day(), op.Hour(), op.Minute(), op.Second(), 0, now.Location())
		if now.Before(opDate) {
			return "closed"
		}
	}
	if hours.ClosesAt != nil {
		ca := *hours.ClosesAt
		caDate := time.Date(now.Year(), now.Month(), now.Day(), ca.Hour(), ca.Minute(), ca.Second(), 0, now.Location())
		if now.After(caDate) {
			return "closed"
		}
	}
	return "open"
}

// JoinQueue handles POST /v1/queue/join
func (s *Server) JoinQueue(w http.ResponseWriter, r *http.Request) {
	var req JoinQueueJSONBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_request_body",
			"message": "Failed to decode request body",
		})
		return
	}

	// Validation of required fields
	if req.LocationId == uuid.Nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_location_id",
			"message": "location_id is required",
		})
		return
	}
	if len(req.VariantIds) == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_variants",
			"message": "At least one variant ID must be selected",
		})
		return
	}
	if req.IdempotencyKey == uuid.Nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_idempotency_key",
			"message": "idempotency_key is required",
		})
		return
	}
	if req.InitiatedVia == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_initiated_via",
			"message": "initiated_via is required",
		})
		return
	}

	partySize := 1
	if req.PartySize != nil {
		partySize = *req.PartySize
	}
	if partySize < 1 || partySize > 10 {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "invalid_party_size",
			"message": "party_size must be between 1 and 10",
		})
		return
	}

	var normPhone *string
	if req.PhoneNumber != nil && *req.PhoneNumber != "" {
		p := repository.NormalizeE164(string(*req.PhoneNumber))
		normPhone = &p
	}

	ctx := r.Context()

	// ── SECTION 1 — TENANT RESOLUTION (public endpoint, no JWT) ──
	var tenantID uuid.UUID
	var maxTotalQueueSize int
	var queueRoutingMode string
	var isActive bool
	err := s.Pool.QueryRow(ctx, `
		SELECT tenant_id, max_total_queue_size, queue_routing_mode, is_active
		FROM locations
		WHERE id = $1 AND is_active = true`, req.LocationId).Scan(&tenantID, &maxTotalQueueSize, &queueRoutingMode, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "location_not_found",
				"message": "Location not found or inactive",
			})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "internal_error",
			"message": "Failed to resolve tenant",
		})
		return
	}

	// ── SECTION 2 — FULL TRANSACTION ──
	var result *queue.JoinQueueResult
	errTx := repository.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		result, err = queue.JoinQueue(ctx, tx, queue.JoinQueueParams{
			TenantID:          tenantID,
			LocationID:        req.LocationId,
			VariantIDs:        req.VariantIds,
			PartySize:         partySize,
			CustomerName:      req.CustomerName,
			PhoneNumber:       normPhone,
			BSUID:             req.Bsuid,
			RequestedBarberID: req.RequestedBarberId,
			IdempotencyKey:    req.IdempotencyKey.String(),
			InitiatedVia:      string(req.InitiatedVia),
			MaxQueueSize:      maxTotalQueueSize,
			HMACSecret:        []byte(s.Config.HMACSecret),
		})
		return err
	})

	if errTx != nil {
		if errors.Is(errTx, queue.ErrRequestInFlight) {
			respondJSON(w, http.StatusConflict, map[string]string{
				"code":    "request_in_flight",
				"message": "request in flight, retry",
			})
			return
		}
		if errors.Is(errTx, queue.ErrShopNotAccepting) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "shop_not_accepting",
				"message": "Shop is not accepting new customers",
			})
			return
		}
		if errors.Is(errTx, queue.ErrQueueFull) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "queue_full",
				"message": "Queue is at capacity",
			})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidVariants) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "invalid_variants",
				"message": "One or more variant IDs not found",
			})
			return
		}
		if errors.Is(errTx, queue.ErrInactiveVariant) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "inactive_variant",
				"message": "One or more variants are not available",
			})
			return
		}
		if errors.Is(errTx, queue.ErrInvalidBarber) {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "invalid_barber",
				"message": "Requested barber is not available",
			})
			return
		}

		var alreadyInQueueErr *queue.ErrAlreadyInQueue
		if errors.As(errTx, &alreadyInQueueErr) {
			existingEntry, errGet := s.getPublicQueueEntryByID(ctx, alreadyInQueueErr.ExistingEntryID)
			if errGet != nil {
				respondJSON(w, http.StatusConflict, map[string]interface{}{
					"code":    "already_in_queue",
					"message": "Customer already has an active entry in the queue",
				})
				return
			}
			respondJSON(w, http.StatusConflict, map[string]interface{}{
				"code":           "already_in_queue",
				"message":        "Customer already has an active entry in the queue",
				"existing_entry": existingEntry,
			})
			return
		}

		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "internal_error",
			"message": errTx.Error(),
		})
		return
	}

	if result.IsIdempotentReplay {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.StoredResponse)
		return
	}

	// Law 8: SSE broadcast fires AFTER COMMIT, never inside the transaction.
	realtimeVal := reflect.ValueOf(s).Elem().FieldByName("Realtime")
	if realtimeVal.IsValid() && !realtimeVal.IsNil() {
		method := realtimeVal.MethodByName("Broadcast")
		if method.IsValid() {
			method.Call([]reflect.Value{
				reflect.ValueOf(req.LocationId),
				reflect.ValueOf(result.NewQueueVersion),
			})
		}
	}

	// ── SECTION 3 — RESPONSE CONSTRUCTION ──
	publicEntry, err := s.getPublicQueueEntryByID(ctx, result.QueueEntryID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "internal_error",
			"message": "Failed to load public queue entry details",
		})
		return
	}

	response := map[string]interface{}{
		"queue_entry":      publicEntry,
		"magic_link_token": result.MagicLinkToken,
		"magic_link_url":   "https://barbers.app/q/status?t=" + result.MagicLinkToken,
		"whatsapp_sent":    result.WhatsAppSent,
	}

	respondJSON(w, http.StatusOK, response)
}

// normalizePublicState collapses needs_review (an end-of-day terminal DB state, schema
// 001_complete_schema.sql:777) to expired for the customer-facing QueueEntryPublic, whose
// state enum (openapi.yaml) omits needs_review — a customer should never see it. Staff DTOs
// deliberately keep needs_review: it is the signal that an in_progress entry awaits manual
// reconciliation. ponytail: only the public surface is normalized; widen only if the public enum drifts.
func normalizePublicState(state string) string {
	if state == "needs_review" {
		return "expired"
	}
	return state
}

func (s *Server) getPublicQueueEntryByID(ctx context.Context, entryID uuid.UUID) (*QueueEntryPublic, error) {
	query := `
		SELECT qe.id, qe.token_number, qe.state, qe.presence_state, v.party_size, v.total_duration_minutes,
		       v.magic_link_expires_at, loc.name AS shop_name, loc.name AS location_name, v.id AS visit_id
		FROM queue_entries qe
		JOIN visits v ON v.id = qe.visit_id
		JOIN locations loc ON loc.id = v.location_id
		WHERE qe.id = $1`

	var id, visitID uuid.UUID
	var tokenNumber, partySize, totalDuration int
	var state, presenceState string
	var magicLinkExpiresAt *time.Time
	var shopName, locationName string

	err := s.Pool.QueryRow(ctx, query, entryID).Scan(
		&id, &tokenNumber, &state, &presenceState, &partySize, &totalDuration,
		&magicLinkExpiresAt, &shopName, &locationName, &visitID,
	)
	if err != nil {
		return nil, err
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT variant_name_snapshot, duration_minutes_snapshot
		FROM visit_services
		WHERE visit_id = $1
		ORDER BY sort_order ASC`, visitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []struct {
		DurationMinutes *int    `json:"duration_minutes,omitempty"`
		Name            *string `json:"name,omitempty"`
	}
	for rows.Next() {
		var name string
		var duration int
		if err := rows.Scan(&name, &duration); err != nil {
			return nil, err
		}
		nameVal := name
		durationVal := duration
		services = append(services, struct {
			DurationMinutes *int    `json:"duration_minutes,omitempty"`
			Name            *string `json:"name,omitempty"`
		}{
			DurationMinutes: &durationVal,
			Name:            &nameVal,
		})
	}

	pState := QueueEntryPublicPresenceState(presenceState)
	sState := QueueEntryPublicState(normalizePublicState(state))

	return &QueueEntryPublic{
		Id:                   id,
		TokenNumber:          tokenNumber,
		State:                sState,
		PresenceState:        pState,
		PositionAhead:        0,
		EstimatedWaitMinutes: totalDuration,
		Services:             services,
		PartySize:            &partySize,
		MagicLinkExpiresAt:   magicLinkExpiresAt,
		ShopName:             &shopName,
		LocationName:         &locationName,
	}, nil
}

func (s *Server) GetMyQueueStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, _, visitID, err := s.resolveCustomerSession(ctx, r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED", "message": "invalid or expired session token"})
		return
	}

	var (
		id                 uuid.UUID
		tokenNumber        int
		state              string
		presenceState      string
		partySize          int
		magicLinkExpiresAt *time.Time
		shopName           string
		locationName       string
		queueSessionID     uuid.UUID
		priorityGroup      int
		sortKey            int64
		visitIDRow         uuid.UUID
	)
	err = s.Pool.QueryRow(ctx, `
		SELECT qe.id, qe.token_number, qe.state, qe.presence_state,
		       v.party_size, v.magic_link_expires_at,
		       loc.name AS shop_name, loc.name AS location_name,
		       qe.queue_session_id, qe.priority_group, qe.sort_key, v.id
		FROM queue_entries qe
		JOIN visits v ON v.id = qe.visit_id
		JOIN locations loc ON loc.id = v.location_id
		WHERE qe.visit_id = $1`, visitID).Scan(
		&id, &tokenNumber, &state, &presenceState,
		&partySize, &magicLinkExpiresAt,
		&shopName, &locationName,
		&queueSessionID, &priorityGroup, &sortKey, &visitIDRow,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR"})
		return
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT variant_name_snapshot, duration_minutes_snapshot
		FROM visit_services WHERE visit_id = $1 ORDER BY sort_order ASC`, visitIDRow)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var services []struct {
		DurationMinutes *int    `json:"duration_minutes,omitempty"`
		Name            *string `json:"name,omitempty"`
	}
	for rows.Next() {
		var name string
		var dur int
		if err := rows.Scan(&name, &dur); err != nil {
			continue
		}
		n, d := name, dur
		services = append(services, struct {
			DurationMinutes *int    `json:"duration_minutes,omitempty"`
			Name            *string `json:"name,omitempty"`
		}{DurationMinutes: &d, Name: &n})
	}
	rows.Close()

	// Count dispatchable entries ahead in the same session by dispatch order
	var positionAhead, estimatedWait int
	_ = s.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(v2.total_duration_minutes), 0)
		FROM queue_entries qe2
		JOIN visits v2 ON v2.id = qe2.visit_id
		WHERE qe2.queue_session_id = $1
		  AND qe2.is_dispatchable = true
		  AND qe2.state IN ('waiting', 'called', 'in_progress')
		  AND (qe2.priority_group < $2 OR (qe2.priority_group = $2 AND qe2.sort_key < $3))
		  AND qe2.id != $4`,
		queueSessionID, priorityGroup, sortKey, id).Scan(&positionAhead, &estimatedWait)

	pState := QueueEntryPublicPresenceState(presenceState)
	sState := QueueEntryPublicState(normalizePublicState(state))
	respondJSON(w, http.StatusOK, &QueueEntryPublic{
		Id:                   id,
		TokenNumber:          tokenNumber,
		State:                sState,
		PresenceState:        pState,
		PositionAhead:        positionAhead,
		EstimatedWaitMinutes: estimatedWait,
		Services:             services,
		PartySize:            &partySize,
		MagicLinkExpiresAt:   magicLinkExpiresAt,
		ShopName:             &shopName,
		LocationName:         &locationName,
	})
}

func (s *Server) resolveCustomerSession(ctx context.Context, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	var tenantID, locationID, visitID uuid.UUID
	var hasTenant, hasLocation, hasVisit bool

	if tVal := ctx.Value(auth.CtxTenantID); tVal != nil {
		if id, err := uuid.Parse(tVal.(string)); err == nil {
			tenantID = id
			hasTenant = true
		}
	}
	if lVal := ctx.Value(auth.CtxLocationID); lVal != nil {
		if id, err := uuid.Parse(lVal.(string)); err == nil {
			locationID = id
			hasLocation = true
		}
	}
	if vVal := ctx.Value("visit_id"); vVal != nil {
		if id, err := uuid.Parse(vVal.(string)); err == nil {
			visitID = id
			hasVisit = true
		}
	}

	if hasTenant && hasLocation && hasVisit {
		return tenantID, locationID, visitID, nil
	}

	token := r.Header.Get("X-Session-Token")
	if token == "" {
		token = r.URL.Query().Get("t")
	}
	if token == "" {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("missing session token")
	}

	query := `
		SELECT tenant_id, location_id, id 
		FROM visits 
		WHERE magic_link_token_hash = $1 
		  AND magic_link_expires_at > NOW()`
	err := s.Pool.QueryRow(ctx, query, token).Scan(&tenantID, &locationID, &visitID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("invalid or expired session token")
	}

	return tenantID, locationID, visitID, nil
}

func (s *Server) ConfirmArrival(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, locationID, visitID, err := s.resolveCustomerSession(ctx, r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": err.Error(),
		})
		return
	}

	var req ConfirmArrivalJSONBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "failed to decode request body",
		})
		return
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip = strings.TrimSpace(parts[0])
		}
	}

	params := presence.ConfirmArrivalParams{
		TenantID:   tenantID,
		LocationID: locationID,
		VisitID:    visitID,
		Method:     string(req.Method),
		IPAddress:  ip,
	}

	if req.Pin != nil {
		params.PIN = *req.Pin
	}
	if req.Latitude != nil {
		params.Latitude = *req.Latitude
	}
	if req.Longitude != nil {
		params.Longitude = *req.Longitude
	}
	if req.AccuracyMetres != nil {
		params.AccuracyMetres = float64(*req.AccuracyMetres)
	}
	if req.NfcToken != nil {
		params.NFCToken = *req.NfcToken
	}

	result, errConfirm := s.Arrival.ConfirmArrival(ctx, params)
	if errConfirm != nil {
		var arrErr *presence.ArrivalErr
		if errors.As(errConfirm, &arrErr) {
			resp := map[string]interface{}{
				"code":    arrErr.Code,
				"message": arrErr.Message,
			}
			if arrErr.AttemptsRemaining >= 0 {
				resp["attempts_remaining"] = arrErr.AttemptsRemaining
			}
			respondJSON(w, arrErr.HTTPStatus, resp)
			return
		}

		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": errConfirm.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"presence_state": result.PresenceState,
		"message":        result.Message,
	})
}

func (s *Server) ConfirmOnTheWay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, locationID, visitID, err := s.resolveCustomerSession(ctx, r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": err.Error(),
		})
		return
	}

	presenceState, errConfirm := s.Arrival.ConfirmOnTheWay(ctx, tenantID, locationID, visitID)
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

	respondJSON(w, http.StatusOK, map[string]string{
		"presence_state": presenceState,
		"message":        "Great! Head over to Star Salon when ready.",
	})
}

func (s *Server) CancelMyEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, locationID, visitID, err := s.resolveCustomerSession(ctx, r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": err.Error(),
		})
		return
	}

	errConfirm := s.Arrival.CancelMyEntry(ctx, tenantID, locationID, visitID)
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

func (s *Server) GetLocationStatus(w http.ResponseWriter, r *http.Request, locationSlug string) {
	ctx := r.Context()
	location, err := repository.GetLocationBySlug(ctx, s.Pool, locationSlug)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	tz, err := time.LoadLocation(location.Timezone)
	if err != nil {
		tz, _ = time.LoadLocation("Asia/Kolkata")
	}
	now := time.Now().In(tz)
	dayOfWeek := int(now.Weekday())

	hours, err := repository.GetLocationHoursForDay(ctx, s.Pool, location.ID, dayOfWeek)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	override, err := repository.GetActiveLocationOverride(ctx, s.Pool, location.ID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	shopStatus := computeShopStatus(override, hours, now)
	businessDate := now.Format("2006-01-02")

	stats, err := repository.GetQueueStats(ctx, s.Pool, location.ID, businessDate)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	queueOpen := shopStatus == "open" && stats.SessionExists && (stats.SessionStatus == "active" || stats.SessionStatus == "ending")

	var tempClosureEndsAt *time.Time
	if shopStatus == "temporarily_closed" && override != nil {
		tempClosureEndsAt = override.ExpiresAt
	}

	response := map[string]interface{}{
		"id":                     location.ID,
		"name":                   location.Name,
		"slug":                   location.Slug,
		"shop_status":            shopStatus,
		"queue_open":             queueOpen,
		"queue_length":           stats.QueueLength,
		"estimated_wait_minutes": stats.EstimatedWaitMinutes,
	}

	if hours != nil {
		response["business_hours_today"] = map[string]interface{}{
			"opens_at":      hours.OpensAt,
			"closes_at":     hours.ClosesAt,
			"is_open_today": hours.IsOpen,
		}
	} else {
		response["business_hours_today"] = map[string]interface{}{
			"is_open_today": false,
		}
	}

	if tempClosureEndsAt != nil {
		response["temporary_closure_ends_at"] = tempClosureEndsAt
	}

	respondJSON(w, http.StatusOK, response)
}

func (s *Server) GetServiceCatalog(w http.ResponseWriter, r *http.Request, locationId UUIDv7, params GetServiceCatalogParams) {
	ctx := r.Context()
	location, err := repository.GetLocationByID(ctx, s.Pool, locationId.String())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	gender := "all"
	if params.Gender != nil {
		gender = string(*params.Gender)
		if gender != "men" && gender != "women" && gender != "unisex" && gender != "all" {
			gender = "all"
		}
	}

	catID := ""
	if params.CategoryId != nil {
		catID = params.CategoryId.String()
	}

	dbCatalog, err := repository.GetServiceCatalog(ctx, s.Pool, location.ID, gender, catID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	// Map DB rows → OpenAPI types so the response matches the ServiceCatalog contract
	// (ServiceCategoryDB has no json tags → PascalCase; ServiceCatalog has tags → lowercase)
	apiCategories := make([]ServiceCategory, len(dbCatalog))
	for i, c := range dbCatalog {
		cID, _ := uuid.Parse(c.ID)
		apiGroups := make([]ServiceGroup, len(c.Groups))
		for j, g := range c.Groups {
			gID, _ := uuid.Parse(g.ID)
			apiVariants := make([]ServiceVariant, len(g.Variants))
			for k, v := range g.Variants {
				vID, _ := uuid.Parse(v.ID)
				isPopular := v.IsPopular
				apiVariants[k] = ServiceVariant{
					Id:                  vID,
					Name:                v.Name,
					Description:         v.Description,
					DurationMinutes:     v.DurationMinutes,
					PricePaise:          v.PricePaise,
					AllowWalkIn:         v.AllowWalkIn,
					AllowAppointment:    v.AllowAppointment,
					RequiresAppointment: v.RequiresAppointment,
					IsPopular:           &isPopular,
				}
			}
			apiGroups[j] = ServiceGroup{
				Id:          gID,
				Name:        g.Name,
				Description: g.Description,
				Variants:    apiVariants,
			}
		}
		sortOrder := c.SortOrder
		apiCategories[i] = ServiceCategory{
			Id:        cID,
			Name:      c.Name,
			Gender:    ServiceCategoryGender(c.Gender),
			SortOrder: &sortOrder,
			Groups:    apiGroups,
		}
	}

	respondJSON(w, http.StatusOK, ServiceCatalog{
		LocationId:  locationId,
		DisplayMode: ServiceCatalogDisplayMode(location.ServiceDisplayMode),
		Categories:  apiCategories,
	})
}

func (s *Server) SearchServiceVariants(w http.ResponseWriter, r *http.Request, locationId UUIDv7, params SearchServiceVariantsParams) {
	ctx := r.Context()
	location, err := repository.GetLocationByID(ctx, s.Pool, locationId.String())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	q := params.Q
	if len(q) < 2 || len(q) > 100 {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_query"})
		return
	}

	results, err := repository.SearchServiceVariants(ctx, s.Pool, location.ID, q)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}

func (s *Server) ResolveBookingOptions(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()
	location, err := repository.GetLocationByID(ctx, s.Pool, locationId.String())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	var req struct {
		VariantIds []string `json:"variant_ids"`
		PartySize  *int     `json:"party_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request_body"})
		return
	}
	if len(req.VariantIds) == 0 {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "missing_variants"})
		return
	}
	partySize := 1
	if req.PartySize != nil && *req.PartySize > 0 {
		partySize = *req.PartySize
	}

	rows, err := repository.GetVariantsByIDs(ctx, s.Pool, location.ID, req.VariantIds)
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_variants"})
		return
	}

	variants := make([]queue.VariantForResolver, len(rows))
	for i, r := range rows {
		variants[i] = queue.VariantForResolver{
			ID: r.ID, DurationMinutes: r.DurationMinutes, PricePaise: r.PricePaise,
			AllowWalkIn: r.AllowWalkIn, AllowAppointment: r.AllowAppointment,
			RequiresAppointment: r.RequiresAppointment,
		}
	}

	tz, err := time.LoadLocation(location.Timezone)
	if err != nil {
		tz, _ = time.LoadLocation("Asia/Kolkata")
	}
	now := time.Now().In(tz)
	dayOfWeek := int(now.Weekday())
	businessDate := now.Format("2006-01-02")

	stats, err := repository.GetQueueStats(ctx, s.Pool, location.ID, businessDate)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	hours, err := repository.GetLocationHoursForDay(ctx, s.Pool, location.ID, dayOfWeek)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	override, err := repository.GetActiveLocationOverride(ctx, s.Pool, location.ID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	shopStatus := computeShopStatus(override, hours, now)
	// isOpenToday derives from the effective status (override > hours) so that a
	// manual "open" override on a no-hours Phase 1 shop still allows walk_in.
	isOpenToday := shopStatus == "open" || shopStatus == "closing_soon"
	var closesAt *time.Time
	if hours != nil && hours.ClosesAt != nil {
		ca := *hours.ClosesAt
		caDate := time.Date(now.Year(), now.Month(), now.Day(), ca.Hour(), ca.Minute(), ca.Second(), 0, tz)
		closesAt = &caDate
	}

	res := queue.ResolveBookingOptions(queue.BookingResolverInput{
		Variants:             variants,
		PartySize:            partySize,
		MaxTotalQueueSize:    location.MaxTotalQueueSize,
		AllowOvertimeMinutes: location.AllowOvertimeMinutes,
		OperationMode:        location.OperationMode,
		ShopStatus:           shopStatus,
		ClosesAt:             closesAt,
		IsOpenToday:          isOpenToday,
		CurrentTime:          now,
		QueueLength:          stats.QueueLength,
		EstimatedWaitMinutes: stats.EstimatedWaitMinutes,
	})

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_duration_minutes": res.TotalDurationMinutes,
		"total_price_paise":      res.TotalPricePaise,
		"allowed_entry_methods":  res.AllowedEntryMethods,
		"blocked_reason":         res.BlockedReason,
		"queue_length":           res.QueueLength,
		"estimated_wait_minutes": res.EstimatedWaitMinutes,
	})
}

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func (s *Server) CreateCheckinIntent(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	ctx := r.Context()
	remoteIP := extractIP(r)
	limiter := getCheckinIntentLimiter(remoteIP)
	if !limiter.Allow() {
		respondJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded"})
		return
	}

	location, err := repository.GetLocationWithTenantSlug(ctx, s.Pool, locationId.String())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	var req struct {
		VariantIds   []string `json:"variant_ids"`
		PartySize    *int     `json:"party_size"`
		CustomerName *string  `json:"customer_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request_body"})
		return
	}
	if len(req.VariantIds) == 0 {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "missing_variants"})
		return
	}
	partySize := 1
	if req.PartySize != nil && *req.PartySize > 0 {
		partySize = *req.PartySize
	}

	rows, err := repository.GetVariantsByIDs(ctx, s.Pool, location.ID, req.VariantIds)
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_variants"})
		return
	}

	variants := make([]queue.VariantForResolver, len(rows))
	for i, r := range rows {
		variants[i] = queue.VariantForResolver{
			ID: r.ID, DurationMinutes: r.DurationMinutes, PricePaise: r.PricePaise,
			AllowWalkIn: r.AllowWalkIn, AllowAppointment: r.AllowAppointment,
			RequiresAppointment: r.RequiresAppointment,
		}
	}

	tz, err := time.LoadLocation(location.Timezone)
	if err != nil {
		tz, _ = time.LoadLocation("Asia/Kolkata")
	}
	now := time.Now().In(tz)
	dayOfWeek := int(now.Weekday())
	businessDate := now.Format("2006-01-02")

	stats, err := repository.GetQueueStats(ctx, s.Pool, location.ID, businessDate)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	hours, err := repository.GetLocationHoursForDay(ctx, s.Pool, location.ID, dayOfWeek)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	override, err := repository.GetActiveLocationOverride(ctx, s.Pool, location.ID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	shopStatus := computeShopStatus(override, hours, now)
	// isOpenToday derives from the effective status (override > hours) so that a
	// manual "open" override on a no-hours Phase 1 shop still allows walk_in.
	isOpenToday := shopStatus == "open" || shopStatus == "closing_soon"
	var closesAt *time.Time
	if hours != nil && hours.ClosesAt != nil {
		ca := *hours.ClosesAt
		caDate := time.Date(now.Year(), now.Month(), now.Day(), ca.Hour(), ca.Minute(), ca.Second(), 0, tz)
		closesAt = &caDate
	}

	res := queue.ResolveBookingOptions(queue.BookingResolverInput{
		Variants:             variants,
		PartySize:            partySize,
		MaxTotalQueueSize:    location.MaxTotalQueueSize,
		AllowOvertimeMinutes: location.AllowOvertimeMinutes,
		OperationMode:        location.OperationMode,
		ShopStatus:           shopStatus,
		ClosesAt:             closesAt,
		IsOpenToday:          isOpenToday,
		CurrentTime:          now,
		QueueLength:          stats.QueueLength,
		EstimatedWaitMinutes: stats.EstimatedWaitMinutes,
	})

	canWalkIn := false
	for _, em := range res.AllowedEntryMethods {
		if em == "walk_in" {
			canWalkIn = true
			break
		}
	}
	if !canWalkIn {
		reason := "walk_in_unavailable"
		if res.BlockedReason != nil {
			reason = *res.BlockedReason
		}
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":          "walk_in_unavailable",
			"blocked_reason": reason,
		})
		return
	}

	variantIDsJSON, _ := json.Marshal(req.VariantIds)
	var tokenCode string
	var insertedID string
	var expiresAt time.Time

	for attempt := 0; attempt < 3; attempt++ {
		tc, err := webhook.GenerateTokenCode()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}

		queryInsert := `
			INSERT INTO checkin_intents (
				tenant_id, location_id, token_code, channel,
				shop_status_at_creation, variant_ids, party_size,
				customer_name, status, source_ip, expires_at
			) VALUES (
				$1, $2, $3, 'whatsapp',
				$4, $5, $6,
				$7, 'created', $8::inet, NOW() + INTERVAL '23 hours'
			) RETURNING id, expires_at
		`
		err = s.Pool.QueryRow(ctx, queryInsert,
			location.TenantID, location.ID, tc,
			shopStatus, variantIDsJSON, partySize,
			req.CustomerName, remoteIP,
		).Scan(&insertedID, &expiresAt)

		if err != nil {
			if strings.Contains(err.Error(), "unique constraint") {
				continue // retry
			}
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}

		tokenCode = tc
		break
	}

	if tokenCode == "" {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	fromPhone := s.Config.BhejnaFromPhone
	if location.WhatsAppMode == "own_number" && location.BusinessWhatsAppNumber != nil {
		fromPhone = *location.BusinessWhatsAppNumber
	}

	text := "JOIN " + location.Slug + " " + tokenCode
	deepLink := "https://wa.me/" + fromPhone + "?text=" + url.QueryEscape(text)

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"intent_id":  insertedID,
		"token_code": tokenCode,
		"deep_link":  deepLink,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

func (s *Server) GetAppointmentSlots(w http.ResponseWriter, r *http.Request, locationId UUIDv7, params GetAppointmentSlotsParams) {
	ctx := r.Context()
	location, err := repository.GetLocationByID(ctx, s.Pool, locationId.String())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}

	dateStr := ""
	dateStr = params.Date.Time.Format("2006-01-02")

	var variantIDs []string
	if params.VariantIds != nil {
		for _, vid := range params.VariantIds {
			variantIDs = append(variantIDs, vid.String())
		}
	}

	rows, err := repository.GetVariantsByIDs(ctx, s.Pool, location.ID, variantIDs)
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_variants"})
		return
	}

	totalDuration := 0
	for _, r := range rows {
		totalDuration += r.DurationMinutes
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"date":                   dateStr,
		"total_duration_minutes": totalDuration,
		"slots":                  []interface{}{},
	})
}

// RegisterManualRoutes manually wires endpoints missing from or needing custom setup outside OpenAPI codegen
func (s *Server) RegisterManualRoutes(r chi.Router) {
	r.Post("/v1/staff/appointments/{appointment_id}/checkin", s.CheckInAppointment)
}

// BookAppointment handles POST /v1/appointments/book
func (s *Server) BookAppointment(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := auth.TenantIDFromCtx(r.Context())
	if tenantIDStr == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req queue.BookAppointmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	
	repo := &queue.QueueRepository{Pool: s.Pool}
	res, err := repo.BookAppointment(r.Context(), tenantID, req)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, res)
}

// CheckInAppointment handles POST /v1/staff/appointments/{appointment_id}/checkin
func (s *Server) CheckInAppointment(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := auth.TenantIDFromCtx(r.Context())
	if tenantIDStr == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	appIDStr := chi.URLParam(r, "appointment_id")
	appID, err := uuid.Parse(appIDStr)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid appointment_id"})
		return
	}

	var req struct {
		LocationID uuid.UUID `json:"location_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body location_id required"})
		return
	}
	
	repo := &queue.QueueRepository{Pool: s.Pool}
	res, err := repo.CheckInAppointment(r.Context(), tenantID, req.LocationID, appID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, res)
}

func (s *Server) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, locationID, visitID, err := s.resolveCustomerSession(ctx, r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": err.Error(),
		})
		return
	}

	var req SubmitFeedbackJSONBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "failed to decode request body",
		})
		return
	}

	if req.Rating < 1 || req.Rating > 5 {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_RATING",
			"message": "rating must be between 1 and 5",
		})
		return
	}

	var (
		frID            uuid.UUID
		frCustomerID    *uuid.UUID
		frStaffMemberID *uuid.UUID
		frExpiresAt     *time.Time
		frStatus        string
	)
	queryFR := `
		SELECT id, customer_id, staff_member_id, expires_at, status
		FROM feedback_requests
		WHERE tenant_id = $1 AND visit_id = $2
		LIMIT 1
	`
	err = s.Pool.QueryRow(ctx, queryFR, tenantID, visitID).Scan(&frID, &frCustomerID, &frStaffMemberID, &frExpiresAt, &frStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "FEEDBACK_REQUEST_NOT_FOUND",
				"message": "feedback request not found",
			})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		})
		return
	}

	if frStatus == "responded" {
		respondJSON(w, http.StatusConflict, map[string]string{
			"code":    "FEEDBACK_ALREADY_SUBMITTED",
			"message": "feedback has already been submitted for this visit",
		})
		return
	}

	isLate := false
	if frExpiresAt != nil && time.Now().After(*frExpiresAt) {
		isLate = true
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		})
		return
	}
	defer tx.Rollback(ctx)

	var responseID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO feedback_responses (
			tenant_id, location_id, feedback_request_id, visit_id,
			customer_id, staff_member_id, rating, comment, source, is_late, received_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, 'web', $9, NOW()
		)
		ON CONFLICT (tenant_id, feedback_request_id) DO NOTHING
		RETURNING id
	`, tenantID, locationID, frID, visitID, frCustomerID, frStaffMemberID, req.Rating, req.Comment, isLate).Scan(&responseID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			respondJSON(w, http.StatusConflict, map[string]string{
				"code":    "FEEDBACK_ALREADY_SUBMITTED",
				"message": "feedback has already been submitted for this visit",
			})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		})
		return
	}

	_, err = tx.Exec(ctx, `
		UPDATE feedback_requests
		SET status = 'responded',
		    updated_at = NOW()
		WHERE id = $1
	`, frID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id": responseID,
	})
}


