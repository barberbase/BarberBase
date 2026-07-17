package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"barberbase-core/internal/repository"
)

// VariantForResolver is a minimal view of service_variants needed for resolution.
// Populated by the repository layer before calling ResolveBookingOptions.
type VariantForResolver struct {
	ID                  string // UUID
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
}

// BookingResolverInput carries all pre-loaded data the resolver needs.
// DB calls happen in the handler before constructing this struct.
type BookingResolverInput struct {
	Variants             []VariantForResolver
	PartySize            int
	// From locations table
	MaxTotalQueueSize    int
	AllowOvertimeMinutes int
	OperationMode        string // 'walk_in_only' | 'appointment_only' | 'hybrid'
	// Computed shop status (override > hours)
	ShopStatus           string // 'open' | 'closing_soon' | 'temporarily_closed' | 'closed'
	// From location_hours for today (nil if location is closed today)
	ClosesAt             *time.Time // location-timezone wall clock time
	IsOpenToday          bool
	CurrentTime          time.Time
	// From queue_sessions + queue_entries
	QueueLength          int
	EstimatedWaitMinutes int
}

// BookingResolverResult maps directly to the BookingOptions OpenAPI schema.
type BookingResolverResult struct {
	TotalDurationMinutes int      `json:"total_duration_minutes"`
	TotalPricePaise      int      `json:"total_price_paise"`
	// JSON key: allowed_entry_methods (NOT allowed_modes)
	AllowedEntryMethods  []string `json:"allowed_entry_methods"` // "walk_in" | "appointment"
	BlockedReason        *string  `json:"blocked_reason,omitempty"`  // "shop_closed" | "queue_full" | "requires_appointment" | "closing_time_exceeded"
	QueueLength          int      `json:"queue_length"`
	EstimatedWaitMinutes int      `json:"estimated_wait_minutes"`
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func remove(slice []string, item string) []string {
	var out []string
	for _, s := range slice {
		if s != item {
			out = append(out, s)
		}
	}
	return out
}

// ResolveBookingOptions applies booking rules in deterministic priority order:
// 1. Variant rules (requires_appointment wins unconditionally)
// 2. Shop state / timing gates (remove walk_in only)
// 3. operation_mode narrows further
func ResolveBookingOptions(in BookingResolverInput) BookingResolverResult {
	// Step 1: totals
	totalDuration := 0
	totalPrice := 0
	for _, v := range in.Variants {
		totalDuration += v.DurationMinutes
		totalPrice += v.PricePaise
	}
	totalDuration *= in.PartySize

	// Step 2: variant-level rules (strictest wins)
	anyRequiresAppointment := false
	allAllowWalkIn := true
	allAllowAppointment := true
	for _, v := range in.Variants {
		if v.RequiresAppointment {
			anyRequiresAppointment = true
		}
		if !v.AllowWalkIn {
			allAllowWalkIn = false
		}
		if !v.AllowAppointment {
			allAllowAppointment = false
		}
	}

	allowedSet := []string{}
	var blockedReason *string

	if anyRequiresAppointment {
		// requires_appointment overrides everything — walk_in is never allowed
		if allAllowAppointment {
			allowedSet = []string{"appointment"}
		}
		// If also !allAllowAppointment: allowedSet stays empty
		r := "requires_appointment"
		blockedReason = &r
	} else {
		if allAllowWalkIn {
			allowedSet = append(allowedSet, "walk_in")
		}
		if allAllowAppointment {
			allowedSet = append(allowedSet, "appointment")
		}
	}

	// Step 3: shop state / timing gates — only removes walk_in
	if contains(allowedSet, "walk_in") {
		if in.ShopStatus == "closed" || in.ShopStatus == "temporarily_closed" {
			r := "shop_closed"
			blockedReason = &r
			allowedSet = remove(allowedSet, "walk_in")
		} else if !in.IsOpenToday {
			r := "shop_closed"
			blockedReason = &r
			allowedSet = remove(allowedSet, "walk_in")
		} else if in.ClosesAt != nil {
			deadline := in.ClosesAt.Add(time.Duration(in.AllowOvertimeMinutes) * time.Minute)
			serviceEnd := in.CurrentTime.Add(time.Duration(totalDuration) * time.Minute)
			if serviceEnd.After(deadline) {
				r := "closing_time_exceeded"
				blockedReason = &r
				allowedSet = remove(allowedSet, "walk_in")
			}
		}
	}

	if contains(allowedSet, "walk_in") && in.QueueLength >= in.MaxTotalQueueSize {
		r := "queue_full"
		blockedReason = &r
		allowedSet = remove(allowedSet, "walk_in")
	}

	// Step 4: operation_mode filter
	switch in.OperationMode {
	case "walk_in_only":
		allowedSet = remove(allowedSet, "appointment")
	case "appointment_only":
		allowedSet = remove(allowedSet, "walk_in")
		if !contains(allowedSet, "walk_in") && blockedReason == nil {
			r := "requires_appointment"
			blockedReason = &r
		}
	}

	if len(allowedSet) > 0 {
		blockedReason = nil
	}

	return BookingResolverResult{
		TotalDurationMinutes: totalDuration,
		TotalPricePaise:      totalPrice,
		AllowedEntryMethods:  allowedSet,
		BlockedReason:        blockedReason,
		QueueLength:          in.QueueLength,
		EstimatedWaitMinutes: in.EstimatedWaitMinutes,
	}
}

type BookAppointmentRequest struct {
	LocationID        uuid.UUID   // from body
	VariantIDs        []uuid.UUID // from body, minItems:1
	PartySize         int         // from body, default 1
	ScheduledStartAt  time.Time   // from body
	PhoneNumber       string      // E.164
	CustomerName      *string     // nullable
	RequestedBarberID *uuid.UUID  // nullable
	InitiatedVia      string      // from body enum or default "staff_dashboard"
	IdempotencyKey    string      // UUIDv4 from client
}

type BookAppointmentResult struct {
	AppointmentID    uuid.UUID
	ScheduledStartAt time.Time
	Status           string // always "scheduled"
	Services         []ServiceSummary
	TotalDurationMin int
	TotalPricePaise  int
	MagicLink        string
}

type ServiceSummary struct {
	Name            string
	DurationMinutes int
}

type CheckInAppointmentResult struct {
	VisitID         uuid.UUID
	QueueEntryID    uuid.UUID
	TokenNumber     int
	NewQueueVersion int // for post-commit SSE broadcast (Law 8)
	MagicLink       string // generated from visit's magic_link_token_hash
}

type QueueRepository struct {
	Pool *pgxpool.Pool
}

func (r *QueueRepository) BookAppointment(ctx context.Context, tenantID uuid.UUID, req BookAppointmentRequest) (*BookAppointmentResult, error) {
	var result *BookAppointmentResult

	err := repository.WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Step 1 — Idempotency key insert
		var ikID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO idempotency_keys(id, tenant_id, key, endpoint, created_at)
			VALUES (gen_random_uuid(), $1, $2, 'appointment.book', NOW())
			ON CONFLICT (tenant_id, key, endpoint) DO NOTHING
			RETURNING id;
		`, tenantID, req.IdempotencyKey).Scan(&ikID)

		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("idempotency insert error: %w", err)
		}

		if errors.Is(err, pgx.ErrNoRows) {
			// Key exists, read response
			var respStatus *int
			var respBody []byte
			err := tx.QueryRow(ctx, `
				SELECT response_status, response_body
				FROM idempotency_keys
				WHERE tenant_id=$1 AND key=$2 AND endpoint='appointment.book' AND expires_at > NOW();
			`, tenantID, req.IdempotencyKey).Scan(&respStatus, &respBody)
			if err != nil {
				return err
			}
			if respStatus != nil {
				// Replay
				var cachedRes BookAppointmentResult
				if err := json.Unmarshal(respBody, &cachedRes); err != nil {
					return err
				}
				result = &cachedRes
				return nil
			}
			return errors.New("request still in flight")
		}

		// Step 2 — Validate location (name/address also feed the WhatsApp templates)
		var timezone, locationName string
		var address *string
		var isActive bool
		err = tx.QueryRow(ctx, `SELECT timezone, name, address, is_active FROM locations WHERE id=$1 AND tenant_id=$2`,
			req.LocationID, tenantID).Scan(&timezone, &locationName, &address, &isActive)
		if err != nil {
			return err
		}
		if !isActive {
			return errors.New("location is inactive")
		}

		// Step 3 — Validate shop open at the requested time
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			loc = time.Local
		}
		localStartAt := req.ScheduledStartAt.In(loc)
		if localStartAt.Before(time.Now().In(loc)) {
			return errors.New("scheduled time is in the past")
		}
		dow := int(localStartAt.Weekday())
		var opensAt, closesAt *time.Time
		var isOpen bool
		err = tx.QueryRow(ctx, `
			SELECT is_open, opens_at, closes_at FROM location_hours
			WHERE location_id=$1 AND day_of_week=$2
		`, req.LocationID, dow).Scan(&isOpen, &opensAt, &closesAt)
		if errors.Is(err, pgx.ErrNoRows) {
			isOpen = false
			err = nil
		}
		if err != nil {
			return err
		}
		if !isOpen || opensAt == nil || closesAt == nil {
			return errors.New("shop is closed on this day")
		}
		startMin := localStartAt.Hour()*60 + localStartAt.Minute()
		opensMin := opensAt.Hour()*60 + opensAt.Minute()
		closesMin := closesAt.Hour()*60 + closesAt.Minute()
		if startMin < opensMin || startMin >= closesMin {
			return errors.New("scheduled time is outside business hours")
		}

		// Step 4 — Validate variants + compute totals
		var totalDuration int
		var totalPrice int
		var services []ServiceSummary
		for _, vid := range req.VariantIDs {
			var price int
			var duration int
			var name string
			err = tx.QueryRow(ctx, `
				SELECT price_paise, duration_minutes, name FROM service_variants WHERE id=$1 AND is_active=true
			`, vid).Scan(&price, &duration, &name)
			if err != nil {
				return err
			}
			totalDuration += duration
			totalPrice += price
			services = append(services, ServiceSummary{Name: name, DurationMinutes: duration})
		}

		// Step 5 — Barber overlap check
		if req.RequestedBarberID != nil {
			var overlap bool
			err = tx.QueryRow(ctx, `
				SELECT EXISTS(
				  SELECT 1 FROM appointments
				  WHERE requested_barber_id=$1
					AND status IN ('scheduled', 'checked_in')
					AND scheduled_start_at < $2::timestamp + ($3 * interval '1 minute')
					AND scheduled_start_at + (total_duration_minutes * interval '1 minute') > $2::timestamp
				)
			`, *req.RequestedBarberID, req.ScheduledStartAt, totalDuration).Scan(&overlap)
			if err != nil {
				return err
			}
			if overlap {
				return errors.New("barber has an overlapping appointment")
			}
		}

		// Step 6 — Upsert customer (column is customers.name; a NULL new name never
		// clobbers an existing one)
		var customerID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO customers (tenant_id, phone_number, name)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, phone_number) DO UPDATE
			SET name = COALESCE(EXCLUDED.name, customers.name)
			RETURNING id;
		`, tenantID, req.PhoneNumber, req.CustomerName).Scan(&customerID)
		if err != nil {
			return err
		}

		// Step 7 — Insert appointment
		var variantIDsStr []string
		for _, vid := range req.VariantIDs {
			variantIDsStr = append(variantIDsStr, vid.String())
		}
		variantJSON, _ := json.Marshal(variantIDsStr)

		var appointmentID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO appointments (
			  tenant_id, location_id, customer_id, requested_barber_id,
			  status, scheduled_start_at, scheduled_end_at, variant_ids, party_size, total_duration_minutes, idempotency_key
			) VALUES (
			  $1, $2, $3, $4,
			  'scheduled', $5::timestamptz, $5::timestamptz + ($6 * interval '1 minute'), $7, $8, $9, $10
			) RETURNING id;
		`, tenantID, req.LocationID, customerID, req.RequestedBarberID,
			req.ScheduledStartAt, totalDuration, variantJSON, req.PartySize, totalDuration, req.IdempotencyKey).Scan(&appointmentID)
		if err != nil {
			return err
		}

		// Appointment magic link token: base64url(apt_id:expires) + "." + base64url(HMAC).
		// The template URL button carries only the token as its dynamic suffix
		// (static prefix https://barberbase.in/q/appointment?t= lives in the template).
		// Valid until 2h past the scheduled end — the reminder (day before) must
		// still carry a live token, so a booking-time 23h TTL would be wrong here.
		expiresUnix := req.ScheduledStartAt.Add(time.Duration(totalDuration)*time.Minute + 2*time.Hour).Unix()
		payloadStr := fmt.Sprintf("%s:%d", appointmentID.String(), expiresUnix)
		payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payloadStr))

		mac := hmac.New(sha256.New, []byte(os.Getenv("HMAC_SECRET")))
		mac.Write([]byte(payloadB64))
		macB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

		token := payloadB64 + "." + macB64
		magicLink := "https://barberbase.in/q/appointment?t=" + token

		serviceNames := ""
		for i, sv := range services {
			if i > 0 {
				serviceNames += ", "
			}
			serviceNames += sv.Name
		}
		localStart := req.ScheduledStartAt.In(loc)
		aptIDStr := appointmentID.String()
		locAddr := locationName
		if address != nil && *address != "" {
			locAddr = *address
		}
		textParam := func(t string) map[string]interface{} {
			return map[string]interface{}{"type": "text", "text": t}
		}

		// Step 8 — Outbox: appointment confirmation (template 6: {{1}} shop,
		// {{2}} date, {{3}} time, {{4}} services, {{5}} duration)
		confPayload := map[string]interface{}{
			"template_code":       "bb_appointment_confirmed",
			"to":                  req.PhoneNumber,
			"location_id":         req.LocationID.String(),
			"language":            "en",
			"notification_type":   "appointment_confirmed",
			"customer_id":         customerID.String(),
			"source_type":         "appointment",
			"source_id":           aptIDStr,
			"components": []interface{}{
				map[string]interface{}{
					"type": "body",
					"parameters": []interface{}{
						textParam(locationName),
						textParam(localStart.Format("Mon, 2 Jan 2006")),
						textParam(localStart.Format("3:04 PM")),
						textParam(serviceNames),
						textParam(fmt.Sprintf("%d", totalDuration)),
					},
				},
				map[string]interface{}{
					"type": "button", "sub_type": "url", "index": 0,
					"parameters": []interface{}{textParam(token)},
				},
				map[string]interface{}{
					"type": "button", "sub_type": "quick_reply", "index": 1,
					"parameters": []interface{}{map[string]interface{}{"type": "payload", "payload": "CANCEL_APT:" + aptIDStr}},
				},
			},
		}
		confJSON, _ := json.Marshal(confPayload)
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events(tenant_id, type, payload, process_after)
			VALUES ($1, 'notification.send', $2, NOW())
		`, tenantID, confJSON)
		if err != nil {
			return err
		}

		// Step 9 — Outbox: reminder at 6 PM the day before (template 7: {{1}} shop,
		// {{2}} time, {{3}} services, {{4}} address). Skipped for same/next-day
		// bookings whose reminder time is already past — the confirmation suffices.
		localDayBefore := time.Date(localStart.Year(), localStart.Month(), localStart.Day()-1, 18, 0, 0, 0, loc)
		if localDayBefore.After(time.Now().In(loc)) {
			remPayload := map[string]interface{}{
				"template_code":       "bb_appointment_reminder",
				"to":                  req.PhoneNumber,
				"location_id":         req.LocationID.String(),
				"language":            "en",
				"notification_type":   "appointment_reminder",
				"customer_id":         customerID.String(),
				"source_type":         "appointment",
				"source_id":           aptIDStr,
				"components": []interface{}{
					map[string]interface{}{
						"type": "body",
						"parameters": []interface{}{
							textParam(locationName),
							textParam(localStart.Format("3:04 PM")),
							textParam(serviceNames),
							textParam(locAddr),
						},
					},
					map[string]interface{}{
						"type": "button", "sub_type": "url", "index": 0,
						"parameters": []interface{}{textParam(token)},
					},
					map[string]interface{}{
						"type": "button", "sub_type": "quick_reply", "index": 1,
						"parameters": []interface{}{map[string]interface{}{"type": "payload", "payload": "CANCEL_APT:" + aptIDStr}},
					},
				},
			}
			remJSON, _ := json.Marshal(remPayload)
			_, err = tx.Exec(ctx, `
				INSERT INTO outbox_events(tenant_id, type, payload, process_after)
				VALUES ($1, 'notification.send', $2, $3)
			`, tenantID, remJSON, localDayBefore.UTC())
			if err != nil {
				return err
			}
		}

		result = &BookAppointmentResult{
			AppointmentID:    appointmentID,
			ScheduledStartAt: req.ScheduledStartAt,
			Status:           "scheduled",
			Services:         services,
			TotalDurationMin: totalDuration,
			TotalPricePaise:  totalPrice,
			MagicLink:        magicLink,
		}

		// Step 10 — Store idempotency response
		resJSON, _ := json.Marshal(result)
		_, err = tx.Exec(ctx, `
			UPDATE idempotency_keys
			SET response_status=200, response_body=$1
			WHERE id=$2
		`, resJSON, ikID)
		if err != nil {
			return err
		}

		return nil
	})

	return result, err
}

func (r *QueueRepository) CheckInAppointment(
	ctx context.Context,
	tenantID, locationID, appointmentID uuid.UUID,
) (*CheckInAppointmentResult, error) {
	var res CheckInAppointmentResult

	err := repository.WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Step 0 - Location timezone drives the business date
		var timezone string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM locations WHERE id=$1 AND tenant_id=$2`,
			locationID, tenantID).Scan(&timezone); err != nil {
			return err
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			loc, _ = time.LoadLocation("Asia/Kolkata")
		}
		businessDateStr := time.Now().In(loc).Format("2006-01-02")

		_, err = tx.Exec(ctx, `
			INSERT INTO queue_sessions (tenant_id, location_id, business_date)
			VALUES ($1, $2, $3::DATE)
			ON CONFLICT (location_id, business_date) DO NOTHING
		`, tenantID, locationID, businessDateStr)
		if err != nil {
			return err
		}

		var sessionID uuid.UUID
		var lastTokenNumber int
		var prevQueueVersion int
		err = tx.QueryRow(ctx, `
			SELECT id, last_token_number, queue_version
			FROM queue_sessions
			WHERE location_id = $1 AND business_date = $2::DATE
			FOR UPDATE
		`, locationID, businessDateStr).Scan(&sessionID, &lastTokenNumber, &prevQueueVersion)
		if err != nil {
			return err
		}

		// Step 2 - Validate appointment
		var status string
		var customerID uuid.UUID
		var partySize int
		var totalDuration int
		var scheduledStartAt time.Time
		var requestedBarberID *uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT status, customer_id, party_size, total_duration_minutes, scheduled_start_at, requested_barber_id
			FROM appointments
			WHERE id = $1 AND tenant_id = $2 AND location_id = $3
		`, appointmentID, tenantID, locationID).Scan(&status, &customerID, &partySize, &totalDuration, &scheduledStartAt, &requestedBarberID)
		if err != nil {
			return err
		}
		if status != "scheduled" {
			return errors.New("appointment is not scheduled")
		}
		if scheduledStartAt.In(loc).Format("2006-01-02") != businessDateStr {
			return errors.New("appointment is not scheduled for today")
		}

		// Step 3 - Create visit
		var visitID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO visits (
				tenant_id, location_id, customer_id,
				entry_type, initiated_via, party_size, total_duration_minutes, status
			) VALUES (
				$1, $2, $3,
				'appointment', 'staff_dashboard', $4, $5, 'active'
			) RETURNING id
		`, tenantID, locationID, customerID, partySize, totalDuration).Scan(&visitID)
		if err != nil {
			return err
		}

		// Magic link: canonical token format (same generator as JoinQueue/webhook —
		// /q/status verification and the SSE stream both expect exactly this shape).
		expiresAt := time.Now().Add(23 * time.Hour) // Law 13: 23h
		magicToken := generateMagicLinkToken(customerID.String(), locationID.String(), visitID.String(), expiresAt, []byte(os.Getenv("HMAC_SECRET")))

		_, err = tx.Exec(ctx, `
			UPDATE visits
			SET magic_link_token_hash = $1, magic_link_expires_at = $2
			WHERE id = $3
		`, magicToken, expiresAt, visitID)
		if err != nil {
			return err
		}

		// Step 4 - Snapshot visit_services
		_, err = tx.Exec(ctx, `
			INSERT INTO visit_services (
				visit_id, service_variant_id,
				variant_name_snapshot, group_name_snapshot, category_name_snapshot,
				duration_minutes_snapshot, price_paise_snapshot, sort_order
			)
			SELECT 
				$1, sv.id,
				sv.name, sg.name, sc.name,
				sv.duration_minutes, sv.price_paise, CAST(elem.ordinality AS INT) - 1
			FROM (SELECT variant_ids FROM appointments WHERE id = $2) a
			CROSS JOIN LATERAL jsonb_array_elements_text(a.variant_ids) WITH ORDINALITY AS elem(vid, ordinality)
			JOIN service_variants sv ON sv.id = elem.vid::uuid
			JOIN service_groups sg ON sg.id = sv.group_id
			JOIN service_categories sc ON sc.id = sg.category_id
		`, visitID, appointmentID)
		if err != nil {
			return err
		}

		// Step 5 - Increment token and create queue_entry.
		// presence_state='arrived': check-in requires physical presence, which keeps
		// appointment entries structurally outside the watchdog's near-turn/auto-snooze
		// path (that path only ever touches 'remote'/'notified' entries). Unresponsive
		// appointment no-shows are handled at the appointments level — staff cancel or
		// the end-of-day job marking stale 'scheduled' rows 'no_show' — never by queue
		// auto-snooze. priority_group=50 protects the position over walk-ins (100).
		tokenNumber := lastTokenNumber + 1
		var entryID uuid.UUID
		// requested_barber_id MUST carry over: barber_specific dispatch matches only
		// requested_barber_id = caller (Law 12) — dropping it here strands the entry.
		err = tx.QueryRow(ctx, `
			INSERT INTO queue_entries (
				visit_id, queue_session_id, customer_id,
				token_number, state, presence_state, is_dispatchable,
				session_channel, priority_group, sort_key, remote_joined_at,
				requested_barber_id
			) VALUES (
				$1, $2, $3,
				$4, 'waiting', 'arrived', true,
				'whatsapp', 50, EXTRACT(EPOCH FROM NOW())::BIGINT, NOW(),
				$5
			) RETURNING id
		`, visitID, sessionID, customerID, tokenNumber, requestedBarberID).Scan(&entryID)
		if err != nil {
			return err
		}

		// Step 6 - Update appointment status
		_, err = tx.Exec(ctx, `
			UPDATE appointments
			SET status = 'checked_in'
			WHERE id = $1
		`, appointmentID)
		if err != nil {
			return err
		}

		// Step 7 - Increment queue_version (returned so the handler can broadcast — Law 8)
		var newQueueVersion int
		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET last_token_number = last_token_number + 1, queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version
		`, sessionID).Scan(&newQueueVersion)
		if err != nil {
			return err
		}

		res = CheckInAppointmentResult{
			VisitID:         visitID,
			QueueEntryID:    entryID,
			TokenNumber:     tokenNumber,
			NewQueueVersion: newQueueVersion,
			MagicLink:       "https://barberbase.in/q/status?t=" + magicToken,
		}

		return nil
	})

	return &res, err
}
