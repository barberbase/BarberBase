package api

// Device call-next layer — transport-agnostic hardware button support.
//
// Law 11 extension: tenant_id and location_id come from the authenticated
// station_devices row only — never from the request body.
// Law 21 extension: this layer is zero-degradation convenience; removing it
// changes nothing else — dashboard and push paths are fully independent.
// Law 20 spirit: the domain layer never sees the device token; auth context
// construction mirrors the PAT path (tenant resolved server-side).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"barberbase-core/internal/device"
	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/realtime"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"
)

// staleThreshold: a press buffered by a gateway (4G blip) older than this is
// discarded — a delayed call-next is worse than a dropped one.
const staleThreshold = 60 * time.Second

// Per-device limiter map, same pattern as pushRateLimiters: growth is bounded
// by the number of real devices (only hash-authenticated devices get an entry).
var deviceRateLimiters sync.Map

func getDeviceRateLimiter(deviceID string) *rate.Limiter {
	v, _ := deviceRateLimiters.LoadOrStore(
		deviceID,
		rate.NewLimiter(rate.Every(3*time.Second), 1),
	)
	return v.(*rate.Limiter)
}

// touchDeviceLastSeen is fire-and-forget, always outside the queue tx.
func (s *Server) touchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) {
	if _, err := s.Pool.Exec(ctx, `UPDATE station_devices SET last_seen_at = NOW() WHERE id = $1`, deviceID); err != nil {
		log.Printf("[Warn] device last_seen update failed: %v", err)
	}
}

// DeviceCallNext implements POST /v1/device/call-next (DeviceToken auth).
// 200 result variants (advanced | no_waiting | stale_discarded) map to LED
// patterns on the gateway; only 4xx/5xx/network read as red.
func (s *Server) DeviceCallNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Authenticate: SHA-256(token) row lookup. No token material logged.
	token := r.Header.Get("X-Device-Token")
	if token == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "UNAUTHORIZED", "message": "missing X-Device-Token header",
		})
		return
	}
	var deviceID, tenantID, locationID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, location_id
		FROM station_devices
		WHERE token_hash = $1 AND is_active = true`,
		device.Hash(token),
	).Scan(&deviceID, &tenantID, &locationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"code": "UNAUTHORIZED", "message": "invalid or revoked device token",
			})
			return
		}
		log.Printf("[Error] DeviceCallNext auth lookup failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	// 2. Rate limit: 1 per 3s per device.
	if !getDeviceRateLimiter(deviceID.String()).Allow() {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	// 3. Parse body.
	var body struct {
		ButtonCode string `json:"button_code"`
		PressedAt  int64  `json:"pressed_at"` // optional unix seconds
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ButtonCode == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "button_code is required",
		})
		return
	}

	// 4. Stale-press guard: discard buffered presses, but still record liveness.
	if body.PressedAt > 0 && time.Since(time.Unix(body.PressedAt, 0)) > staleThreshold {
		s.touchDeviceLastSeen(ctx, deviceID)
		respondJSON(w, http.StatusOK, map[string]string{"result": "stale_discarded"})
		return
	}

	// 5. Resolve button.
	var buttonStaffID *uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		SELECT staff_member_id FROM station_buttons
		WHERE device_id = $1 AND button_code = $2`,
		deviceID, body.ButtonCode,
	).Scan(&buttonStaffID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code": "NOT_FOUND", "message": "unknown button_code for this device",
			})
			return
		}
		log.Printf("[Error] DeviceCallNext button lookup failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	// 6. Same domain function as the JWT and PAT paths. NULL button staff →
	// uuid.Nil → pooled dispatch with no barber assignment.
	staffMemberID := uuid.Nil
	if buttonStaffID != nil {
		staffMemberID = *buttonStaffID
	}
	output, err := queue.CallNext(ctx, s.Pool, queue.CallNextParams{
		TenantID:        tenantID,
		LocationID:      locationID,
		StaffMemberID:   staffMemberID,
		BhejnaFromPhone: s.Config.BhejnaFromPhone,
		HMACSecret:      []byte(s.Config.HMACSecret),
	})
	if err != nil {
		s.touchDeviceLastSeen(ctx, deviceID)
		var noDispErr queue.ErrNoDispatchable
		if errors.As(err, &noDispErr) || errors.Is(err, queue.ErrSessionNotFound) {
			respondJSON(w, http.StatusOK, map[string]string{"result": "no_waiting"})
			return
		}
		if errors.Is(err, queue.ErrLockTimeout) {
			w.Header().Set("Retry-After", "1")
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lock_timeout_retry"})
			return
		}
		log.Printf("[Error] DeviceCallNext CallNext failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	// Law 8: SSE broadcast after commit.
	if s.Manager != nil {
		s.Manager.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: output.QueueVersion,
		})
	}

	s.touchDeviceLastSeen(ctx, deviceID)
	respondJSON(w, http.StatusOK, map[string]any{
		"result":              "advanced",
		"called_token_number": output.Entry.TokenNumber,
	})
}

// ── Platform-admin CRUD (operator-driven onboarding, no self-registration) ──

// CreateStationDevice implements POST /v1/admin/devices (PLATFORM_ADMIN_KEY).
// Returns the plaintext secret exactly once; only the hash is stored.
func (s *Server) CreateStationDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		TenantID   uuid.UUID `json:"tenant_id"`
		LocationID uuid.UUID `json:"location_id"`
		Label      string    `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.TenantID == uuid.Nil || body.LocationID == uuid.Nil || body.Label == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "tenant_id, location_id and label are required",
		})
		return
	}

	// Location must belong to the tenant — reject cross-tenant wiring at create time.
	var ok bool
	if err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1 AND tenant_id = $2)`,
		body.LocationID, body.TenantID).Scan(&ok); err != nil || !ok {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code": "INVALID_LOCATION", "message": "location does not belong to tenant",
		})
		return
	}

	secret, err := device.NewSecret()
	if err != nil {
		log.Printf("[Error] CreateStationDevice secret generation failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO station_devices (tenant_id, location_id, label, token_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		body.TenantID, body.LocationID, body.Label, device.Hash(secret),
	).Scan(&id)
	if err != nil {
		log.Printf("[Error] CreateStationDevice insert failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"id":     id,
		"label":  body.Label,
		"secret": secret, // shown once — not retrievable later
	})
}

// CreateStationButton implements operationId createStationButton (POST /v1/admin/devices/{device_id}/buttons).
func (s *Server) CreateStationButton(w http.ResponseWriter, r *http.Request, deviceId UUIDv7) {
	ctx := r.Context()
	deviceID := uuid.UUID(deviceId)
	var body struct {
		ButtonCode    string     `json:"button_code"`
		StaffMemberID *uuid.UUID `json:"staff_member_id"` // null = pooled/shop-wide
		Label         string     `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ButtonCode == "" || body.Label == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "button_code and label are required",
		})
		return
	}

	// Barber-bound buttons must reference active staff at the device's location.
	if body.StaffMemberID != nil {
		var ok bool
		if err := s.Pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM staff_members sm
				JOIN station_devices sd ON sd.location_id = sm.location_id
				WHERE sm.id = $1 AND sd.id = $2 AND sm.is_active = true)`,
			*body.StaffMemberID, deviceID).Scan(&ok); err != nil || !ok {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code": "INVALID_STAFF", "message": "staff member not active at device location",
			})
			return
		}
	}

	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO station_buttons (device_id, button_code, staff_member_id, label)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		deviceID, body.ButtonCode, body.StaffMemberID, body.Label,
	).Scan(&id)
	if err != nil {
		// FK miss (unknown device) and UNIQUE(device_id, button_code) both land here.
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code": "INVALID_BUTTON", "message": "unknown device or duplicate button_code",
		})
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// SetStationDeviceActive implements operationId setStationDeviceActive (PATCH /v1/admin/devices/{device_id}).
func (s *Server) SetStationDeviceActive(w http.ResponseWriter, r *http.Request, deviceId UUIDv7) {
	ctx := r.Context()
	deviceID := uuid.UUID(deviceId)
	var body struct {
		IsActive *bool `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IsActive == nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "is_active is required",
		})
		return
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE station_devices SET is_active = $1 WHERE id = $2`,
		*body.IsActive, deviceID)
	if err != nil {
		log.Printf("[Error] SetStationDeviceActive failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}
	if tag.RowsAffected() == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{
			"code": "NOT_FOUND", "message": "device not found",
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListStationDevices serves GET /v1/admin/devices?location_id=… for the
// platform console. Manual route (PLATFORM_ADMIN_KEY) — not yet in
// openapi.yaml; the one-line spec addition is queued for the next
// frozen-file session. Returns devices with their buttons plus the
// location's active staff so the console can offer barber binding.
func (s *Server) ListStationDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locationID, err := uuid.Parse(r.URL.Query().Get("location_id"))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "location_id query param required",
		})
		return
	}

	type buttonOut struct {
		ID            uuid.UUID  `json:"id"`
		ButtonCode    string     `json:"button_code"`
		Label         *string    `json:"label"`
		StaffMemberID *uuid.UUID `json:"staff_member_id"`
	}
	type deviceOut struct {
		ID         uuid.UUID   `json:"id"`
		Label      string      `json:"label"`
		IsActive   bool        `json:"is_active"`
		LastSeenAt *time.Time  `json:"last_seen_at"`
		Buttons    []buttonOut `json:"buttons"`
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT d.id, d.label, d.is_active, d.last_seen_at,
		       b.id, b.button_code, b.label, b.staff_member_id
		FROM station_devices d
		LEFT JOIN station_buttons b ON b.device_id = d.id
		WHERE d.location_id = $1
		ORDER BY d.created_at, b.button_code`, locationID)
	if err != nil {
		log.Printf("[Error] ListStationDevices query failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}
	defer rows.Close()

	devices := []deviceOut{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var d deviceOut
		var b buttonOut
		var bID *uuid.UUID
		var bCode *string
		if err := rows.Scan(&d.ID, &d.Label, &d.IsActive, &d.LastSeenAt,
			&bID, &bCode, &b.Label, &b.StaffMemberID); err != nil {
			log.Printf("[Error] ListStationDevices scan failed: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "INTERNAL_ERROR", "message": "internal server error",
			})
			return
		}
		i, seen := index[d.ID]
		if !seen {
			d.Buttons = []buttonOut{}
			devices = append(devices, d)
			i = len(devices) - 1
			index[d.ID] = i
		}
		if bID != nil {
			b.ID = *bID
			b.ButtonCode = *bCode
			devices[i].Buttons = append(devices[i].Buttons, b)
		}
	}

	type staffOut struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
		Role string    `json:"role"`
	}
	staff := []staffOut{}
	srows, err := s.Pool.Query(ctx, `
		SELECT id, name, role FROM staff_members
		WHERE location_id = $1 AND is_active = true
		ORDER BY name`, locationID)
	if err != nil {
		log.Printf("[Error] ListStationDevices staff query failed: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "INTERNAL_ERROR", "message": "internal server error",
		})
		return
	}
	defer srows.Close()
	for srows.Next() {
		var m staffOut
		if err := srows.Scan(&m.ID, &m.Name, &m.Role); err != nil {
			continue
		}
		staff = append(staff, m)
	}

	respondJSON(w, http.StatusOK, map[string]any{"devices": devices, "staff": staff})
}
