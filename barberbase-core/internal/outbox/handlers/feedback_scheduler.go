package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/config"
)

type FeedbackScheduler struct {
	pool *pgxpool.Pool
	cfg  *config.Config
}

func NewFeedbackScheduler(pool *pgxpool.Pool, cfg *config.Config) *FeedbackScheduler {
	return &FeedbackScheduler{
		pool: pool,
		cfg:  cfg,
	}
}

type FeedbackSchedulePayload struct {
	VisitID    string `json:"visit_id"`
	TenantID   string `json:"tenant_id"`
	LocationID string `json:"location_id"`
}

func (fs *FeedbackScheduler) Handle(ctx context.Context, pool *pgxpool.Pool, event *OutboxEvent) error {
	var payload FeedbackSchedulePayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return newTerminalError("failed to unmarshal feedback schedule payload: %v", err)
	}

	visitID, err := uuid.Parse(payload.VisitID)
	if err != nil {
		return newTerminalError("invalid visit_id: %v", err)
	}
	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		return newTerminalError("invalid tenant_id: %v", err)
	}
	locationID, err := uuid.Parse(payload.LocationID)
	if err != nil {
		return newTerminalError("invalid location_id: %v", err)
	}

	// 2. IDEMPOTENCY GUARD (read-only, before opening a write tx)
	var existingID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT id FROM feedback_requests
		WHERE tenant_id = $1 AND visit_id = $2 AND channel = 'whatsapp'
	`, tenantID, visitID).Scan(&existingID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to pre-check feedback_requests: %w", err)
	}

	// 3. BEGIN TRANSACTION
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 4. DATA LOOKUP — single query
	var (
		customerID             uuid.UUID
		customerPhone          *string
		assignedBarberID       *uuid.UUID
		staffName              string
		shopName               string
		whatsappMode           string
		businessWhatsAppNumber *string
	)

	query := `
		SELECT
			v.customer_id,
			c.phone_number                     AS customer_phone,
			qe.assigned_barber_id,
			COALESCE(sm.name, 'Your barber')   AS staff_name,
			l.name                             AS shop_name,
			l.whatsapp_mode,
			l.business_whatsapp_number
		FROM visits v
		JOIN customers c      ON c.id = v.customer_id
							 AND c.merged_into_customer_id IS NULL
		JOIN locations l      ON l.id = $1
		LEFT JOIN queue_entries qe ON qe.visit_id = v.id
		LEFT JOIN staff_members sm ON sm.id = qe.assigned_barber_id
								   AND sm.is_active = true
		WHERE v.id        = $2
		  AND v.tenant_id = $3
	`

	err = tx.QueryRow(ctx, query, locationID, visitID, tenantID).Scan(
		&customerID,
		&customerPhone,
		&assignedBarberID,
		&staffName,
		&shopName,
		&whatsappMode,
		&businessWhatsAppNumber,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return newTerminalError("visit or customer not found for feedback schedule: %v", err)
		}
		return err
	}

	if customerPhone == nil || *customerPhone == "" {
		_ = tx.Rollback(ctx)
		return nil
	}

	// 5. INSERT INTO feedback_requests
	var feedbackRequestID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO feedback_requests (
			tenant_id, location_id, visit_id, customer_id, staff_member_id,
			channel, status, scheduled_at
		) VALUES (
			$1, $2, $3, $4, $5,
			'whatsapp', 'scheduled', $6
		)
		ON CONFLICT (tenant_id, visit_id, channel) DO NOTHING
		RETURNING id
	`, tenantID, locationID, visitID, customerID, assignedBarberID, event.ProcessAfter).Scan(&feedbackRequestID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return nil
		}
		return err
	}

	// 6. RESOLVE from_business_phone
	fromBusinessPhone := ""
	if whatsappMode == "own_number" && businessWhatsAppNumber != nil && *businessWhatsAppNumber != "" {
		fromBusinessPhone = *businessWhatsAppNumber
	} else {
		fromBusinessPhone = fs.cfg.BhejnaFromPhone
	}

	// 7. BUILD notification.send payload
	type feedbackComponentParam struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type feedbackComponent struct {
		Type       string                   `json:"type"`
		Parameters []feedbackComponentParam `json:"parameters"`
	}

	custIDStr := customerID.String()
	visitIDStr := visitID.String()

	sendPayload := map[string]interface{}{
		"template_code":       "bb_service_feedback",
		"to":                  *customerPhone,
		"from_business_phone": fromBusinessPhone,
		"location_id":         locationID.String(),
		"language":            "en",
		"feedback_request_id": feedbackRequestID.String(),
		"notification_type":   "feedback_request",
		"customer_id":         &custIDStr,
		"source_type":         "feedback_request",
		"source_id":           feedbackRequestID.String(),
		"components": []interface{}{
			feedbackComponent{
				Type: "body",
				Parameters: []feedbackComponentParam{
					{Type: "text", Text: staffName},
					{Type: "text", Text: shopName},
					{Type: "text", Text: visitIDStr},
				},
			},
			map[string]interface{}{
				"type":     "button",
				"sub_type": "quick_reply",
				"index":    0,
				"parameters": []interface{}{
					map[string]interface{}{"type": "payload", "payload": "RATING:5:" + visitIDStr},
				},
			},
			map[string]interface{}{
				"type":     "button",
				"sub_type": "quick_reply",
				"index":    1,
				"parameters": []interface{}{
					map[string]interface{}{"type": "payload", "payload": "RATING:3:" + visitIDStr},
				},
			},
			map[string]interface{}{
				"type":     "button",
				"sub_type": "quick_reply",
				"index":    2,
				"parameters": []interface{}{
					map[string]interface{}{"type": "payload", "payload": "RATING:1:" + visitIDStr},
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(sendPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal notification payload: %w", err)
	}

	// 8. INSERT INTO outbox_events
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (tenant_id, type, payload, process_after)
		VALUES ($1, 'notification.send', $2, NOW())
	`, tenantID, payloadBytes)
	if err != nil {
		return err
	}

	// 9. COMMIT
	return tx.Commit(ctx)
}
