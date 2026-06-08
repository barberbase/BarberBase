package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/repository"
)

type Processor struct {
	pool        *pgxpool.Pool
	broadcaster SSEBroadcaster
}

func NewProcessor(pool *pgxpool.Pool, broadcaster SSEBroadcaster) *Processor {
	return &Processor{
		pool:        pool,
		broadcaster: broadcaster,
	}
}

type WebhookEventRow struct {
	ID              uuid.UUID
	Source          string
	ExternalEventID string
	EventType       string
	TenantID        *uuid.UUID
	LocationID      *uuid.UUID
	Payload         []byte
	Status          string
	Attempts        int
	LastError       *string
	LockedUntil     *time.Time
	CreatedAt       time.Time
	ProcessedAt     *time.Time
}

// Run blocks until ctx is cancelled.
func (p *Processor) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		row, err := p.claimEvent(ctx)
		if err != nil {
			log.Printf("[Error] failed to claim webhook event: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		if row == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		// Process outside transaction
		p.processWithRecovery(ctx, row)
	}
}

func (p *Processor) claimEvent(ctx context.Context) (*WebhookEventRow, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE webhook_events
		SET status = 'processing',
		    locked_until = NOW() + INTERVAL '30 seconds',
		    attempts = attempts + 1
		WHERE id = (
			SELECT id FROM webhook_events
			WHERE (
				status = 'pending'
				OR (status = 'failed' AND attempts < 10)
				OR (status = 'processing' AND locked_until < NOW())
			)
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, source, external_event_id, event_type, tenant_id, location_id, payload, status, attempts, last_error, locked_until, created_at, processed_at
	`

	var row WebhookEventRow
	err = tx.QueryRow(ctx, query).Scan(
		&row.ID, &row.Source, &row.ExternalEventID, &row.EventType, &row.TenantID, &row.LocationID,
		&row.Payload, &row.Status, &row.Attempts, &row.LastError, &row.LockedUntil, &row.CreatedAt, &row.ProcessedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	return &row, nil
}

func (p *Processor) processWithRecovery(ctx context.Context, row *WebhookEventRow) {
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("panic: %v", r)
			log.Printf("[Error] panic recovered while processing webhook event %s: %s", row.ID, errStr)
			p.markEventFailed(ctx, row.ID, errors.New(errStr))
		}
	}()

	err := p.processEvent(ctx, row)
	if err != nil {
		log.Printf("[Error] failed to process webhook event %s: %v", row.ID, err)
		p.markEventFailed(ctx, row.ID, err)
	} else {
		p.markEventProcessed(ctx, row.ID)
	}
}

func (p *Processor) markEventProcessed(ctx context.Context, id uuid.UUID) {
	query := `
		UPDATE webhook_events
		SET status = 'processed',
		    processed_at = NOW()
		WHERE id = $1
	`
	_, err := p.pool.Exec(ctx, query, id)
	if err != nil {
		log.Printf("[Error] failed to mark webhook event %s as processed: %v", id, err)
	}
}

func (p *Processor) markEventFailed(ctx context.Context, id uuid.UUID, errVal error) {
	query := `
		UPDATE webhook_events
		SET status = 'failed',
		    last_error = $1
		WHERE id = $2
	`
	_, err := p.pool.Exec(ctx, query, errVal.Error(), id)
	if err != nil {
		log.Printf("[Error] failed to mark webhook event %s as failed: %v", id, err)
	}
}

func (p *Processor) processEvent(ctx context.Context, row *WebhookEventRow) error {
	var raw bhejnaPayload
	if err := json.Unmarshal(row.Payload, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// Mode A (location_id is NULL) business-phone pre-filter
	if row.LocationID == nil {
		bhejnaFromPhoneEnv := repository.NormalizeE164(os.Getenv("BHEJNA_FROM_PHONE"))
		businessPhonePayload := repository.NormalizeE164(raw.BusinessPhoneNumber)
		if businessPhonePayload != bhejnaFromPhoneEnv {
			log.Printf("[Warning] Mode A business phone mismatch: payload '%s' vs env '%s'. Discarding event.", businessPhonePayload, bhejnaFromPhoneEnv)
			return nil // discard by returning nil (processed)
		}
	}

	classified, err := Classify(row.Payload, row.LocationID)
	if err != nil {
		return fmt.Errorf("classification failed: %w", err)
	}

	return p.dispatch(ctx, classified, row.Payload)
}

func (p *Processor) dispatch(ctx context.Context, msg ClassifiedMessage, rawPayload []byte) error {
	switch msg.Action {
	case ActionJoin:
		resolver := NewIntentResolver(p.pool, p.broadcaster, []byte(os.Getenv("HMAC_SECRET")), os.Getenv("BHEJNA_FROM_PHONE"))
		reply, err := resolver.ResolveJoin(ctx, msg)
		if err != nil {
			return err
		}
		if reply != "" {
			var tenantID *uuid.UUID
			if msg.LocationID != nil {
				var tID uuid.UUID
				queryLoc := `SELECT tenant_id FROM locations WHERE id = $1 AND is_active = true`
				errLoc := p.pool.QueryRow(ctx, queryLoc, msg.LocationID).Scan(&tID)
				if errLoc == nil {
					tenantID = &tID
				}
			}

			fromPhone := os.Getenv("BHEJNA_FROM_PHONE")
			if msg.LocationID != nil {
				var (
					whatsappMode  string
					businessPhone *string
				)
				queryLoc := `SELECT whatsapp_mode, business_whatsapp_number FROM locations WHERE id = $1 AND is_active = true`
				_ = p.pool.QueryRow(ctx, queryLoc, msg.LocationID).Scan(&whatsappMode, &businessPhone)
				if whatsappMode == "own_number" && businessPhone != nil && *businessPhone != "" {
					fromPhone = *businessPhone
				}
			}

			payloadMap := map[string]interface{}{
				"type":                "text",
				"to":                  msg.SenderPhone,
				"from_business_phone": fromPhone,
				"text": map[string]interface{}{
					"body": reply,
				},
			}
			payloadBytes, err := json.Marshal(payloadMap)
			if err != nil {
				return fmt.Errorf("failed to marshal JOIN response: %w", err)
			}

			queryInsertOutbox := `
				INSERT INTO outbox_events (tenant_id, type, payload, process_after)
				VALUES ($1, 'notification.send', $2, NOW())
			`
			_, err = p.pool.Exec(ctx, queryInsertOutbox, tenantID, payloadBytes)
			if err != nil {
				return fmt.Errorf("failed to insert JOIN response outbox event: %w", err)
			}
		}
		return nil

	case ActionOnTheWay:
		return p.handleOnTheWay(ctx, msg)

	case ActionCancel:
		return p.handleCancel(ctx, msg)

	case ActionCancelApt:
		return p.handleCancelApt(ctx, msg)

	case ActionRatingButton:
		return p.handleRatingButton(ctx, msg)

	case ActionPlainRating:
		return p.handlePlainRating(ctx, msg)

	case ActionOptOutButton, ActionStop:
		return p.handleStop(ctx, msg)

	case ActionStatusUpdated:
		log.Printf("[Debug] ActionStatusUpdated received payload size: %d", len(rawPayload))
		return nil

	case ActionUnknown:
		return p.handleUnknown(ctx, msg)

	default:
		return p.handleUnknown(ctx, msg)
	}
}

func (p *Processor) handleOnTheWay(ctx context.Context, msg ClassifiedMessage) error {
	entryID, err := uuid.Parse(msg.EntryID)
	if err != nil {
		log.Printf("[Debug] ActionOnTheWay: invalid entry ID '%s'", msg.EntryID)
		return nil
	}

	var (
		id            uuid.UUID
		state         string
		presenceState string
		locationID    uuid.UUID
		tenantID      uuid.UUID
	)

	// Resolve tenant_id and location_id from entity UUID chain instead of sender or slug
	query := `
		SELECT qe.id, qe.state, qe.presence_state,
		       qs.location_id, qs.tenant_id
		FROM queue_entries qe
		JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		WHERE qe.id = $1
	`
	err = p.pool.QueryRow(ctx, query, entryID).Scan(&id, &state, &presenceState, &locationID, &tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Debug] ActionOnTheWay: queue entry %s not found", entryID)
			return nil
		}
		return fmt.Errorf("failed to query queue entry: %w", err)
	}

	if state != "waiting" && state != "called" {
		return nil
	}

	if presenceState == "on_the_way" || presenceState == "arrived" {
		return nil
	}

	queryUpdate := `
		UPDATE queue_entries
		SET presence_state = 'on_the_way',
		    on_the_way_at  = NOW()
		WHERE id = $1
		  AND presence_state NOT IN ('on_the_way', 'arrived')
	`
	_, err = p.pool.Exec(ctx, queryUpdate, entryID)
	if err != nil {
		return fmt.Errorf("failed to update presence_state: %w", err)
	}

	return nil
}

func (p *Processor) handleCancel(ctx context.Context, msg ClassifiedMessage) error {
	entryID, err := uuid.Parse(msg.EntryID)
	if err != nil {
		log.Printf("[Debug] ActionCancel: invalid entry ID '%s'", msg.EntryID)
		return nil
	}

	var (
		id           uuid.UUID
		state        string
		tenantID     uuid.UUID
		locationID   uuid.UUID
		sessionID    uuid.UUID
		queueVersion int
	)

	// Resolve tenant_id and location_id from entity UUID chain
	query := `
		SELECT qe.id, qe.state, qs.tenant_id, qs.location_id, qs.id AS session_id, qs.queue_version
		FROM queue_entries qe
		JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		WHERE qe.id = $1
	`
	err = p.pool.QueryRow(ctx, query, entryID).Scan(&id, &state, &tenantID, &locationID, &sessionID, &queueVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Debug] ActionCancel: queue entry %s not found", entryID)
			return nil
		}
		return fmt.Errorf("failed to query queue entry for cancel: %w", err)
	}

	if state != "waiting" && state != "called" {
		return nil
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin cancel tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock queue_sessions (Law 1)
	queryLock := `
		SELECT queue_version FROM queue_sessions
		WHERE id = $1
		FOR UPDATE
	`
	var lockedVersion int
	err = tx.QueryRow(ctx, queryLock, sessionID).Scan(&lockedVersion)
	if err != nil {
		return fmt.Errorf("failed to lock session: %w", err)
	}

	// Update queue entry
	queryUpdateEntry := `
		UPDATE queue_entries
		SET state = 'cancelled'
		WHERE id = $1 AND state IN ('waiting', 'called')
	`
	_, err = tx.Exec(ctx, queryUpdateEntry, entryID)
	if err != nil {
		return fmt.Errorf("failed to cancel queue entry: %w", err)
	}

	// Update queue session
	var newVersion int
	queryUpdateSession := `
		UPDATE queue_sessions
		SET queue_version = queue_version + 1
		WHERE id = $1
		RETURNING queue_version
	`
	err = tx.QueryRow(ctx, queryUpdateSession, sessionID).Scan(&newVersion)
	if err != nil {
		return fmt.Errorf("failed to increment queue version: %w", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit cancel tx: %w", err)
	}

	// Broadcast (Law 8)
	p.broadcaster.Broadcast(locationID, newVersion)

	return nil
}

func (p *Processor) handleRatingButton(ctx context.Context, msg ClassifiedMessage) error {
	visitID, err := uuid.Parse(msg.VisitID)
	if err != nil {
		log.Printf("[Debug] ActionRatingButton: invalid visit ID '%s'", msg.VisitID)
		return nil
	}

	var (
		feedbackRequestID uuid.UUID
		tenantID          uuid.UUID
		locationID        uuid.UUID
		vID               uuid.UUID
		customerID        *uuid.UUID
	)

	// Resolve tenant_id and location_id from entity UUID chain (feedback_requests)
	query := `
		SELECT fr.id, fr.tenant_id, fr.location_id, fr.visit_id, fr.customer_id
		FROM feedback_requests fr
		WHERE fr.visit_id = $1
		  AND fr.status = 'sent'
		  AND fr.channel = 'whatsapp'
		LIMIT 1
	`
	err = p.pool.QueryRow(ctx, query, visitID).Scan(&feedbackRequestID, &tenantID, &locationID, &vID, &customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Debug] ActionRatingButton: feedback request not found or not in sent status for visit %s", visitID)
			return nil
		}
		return fmt.Errorf("failed to query feedback request: %w", err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin rating tx: %w", err)
	}
	defer tx.Rollback(ctx)

	queryInsertResponse := `
		INSERT INTO feedback_responses (
			tenant_id, location_id, feedback_request_id, visit_id,
			customer_id, rating, source
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, 'whatsapp'
		)
		ON CONFLICT (tenant_id, feedback_request_id) DO NOTHING
	`
	_, err = tx.Exec(ctx, queryInsertResponse, tenantID, locationID, feedbackRequestID, visitID, customerID, msg.Rating)
	if err != nil {
		return fmt.Errorf("failed to insert feedback response: %w", err)
	}

	queryUpdateRequest := `
		UPDATE feedback_requests
		SET status = 'responded',
		    updated_at = NOW()
		WHERE id = $1
	`
	_, err = tx.Exec(ctx, queryUpdateRequest, feedbackRequestID)
	if err != nil {
		return fmt.Errorf("failed to update feedback request status: %w", err)
	}

	return tx.Commit(ctx)
}

func (p *Processor) handlePlainRating(ctx context.Context, msg ClassifiedMessage) error {
	var (
		feedbackRequestID uuid.UUID
		tenantID          uuid.UUID
		locationID        uuid.UUID
		visitID           uuid.UUID
		customerID        *uuid.UUID
	)

	if msg.LocationID != nil {
		// Mode B (tenant_id known from location)
		var tID uuid.UUID
		queryTenant := `SELECT tenant_id FROM locations WHERE id = $1 AND is_active = true`
		err := p.pool.QueryRow(ctx, queryTenant, msg.LocationID).Scan(&tID)
		if err != nil {
			log.Printf("[Debug] ActionPlainRating: failed to resolve tenant for location %s: %v", msg.LocationID, err)
			return nil
		}
		tenantID = tID
		locationID = *msg.LocationID

		queryRating := `
			SELECT fr.id, fr.visit_id, fr.customer_id
			FROM feedback_requests fr
			JOIN visits v ON v.id = fr.visit_id
			JOIN customers c ON c.id = v.customer_id
			WHERE fr.tenant_id = $1
			  AND c.phone_number = $2
			  AND c.merged_into_customer_id IS NULL
			  AND fr.status = 'sent'
			ORDER BY fr.sent_at DESC
			LIMIT 1
		`
		err = p.pool.QueryRow(ctx, queryRating, tenantID, msg.SenderPhone).Scan(&feedbackRequestID, &visitID, &customerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("[Debug] ActionPlainRating (Mode B): feedback request not found for phone %s", msg.SenderPhone)
				return nil
			}
			return fmt.Errorf("failed to query feedback request: %w", err)
		}
	} else {
		// Mode A (tenant_id unknown — cross-tenant lookup)
		queryRating := `
			SELECT fr.id, fr.tenant_id, fr.location_id, fr.visit_id, fr.customer_id
			FROM feedback_requests fr
			JOIN visits v ON v.id = fr.visit_id
			JOIN customers c ON c.id = v.customer_id
			WHERE c.phone_number = $1
			  AND c.merged_into_customer_id IS NULL
			  AND fr.status = 'sent'
			ORDER BY fr.sent_at DESC
			LIMIT 1
		`
		err := p.pool.QueryRow(ctx, queryRating, msg.SenderPhone).Scan(&feedbackRequestID, &tenantID, &locationID, &visitID, &customerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("[Debug] ActionPlainRating (Mode A): feedback request not found for phone %s", msg.SenderPhone)
				return nil
			}
			return fmt.Errorf("failed to query feedback request: %w", err)
		}
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin rating tx: %w", err)
	}
	defer tx.Rollback(ctx)

	queryInsertResponse := `
		INSERT INTO feedback_responses (
			tenant_id, location_id, feedback_request_id, visit_id,
			customer_id, rating, source
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, 'whatsapp'
		)
		ON CONFLICT (tenant_id, feedback_request_id) DO NOTHING
	`
	_, err = tx.Exec(ctx, queryInsertResponse, tenantID, locationID, feedbackRequestID, visitID, customerID, msg.Rating)
	if err != nil {
		return fmt.Errorf("failed to insert feedback response: %w", err)
	}

	queryUpdateRequest := `
		UPDATE feedback_requests
		SET status = 'responded',
		    updated_at = NOW()
		WHERE id = $1
	`
	_, err = tx.Exec(ctx, queryUpdateRequest, feedbackRequestID)
	if err != nil {
		return fmt.Errorf("failed to update feedback request status: %w", err)
	}

	return tx.Commit(ctx)
}

func (p *Processor) handleStop(ctx context.Context, msg ClassifiedMessage) error {
	var targets []struct {
		customerID uuid.UUID
		tenantID   uuid.UUID
	}

	if msg.LocationID != nil {
		var tenantID uuid.UUID
		queryTenant := `SELECT tenant_id FROM locations WHERE id = $1 AND is_active = true`
		err := p.pool.QueryRow(ctx, queryTenant, msg.LocationID).Scan(&tenantID)
		if err != nil {
			return fmt.Errorf("failed to query tenant for location %s: %w", msg.LocationID, err)
		}
		customerID, err := repository.ResolveOrCreateCustomer(ctx, p.pool, tenantID, msg.SenderPhone, msg.BSUID, msg.DisplayName)
		if err != nil {
			return fmt.Errorf("failed to resolve customer: %w", err)
		}
		targets = append(targets, struct {
			customerID uuid.UUID
			tenantID   uuid.UUID
		}{customerID, tenantID})
	} else {
		// Cross-tenant STOP
		query := `
			SELECT id, tenant_id FROM customers
			WHERE phone_number = $1
			  AND merged_into_customer_id IS NULL
		`
		rows, err := p.pool.Query(ctx, query, msg.SenderPhone)
		if err != nil {
			return fmt.Errorf("failed to query customers for STOP: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var t struct {
				customerID uuid.UUID
				tenantID   uuid.UUID
			}
			if err := rows.Scan(&t.customerID, &t.tenantID); err == nil {
				targets = append(targets, t)
			}
		}
	}

	for _, target := range targets {
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin STOP tx: %w", err)
		}
		defer tx.Rollback(ctx)

		queryConsent := `
			INSERT INTO customer_consents (tenant_id, customer_id, channel, consent_type, status, source)
			VALUES ($1, $2, 'whatsapp', 'marketing', 'opted_out', 'whatsapp_stop')
			ON CONFLICT (tenant_id, customer_id, channel, consent_type)
			DO UPDATE SET status='opted_out', captured_at=NOW()
		`
		_, err = tx.Exec(ctx, queryConsent, target.tenantID, target.customerID)
		if err != nil {
			return fmt.Errorf("failed to insert customer consent opt-out: %w", err)
		}

		queryCustomer := `
			UPDATE customers
			SET marketing_opt_in=false, marketing_opt_out_at=NOW(), updated_at=NOW()
			WHERE id=$1
		`
		_, err = tx.Exec(ctx, queryCustomer, target.customerID)
		if err != nil {
			return fmt.Errorf("failed to update customer marketing status: %w", err)
		}

		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit STOP tx: %w", err)
		}
	}

	return nil
}

func (p *Processor) handleCancelApt(ctx context.Context, msg ClassifiedMessage) error {
	log.Printf("[Info] ActionCancelApt received for appointment %s (Deferred to Phase 1.5)", msg.AptID)
	return nil
}

func (p *Processor) handleUnknown(ctx context.Context, msg ClassifiedMessage) error {
	var tenantID *uuid.UUID
	fromPhone := os.Getenv("BHEJNA_FROM_PHONE")

	if msg.LocationID != nil {
		var (
			tID           uuid.UUID
			whatsappMode  string
			businessPhone *string
		)
		queryLoc := `SELECT tenant_id, whatsapp_mode, business_whatsapp_number FROM locations WHERE id = $1 AND is_active = true`
		err := p.pool.QueryRow(ctx, queryLoc, msg.LocationID).Scan(&tID, &whatsappMode, &businessPhone)
		if err == nil {
			tenantID = &tID
			if whatsappMode == "own_number" && businessPhone != nil && *businessPhone != "" {
				fromPhone = *businessPhone
			}
		}
	}

	payloadMap := map[string]interface{}{
		"type":                "text",
		"to":                  msg.SenderPhone,
		"from_business_phone": fromPhone,
		"text": map[string]interface{}{
			"body": "Reply JOIN <shopname> <code> to join the queue. Reply STOP to opt out.",
		},
	}
	payloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return fmt.Errorf("failed to marshal unknown response outbox payload: %w", err)
	}

	queryInsertOutbox := `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1, 'notification.send', $2, NOW())
	`
	_, err = p.pool.Exec(ctx, queryInsertOutbox, tenantID, payloadBytes)
	if err != nil {
		return fmt.Errorf("failed to insert unknown response outbox event: %w", err)
	}

	return nil
}
