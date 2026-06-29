package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/repository"
)

type IntentResolver struct {
	pool            *pgxpool.Pool
	broadcaster     SSEBroadcaster
	hmacSecret      []byte // from config (HMAC_SECRET env var)
	bhejnaFromPhone string // env.BHEJNA_FROM_PHONE (Mode A from-phone)
}

func NewIntentResolver(pool *pgxpool.Pool, broadcaster SSEBroadcaster, hmacSecret []byte, bhejnaFromPhone string) *IntentResolver {
	return &IntentResolver{
		pool:            pool,
		broadcaster:     broadcaster,
		hmacSecret:      hmacSecret,
		bhejnaFromPhone: bhejnaFromPhone,
	}
}

// ResolveJoin executes the full JOIN flow.
// Returns (whatsappReply string, err error).
// whatsappReply == "" means no reply needed (outbox handles notification).
// A non-empty reply means send this text back to the customer via Bhejna plain-text API.
// err != nil means a retryable processing failure — do not mark webhook_event processed.
func (r *IntentResolver) ResolveJoin(ctx context.Context, msg ClassifiedMessage) (string, error) {
	// Step 1 — Load checkin_intent + location
	var (
		intentID             uuid.UUID
		tenantID             uuid.UUID
		locationID           uuid.UUID
		intentStatus         string
		expiresAt            time.Time
		shopStatusAtCreation string
		variantIDsJSON       []byte
		partySize            int
		customerName         *string
		locationSlug         string
		locationName         string
		timezone             string
		locationIsActive     bool
		whatsappMode         string
		businessPhone        *string
	)

	// No status filter in SQL — check in Go so expired/resolved give distinct log lines.
	queryIntent := `
		SELECT ci.id, ci.tenant_id, ci.location_id, ci.status, ci.expires_at,
		       ci.shop_status_at_creation, ci.variant_ids, ci.party_size, ci.customer_name,
		       l.slug, l.name AS location_name, l.timezone, l.is_active,
		       l.whatsapp_mode, l.business_whatsapp_number
		FROM checkin_intents ci
		JOIN locations l ON l.id = ci.location_id
		WHERE ci.token_code = $1
		LIMIT 1
	`
	err := r.pool.QueryRow(ctx, queryIntent, msg.TokenCode).Scan(
		&intentID, &tenantID, &locationID, &intentStatus, &expiresAt,
		&shopStatusAtCreation, &variantIDsJSON, &partySize, &customerName,
		&locationSlug, &locationName, &timezone, &locationIsActive,
		&whatsappMode, &businessPhone,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[JOIN] token_code '%s' truly absent from DB (from '%s', slug '%s')", msg.TokenCode, msg.SenderPhone, msg.SlugFromBody)
			return "Link expired or invalid", nil
		}
		return "", fmt.Errorf("failed to load checkin intent: %w", err)
	}

	// Status gate — checked in Go so each outcome has its own log line.
	switch intentStatus {
	case "resolved":
		// Bhejna redelivered a message whose intent was already consumed. Silent
		// success: the queue_entry already exists, the customer is already in.
		log.Printf("[JOIN] token_code '%s' already resolved (duplicate Bhejna delivery) for location '%s'", msg.TokenCode, locationSlug)
		return "", nil
	case "expired", "rejected":
		log.Printf("[JOIN] token_code '%s' status=%s for location '%s'", msg.TokenCode, intentStatus, locationSlug)
		return "This link has already been used or has expired. Please scan the QR code again.", nil
	case "created":
		// fall through
	default:
		log.Printf("[JOIN] token_code '%s' unexpected status='%s' for location '%s'", msg.TokenCode, intentStatus, locationSlug)
		return "Link expired or invalid", nil
	}

	// Step 2 — Slug validation (optional, non-fatal)
	if msg.SlugFromBody != "" {
		if !strings.EqualFold(strings.TrimSpace(locationSlug), strings.TrimSpace(msg.SlugFromBody)) {
			log.Printf("[Warning] slug mismatch: expected '%s', got '%s'. token_code '%s' is authoritative.", locationSlug, msg.SlugFromBody, msg.TokenCode)
		}
	}

	// Step 3 — Expiry check
	if !expiresAt.After(time.Now()) {
		log.Printf("[JOIN] token_code '%s' expired at %s (from '%s')", msg.TokenCode, expiresAt.Format(time.RFC3339), msg.SenderPhone)
		return "Link expired or invalid", nil
	}

	// Step 4 — Shop status gate
	if shopStatusAtCreation != "open" && shopStatusAtCreation != "closing_soon" {
		log.Printf("[JOIN] token_code '%s' blocked: shop_status_at_creation='%s' for location '%s'", msg.TokenCode, shopStatusAtCreation, locationSlug)
		return "This shop isn't accepting new walk-ins right now", nil
	}
	if !locationIsActive {
		log.Printf("[JOIN] token_code '%s' blocked: location '%s' is inactive", msg.TokenCode, locationSlug)
		return "This shop isn't available", nil
	}

	// Step 5 — Customer resolution (OUTSIDE main transaction)
	displayName := msg.DisplayName
	if displayName == "" && customerName != nil && *customerName != "" {
		displayName = *customerName
	}
	customerID, err := repository.ResolveOrCreateCustomer(ctx, r.pool, tenantID, msg.SenderPhone, msg.BSUID, displayName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve or create customer: %w", err)
	}

	// Step 6 — Main transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 6a — Queue session upsert-then-lock
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		// fallback to Asia/Kolkata default timezone per schema
		loc, err = time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.Local
		}
	}
	businessDateStr := time.Now().In(loc).Format("2006-01-02")

	// Upsert session
	querySessionUpsert := `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date)
		VALUES ($1, $2, $3::DATE)
		ON CONFLICT (location_id, business_date) DO NOTHING
	`
	_, err = tx.Exec(ctx, querySessionUpsert, tenantID, locationID, businessDateStr)
	if err != nil {
		return "", fmt.Errorf("failed to upsert queue session: %w", err)
	}

	// Lock session FOR UPDATE
	var (
		sessionID        uuid.UUID
		lastTokenNumber  int
		prevQueueVersion int
	)
	querySessionLock := `
		SELECT id, last_token_number, queue_version
		FROM queue_sessions
		WHERE location_id = $1
		  AND business_date = $2::DATE
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, querySessionLock, locationID, businessDateStr).Scan(&sessionID, &lastTokenNumber, &prevQueueVersion)
	if err != nil {
		return "", fmt.Errorf("failed to lock queue session: %w", err)
	}

	// 6b — Duplicate active entry guard
	var existsActive bool
	queryActiveCheck := `
		SELECT EXISTS(
			SELECT 1 FROM queue_entries qe
			JOIN visits v ON v.id = qe.visit_id
			WHERE qe.queue_session_id = $1
			  AND v.customer_id = $2
			  AND qe.state IN ('waiting', 'called', 'in_progress')
		)
	`
	err = tx.QueryRow(ctx, queryActiveCheck, sessionID, customerID).Scan(&existsActive)
	if err != nil {
		return "", fmt.Errorf("failed to check duplicate active entry: %w", err)
	}
	if existsActive {
		return "You already have an active spot in this queue", nil
	}

	// 6c — Snapshot variant data
	var variantIDs []string
	if len(variantIDsJSON) > 0 {
		_ = json.Unmarshal(variantIDsJSON, &variantIDs)
	}

	type variantSnapshot struct {
		variantID       uuid.UUID
		variantName     string
		groupName       string
		categoryName    string
		durationMinutes int
		pricePaise      int
	}

	var snapshots []variantSnapshot
	for _, vidStr := range variantIDs {
		vid, err := uuid.Parse(vidStr)
		if err != nil {
			continue
		}

		var snap variantSnapshot
		queryVariant := `
			SELECT sv.name AS variant_name, sg.name AS group_name, sc.name AS category_name,
			       sv.duration_minutes, sv.price_paise, sv.id AS variant_id
			FROM service_variants sv
			JOIN service_groups sg ON sg.id = sv.group_id
			JOIN service_categories sc ON sc.id = sg.category_id
			WHERE sv.id = $1
			  AND sv.tenant_id = $2
			  AND sv.is_active = true
		`
		err = tx.QueryRow(ctx, queryVariant, vid, tenantID).Scan(
			&snap.variantName, &snap.groupName, &snap.categoryName,
			&snap.durationMinutes, &snap.pricePaise, &snap.variantID,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // skip deleted/inactive variants silently
			}
			return "", fmt.Errorf("failed to snapshot service variant %s: %w", vidStr, err)
		}
		snapshots = append(snapshots, snap)
	}

	totalDuration := 0
	for _, snap := range snapshots {
		totalDuration += snap.durationMinutes
	}
	if totalDuration == 0 {
		totalDuration = 30 // fallback duration (visits.total_duration_minutes CHECK > 0)
	}

	// 6d — INSERT visits
	var visitID uuid.UUID
	queryInsertVisit := `
		INSERT INTO visits (
			tenant_id, location_id, customer_id,
			entry_type, initiated_via, party_size, total_duration_minutes, status
		) VALUES (
			$1, $2, $3,
			'walk_in', 'whatsapp', $4, $5, 'active'
		) RETURNING id
	`
	err = tx.QueryRow(ctx, queryInsertVisit, tenantID, locationID, customerID, partySize, totalDuration).Scan(&visitID)
	if err != nil {
		return "", fmt.Errorf("failed to insert visit: %w", err)
	}

	// 6e — INSERT visit_services (Law 10: written once, never updated)
	for _, snap := range snapshots {
		// Find sort order based on index in variantIDs
		sortOrder := 0
		for idx, vidStr := range variantIDs {
			if vidStr == snap.variantID.String() {
				sortOrder = idx
				break
			}
		}

		queryInsertService := `
			INSERT INTO visit_services (
				visit_id, service_variant_id,
				variant_name_snapshot, group_name_snapshot, category_name_snapshot,
				duration_minutes_snapshot, price_paise_snapshot, sort_order
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8
			)
		`
		_, err = tx.Exec(ctx, queryInsertService,
			visitID, snap.variantID,
			snap.variantName, snap.groupName, snap.categoryName,
			snap.durationMinutes, snap.pricePaise, sortOrder,
		)
		if err != nil {
			return "", fmt.Errorf("failed to insert visit service: %w", err)
		}
	}

	// 6f — INSERT queue_entries
	var entryID uuid.UUID
	tokenNumber := lastTokenNumber + 1
	queryInsertEntry := `
		INSERT INTO queue_entries (
			visit_id, queue_session_id, customer_id,
			token_number, state, presence_state, is_dispatchable,
			session_channel, priority_group, sort_key, remote_joined_at
		) VALUES (
			$1, $2, $3,
			$4,
			'waiting', 'remote', true,
			'whatsapp', 100, EXTRACT(EPOCH FROM NOW())::BIGINT, NOW()
		) RETURNING id
	`
	err = tx.QueryRow(ctx, queryInsertEntry, visitID, sessionID, customerID, tokenNumber).Scan(&entryID)
	if err != nil {
		return "", fmt.Errorf("failed to insert queue entry: %w", err)
	}

	// 6g — UPDATE queue_sessions
	var newQueueVersion int
	queryUpdateSession := `
		UPDATE queue_sessions
		SET last_token_number = last_token_number + 1,
		    queue_version     = queue_version + 1
		WHERE id = $1
		RETURNING queue_version
	`
	err = tx.QueryRow(ctx, queryUpdateSession, sessionID).Scan(&newQueueVersion)
	if err != nil {
		return "", fmt.Errorf("failed to update queue session: %w", err)
	}

	// 6h — UPDATE checkin_intents
	queryUpdateIntent := `
		UPDATE checkin_intents
		SET status = 'resolved',
		    resolved_queue_entry_id = $1
		WHERE id = $2
	`
	_, err = tx.Exec(ctx, queryUpdateIntent, entryID, intentID)
	if err != nil {
		return "", fmt.Errorf("failed to update checkin intent: %w", err)
	}

	// 6i — Generate magic link token (stored directly as hash — matches commands.go pattern)
	expiresML := time.Now().Add(23 * time.Hour) // Law 13: hardcoded 23h
	tokenStr := generateMagicLinkToken(customerID, locationID, visitID, expiresML, r.hmacSecret)

	queryUpdateVisitML := `
		UPDATE visits
		SET magic_link_token_hash = $1,
		    magic_link_expires_at = $2
		WHERE id = $3
	`
	_, err = tx.Exec(ctx, queryUpdateVisitML, tokenStr, expiresML, visitID)
	if err != nil {
		return "", fmt.Errorf("failed to update magic link on visit: %w", err)
	}

	// 6j — INSERT outbox_events (INSIDE tx — Law 7)
	fromPhone := r.bhejnaFromPhone
	if whatsappMode == "own_number" && businessPhone != nil && *businessPhone != "" {
		fromPhone = *businessPhone
	}

	// Get active queue position before this entry
	var queuePosition int
	queryPosition := `
		SELECT COUNT(*) FROM queue_entries
		WHERE queue_session_id = $1
		  AND state IN ('waiting', 'called', 'in_progress')
	`
	err = tx.QueryRow(ctx, queryPosition, sessionID).Scan(&queuePosition)
	if err != nil {
		return "", fmt.Errorf("failed to query queue position: %w", err)
	}

	// queuePosition COUNT runs after the new entry is inserted, so subtract 1 for people_ahead.
	peopleAhead := queuePosition - 1
	estWaitMinutes := peopleAhead * totalDuration

	outboxPayload := map[string]interface{}{
		"template_code":       "bb_queue_joined",
		"to":                  msg.SenderPhone, // empty string if masked
		"from_business_phone": fromPhone,
		"location_id":         locationID.String(), // required by handler for credential resolution
		"notification_type":   "queue_joined",
		"components": []interface{}{
			map[string]interface{}{
				"type": "body",
				"parameters": []interface{}{
					map[string]interface{}{"type": "text", "text": locationName},
					map[string]interface{}{"type": "text", "text": strconv.Itoa(tokenNumber)},
					map[string]interface{}{"type": "text", "text": strconv.Itoa(peopleAhead)},
					map[string]interface{}{"type": "text", "text": strconv.Itoa(estWaitMinutes)},
				},
			},
			map[string]interface{}{
				"type":     "button",
				"sub_type": "url",
				"index":    0,
				"parameters": []interface{}{
					map[string]interface{}{"type": "text", "text": tokenStr},
				},
			},
			map[string]interface{}{
				"type":     "button",
				"sub_type": "quick_reply",
				"index":    1,
				"parameters": []interface{}{
					map[string]interface{}{"type": "payload", "payload": "CANCEL:" + entryID.String()},
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(outboxPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal outbox event payload: %w", err)
	}

	queryInsertOutbox := `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1, 'notification.send', $2, NOW())
	`
	_, err = tx.Exec(ctx, queryInsertOutbox, tenantID, payloadBytes)
	if err != nil {
		return "", fmt.Errorf("failed to insert outbox event: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Step 7 — SSE broadcast (AFTER COMMIT — Law 8)
	r.broadcaster.Broadcast(locationID, newQueueVersion)

	return "", nil
}

// generateMagicLinkToken matches commands.go format: HMAC of pipe-delimited payload, base64url-encoded.
// The returned token is stored directly as magic_link_token_hash and passed as the button URL suffix.
func generateMagicLinkToken(customerID, locationID, visitID uuid.UUID, expiresAt time.Time, hmacSecret []byte) string {
	payload := customerID.String() + "|" + locationID.String() + "|" + visitID.String() + "|" + strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, hmacSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// GenerateTokenCode returns a random 6-character uppercase alphanumeric code.
// Used by createCheckinIntent to populate checkin_intents.token_code.
// Entropy: crypto/rand. Modulo bias on 256-byte values over 36-char alphabet is
// acceptable for a short-lived 23h display token (bias ≈ 0.5%).
//
// The caller is responsible for retry on unique constraint violation (INSERT conflict).
// Max 3 retries recommended; 5xx on persistent collision (astronomically unlikely).
func GenerateTokenCode() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("GenerateTokenCode: entropy failure: %w", err)
	}
	result := make([]byte, 6)
	for i, v := range b {
		result[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(result), nil
}
