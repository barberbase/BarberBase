package presence

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

// Service owns all arrival verification logic. One instance, held as a field on api.Server.
type Service struct {
	db   *pgxpool.Pool
	sse  BroadcastFunc
	ipRL *ipRateLimiter
}

// ipRateLimiter manages per-IP x/time/rate limiters.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// newIPRateLimiter creates a limiter store.
// Each IP gets a token bucket: 10 tokens, refill rate 1 token per 6 minutes (= 10/hr).
func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
	}
}

// allow checks (and consumes) one token for the given IP.
// Returns false if the IP is at limit.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, exists := l.limiters[ip]
	if !exists {
		lim = rate.NewLimiter(rate.Every(6*time.Minute), 10)
		l.limiters[ip] = lim
	}
	return lim.Allow()
}

// BroadcastFunc is the SSE broadcast hook. Injected at construction.
type BroadcastFunc func(locationID uuid.UUID, version int64)

// NewService constructs the arrival service.
func NewService(db *pgxpool.Pool, broadcast BroadcastFunc) *Service {
	return &Service{
		db:   db,
		sse:  broadcast,
		ipRL: newIPRateLimiter(),
	}
}

// ConfirmArrivalParams carries the parsed and pre-validated request body.
type ConfirmArrivalParams struct {
	TenantID       uuid.UUID
	LocationID     uuid.UUID
	VisitID        uuid.UUID   // from CustomerSession context
	Method         string      // "pin", "geolocation", "nfc"
	PIN            string      // non-empty when Method="pin"
	Latitude       float64
	Longitude      float64
	AccuracyMetres float64
	NFCToken       string      // non-empty when Method="nfc"
	IPAddress      string      // net/http RemoteAddr, stripped of port
}

// ConfirmArrivalResult is returned on 200.
type ConfirmArrivalResult struct {
	PresenceState string // always "arrived"
	Message       string
}

// ArrivalErr carries structured error data back to the handler.
type ArrivalErr struct {
	Code              string
	Message           string
	HTTPStatus        int // 400, 422, 429
	AttemptsRemaining int // -1 when not applicable
}

func (e *ArrivalErr) Error() string { return e.Message }

// ConfirmArrival implements the three customer-facing arrival verification paths.
func (s *Service) ConfirmArrival(ctx context.Context, params ConfirmArrivalParams) (*ConfirmArrivalResult, error) {
	// 1. Per-IP rate check (in-memory)
	if !s.ipRL.allow(params.IPAddress) {
		return nil, &ArrivalErr{Code: "RATE_LIMITED", Message: "Too many arrival attempts from this IP", HTTPStatus: 429, AttemptsRemaining: -1}
	}

	// 2. Fetch location + current attempt count (no lock — read-only)
	var loc struct {
		ArrivalPinHash      *string
		ArrivalPinPlain     *string
		GPSLatitude         *float64
		GPSLongitude        *float64
		ArrivalRadiusMetres int
		NfcEnabled          bool
		NfcTokenHash        *string
	}

	err := s.db.QueryRow(ctx, `
		SELECT arrival_pin_hash, arrival_pin_plain, gps_latitude, gps_longitude,
		       arrival_radius_metres, nfc_enabled, nfc_token_hash
		FROM locations
		WHERE id = $1 AND is_active = true`, params.LocationID).Scan(
		&loc.ArrivalPinHash, &loc.ArrivalPinPlain, &loc.GPSLatitude, &loc.GPSLongitude,
		&loc.ArrivalRadiusMetres, &loc.NfcEnabled, &loc.NfcTokenHash,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &ArrivalErr{Code: "LOCATION_NOT_FOUND", Message: "Location not found or inactive", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		return nil, err
	}

	var attemptCount int
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM arrival_attempts
		WHERE queue_entry_id = (
			SELECT id FROM queue_entries WHERE visit_id = $1
		) AND success = false`, params.VisitID).Scan(&attemptCount)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	if attemptCount >= 5 {
		return nil, &ArrivalErr{Code: "RATE_LIMITED", Message: "Max verification attempts reached", HTTPStatus: 429, AttemptsRemaining: 0}
	}

	// 3. Pre-transaction validation by method
	pinOK := false
	gpsOK := false
	nfcOK := false

	switch params.Method {
	case "pin":
		if params.PIN == "" {
			return nil, &ArrivalErr{Code: "MISSING_PIN", Message: "PIN is required", HTTPStatus: 400, AttemptsRemaining: -1}
		}
		if loc.ArrivalPinHash == nil || *loc.ArrivalPinHash == "" {
			return nil, &ArrivalErr{Code: "PIN_NOT_CONFIGURED", Message: "PIN verification is not configured for this location", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		pinOK = bcrypt.CompareHashAndPassword([]byte(*loc.ArrivalPinHash), []byte(params.PIN)) == nil

	case "geolocation":
		if params.AccuracyMetres > 150 {
			return nil, &ArrivalErr{Code: "GPS_ACCURACY_TOO_LOW", Message: "GPS accuracy is too low", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		if loc.GPSLatitude == nil || loc.GPSLongitude == nil || (*loc.GPSLatitude == 0 && *loc.GPSLongitude == 0) {
			return nil, &ArrivalErr{Code: "LOCATION_GPS_NOT_CONFIGURED", Message: "Location GPS is not configured", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		dist := haversineMetres(params.Latitude, params.Longitude, *loc.GPSLatitude, *loc.GPSLongitude)
		gpsOK = dist <= float64(loc.ArrivalRadiusMetres)

	case "nfc":
		if !loc.NfcEnabled {
			return nil, &ArrivalErr{Code: "NFC_NOT_ENABLED", Message: "NFC verification is not enabled", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		if params.NFCToken == "" {
			return nil, &ArrivalErr{Code: "MISSING_NFC_TOKEN", Message: "NFC Token is required", HTTPStatus: 400, AttemptsRemaining: -1}
		}
		if loc.NfcTokenHash == nil || *loc.NfcTokenHash == "" {
			return nil, &ArrivalErr{Code: "NFC_NOT_CONFIGURED", Message: "NFC is not configured", HTTPStatus: 422, AttemptsRemaining: -1}
		}
		nfcOK = bcrypt.CompareHashAndPassword([]byte(*loc.NfcTokenHash), []byte(params.NFCToken)) == nil

	default:
		return nil, &ArrivalErr{Code: "INVALID_METHOD", Message: "Invalid verification method", HTTPStatus: 400, AttemptsRemaining: -1}
	}

	// 4. Transaction — fast DB ops only
	var result *ConfirmArrivalResult
	var newVersion int64
	var locationID uuid.UUID
	var pinErr error

	txErr := s.withTx(ctx, func(tx pgx.Tx) error {
		// Fetch entry and lock it
		var entry struct {
			ID             uuid.UUID
			QueueSessionID uuid.UUID
			PresenceState  string
			State          string
		}

		err = tx.QueryRow(ctx, `
			SELECT qe.id, qe.queue_session_id, qe.presence_state, qe.state
			FROM queue_entries qe
			JOIN visits v ON v.id = qe.visit_id
			WHERE qe.visit_id = $1 AND v.tenant_id = $2
			FOR UPDATE OF qe`, params.VisitID, params.TenantID).Scan(
			&entry.ID, &entry.QueueSessionID, &entry.PresenceState, &entry.State,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ArrivalErr{Code: "ENTRY_NOT_FOUND", Message: "Queue entry not found", HTTPStatus: 422, AttemptsRemaining: -1}
			}
			return err
		}

		if entry.State == "completed" || entry.State == "cancelled" || entry.State == "expired" || entry.State == "no_show" {
			return &ArrivalErr{Code: "INVALID_STATE", Message: "Queue entry is in a terminal state", HTTPStatus: 422, AttemptsRemaining: -1}
		}

		// Lock queue_session (Law 1)
		var session struct {
			ID           uuid.UUID
			QueueVersion int64
		}
		err = tx.QueryRow(ctx, `
			SELECT id, queue_version
			FROM queue_sessions
			WHERE id = $1
			FOR UPDATE`, entry.QueueSessionID).Scan(&session.ID, &session.QueueVersion)
		if err != nil {
			return err
		}

		// Double check attempt count inside tx
		var txAttemptCount int
		err = tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM arrival_attempts
			WHERE queue_entry_id = $1 AND success = false`, entry.ID).Scan(&txAttemptCount)
		if err != nil {
			return err
		}

		if txAttemptCount >= 5 {
			return &ArrivalErr{Code: "RATE_LIMITED", Message: "Max verification attempts reached", HTTPStatus: 429, AttemptsRemaining: 0}
		}

		verified := pinOK || gpsOK || nfcOK

		// Determine IP Address representation for INET
		var ipAddr interface{}
		if params.IPAddress != "" {
			ipAddr = params.IPAddress
		} else {
			ipAddr = nil
		}

		// Log the attempt
		_, err = tx.Exec(ctx, `
			INSERT INTO arrival_attempts
				(id, tenant_id, location_id, queue_entry_id, method, success, ip_address, attempted_at)
			VALUES
				(gen_random_uuid(), $1, $2, $3, $4, $5, $6, NOW())`,
			params.TenantID, params.LocationID, entry.ID, params.Method, verified, ipAddr,
		)
		if err != nil {
			return err
		}

		if !verified {
			switch params.Method {
			case "pin":
				remaining := 5 - (txAttemptCount + 1)
				if remaining < 0 {
					remaining = 0
				}
				pinErr = &ArrivalErr{Code: "WRONG_PIN", Message: "Incorrect PIN", HTTPStatus: 400, AttemptsRemaining: remaining}
			case "geolocation":
				pinErr = &ArrivalErr{Code: "GPS_OUT_OF_RANGE", Message: "You are outside the shop's arrival area", HTTPStatus: 422, AttemptsRemaining: -1}
			case "nfc":
				pinErr = &ArrivalErr{Code: "WRONG_NFC_TOKEN", Message: "Invalid NFC Token", HTTPStatus: 422, AttemptsRemaining: -1}
			}
			return nil
		}

		// Success path
		if entry.PresenceState == "arrived" {
			// Idempotent
			result = &ConfirmArrivalResult{
				PresenceState: "arrived",
				Message:       "Welcome! You are next in line.",
			}
			newVersion = session.QueueVersion
			locationID = params.LocationID
			return nil
		}

		// Update entry state
		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET presence_state = 'arrived',
			    arrived_at     = NOW(),
			    is_dispatchable = true
			WHERE id = $1`, entry.ID)
		if err != nil {
			return err
		}

		// Increment queue session version
		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version`, session.ID).Scan(&newVersion)
		if err != nil {
			return err
		}

		result = &ConfirmArrivalResult{
			PresenceState: "arrived",
			Message:       "Welcome! You are next in line.",
		}
		locationID = params.LocationID
		return nil
	})

	if txErr != nil {
		return nil, txErr
	}

	if pinErr != nil {
		return nil, pinErr
	}

	// SSE broadcast post commit (Law 8)
	if s.sse != nil && newVersion > 0 {
		s.sse(locationID, newVersion)
	}

	return result, nil
}

// ConfirmOnTheWay transitions presence_state → on_the_way.
func (s *Service) ConfirmOnTheWay(ctx context.Context, tenantID, locationID, visitID uuid.UUID) (string, error) {
	var newVersion int64
	txErr := s.withTx(ctx, func(tx pgx.Tx) error {
		// Fetch entry
		var entry struct {
			ID             uuid.UUID
			QueueSessionID uuid.UUID
			PresenceState  string
			State          string
		}
		err := tx.QueryRow(ctx, `
			SELECT qe.id, qe.queue_session_id, qe.presence_state, qe.state
			FROM queue_entries qe
			JOIN visits v ON v.id = qe.visit_id
			WHERE qe.visit_id = $1 AND v.tenant_id = $2
			FOR UPDATE OF qe`, visitID, tenantID).Scan(&entry.ID, &entry.QueueSessionID, &entry.PresenceState, &entry.State)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ArrivalErr{Code: "ENTRY_NOT_FOUND", Message: "Queue entry not found", HTTPStatus: 422}
			}
			return err
		}

		// Lock queue session (Law 1)
		var sessionID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, entry.QueueSessionID).Scan(&sessionID)
		if err != nil {
			return err
		}

		// Validate transitions
		if entry.State == "completed" || entry.State == "cancelled" || entry.State == "expired" || entry.State == "no_show" || entry.State == "in_progress" {
			return &ArrivalErr{Code: "INVALID_TRANSITION", Message: "Cannot transition to on_the_way in current state", HTTPStatus: 422}
		}

		if entry.PresenceState == "arrived" || entry.PresenceState == "snoozed" {
			return &ArrivalErr{Code: "INVALID_TRANSITION", Message: "Cannot transition to on_the_way from current presence state", HTTPStatus: 422}
		}

		// Update entry state
		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET presence_state = 'on_the_way',
			    on_the_way_at  = NOW()
			WHERE id = $1`, entry.ID)
		if err != nil {
			return err
		}

		// Increment queue session version
		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version`, sessionID).Scan(&newVersion)
		if err != nil {
			return err
		}

		return nil
	})

	if txErr != nil {
		return "", txErr
	}

	// SSE broadcast post commit (Law 8)
	if s.sse != nil && newVersion > 0 {
		s.sse(locationID, newVersion)
	}

	return "on_the_way", nil
}

// CancelMyEntry transitions queue_entry state → cancelled.
func (s *Service) CancelMyEntry(ctx context.Context, tenantID, locationID, visitID uuid.UUID) error {
	var newVersion int64
	txErr := s.withTx(ctx, func(tx pgx.Tx) error {
		// Fetch entry
		var entry struct {
			ID             uuid.UUID
			QueueSessionID uuid.UUID
			State          string
		}
		err := tx.QueryRow(ctx, `
			SELECT qe.id, qe.queue_session_id, qe.state
			FROM queue_entries qe
			JOIN visits v ON v.id = qe.visit_id
			WHERE qe.visit_id = $1 AND v.tenant_id = $2
			FOR UPDATE OF qe`, visitID, tenantID).Scan(&entry.ID, &entry.QueueSessionID, &entry.State)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ArrivalErr{Code: "ENTRY_NOT_FOUND", Message: "Queue entry not found", HTTPStatus: 422}
			}
			return err
		}

		// Lock queue session (Law 1)
		var sessionID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, entry.QueueSessionID).Scan(&sessionID)
		if err != nil {
			return err
		}

		if entry.State != "waiting" && entry.State != "called" {
			return &ArrivalErr{
				Code:       "CANNOT_CANCEL",
				Message:    "Cannot cancel while service is in progress",
				HTTPStatus: 422,
			}
		}

		// Update entry state
		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET state           = 'cancelled',
			    is_dispatchable = false
			WHERE id = $1`, entry.ID)
		if err != nil {
			return err
		}

		// Increment queue session version
		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version`, sessionID).Scan(&newVersion)
		if err != nil {
			return err
		}

		return nil
	})

	if txErr != nil {
		return txErr
	}

	// SSE broadcast post commit (Law 8)
	if s.sse != nil && newVersion > 0 {
		s.sse(locationID, newVersion)
	}

	return nil
}

// StaffConfirmArrival confirms arrival via staff authority. StaffJWT authenticated.
func (s *Service) StaffConfirmArrival(
	ctx context.Context,
	entryID    uuid.UUID,
	tenantID   uuid.UUID,
	locationID uuid.UUID, // from JWT, for isolation check
) error {
	var newVersion int64
	txErr := s.withTx(ctx, func(tx pgx.Tx) error {
		// Fetch entry
		var entry struct {
			ID             uuid.UUID
			QueueSessionID uuid.UUID
			PresenceState  string
			State          string
			LocationID     uuid.UUID
		}
		err := tx.QueryRow(ctx, `
			SELECT qe.id, qe.queue_session_id, qe.presence_state, qe.state, qs.location_id
			FROM queue_entries qe
			JOIN queue_sessions qs ON qs.id = qe.queue_session_id
			WHERE qe.id = $1 AND qs.tenant_id = $2
			FOR UPDATE OF qe`, entryID, tenantID).Scan(&entry.ID, &entry.QueueSessionID, &entry.PresenceState, &entry.State, &entry.LocationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ArrivalErr{Code: "ENTRY_NOT_FOUND", Message: "Queue entry not found", HTTPStatus: 404}
			}
			return err
		}

		if entry.LocationID != locationID {
			return &ArrivalErr{Code: "FORBIDDEN", Message: "Entry does not belong to your location", HTTPStatus: 403}
		}

		if entry.State == "completed" || entry.State == "cancelled" || entry.State == "expired" || entry.State == "no_show" {
			return &ArrivalErr{Code: "INVALID_STATE", Message: "Queue entry is in a terminal state", HTTPStatus: 422}
		}

		// Lock queue session (Law 1)
		var sessionID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE`, entry.QueueSessionID).Scan(&sessionID)
		if err != nil {
			return err
		}

		// Log the attempt
		_, err = tx.Exec(ctx, `
			INSERT INTO arrival_attempts
				(id, tenant_id, location_id, queue_entry_id, method, success, ip_address, attempted_at)
			VALUES
				(gen_random_uuid(), $1, $2, $3, 'staff', true, NULL, NOW())`,
			tenantID, locationID, entry.ID,
		)
		if err != nil {
			return err
		}

		if entry.PresenceState == "arrived" {
			return nil
		}

		// Update entry state
		_, err = tx.Exec(ctx, `
			UPDATE queue_entries
			SET presence_state = 'arrived',
			    arrived_at     = NOW(),
			    is_dispatchable = true
			WHERE id = $1`, entry.ID)
		if err != nil {
			return err
		}

		// Increment queue session version
		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version`, sessionID).Scan(&newVersion)
		if err != nil {
			return err
		}

		return nil
	})

	if txErr != nil {
		return txErr
	}

	// SSE broadcast post commit (Law 8)
	if s.sse != nil && newVersion > 0 {
		s.sse(locationID, newVersion)
	}

	return nil
}

// withTx runs a callback within a database transaction.
func (s *Service) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

// haversineMetres returns the great-circle distance in metres between two WGS-84 points.
func haversineMetres(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180.0
	phi1 := lat1 * rad
	phi2 := lat2 * rad
	dphi := (lat2 - lat1) * rad
	dlambda := (lon2 - lon1) * rad

	a := math.Sin(dphi/2)*math.Sin(dphi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dlambda/2)*math.Sin(dlambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}
