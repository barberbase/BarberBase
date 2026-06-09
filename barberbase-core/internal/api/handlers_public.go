package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/repository"
)

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
	sState := QueueEntryPublicState(state)

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
