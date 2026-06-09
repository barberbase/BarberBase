package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/bhejna"
)

// OutboxEvent mirrors the structure in the outbox package.
type OutboxEvent struct {
	ID           string
	TenantID     *string
	Type         string
	Payload      []byte
	Status       string
	Attempts     int
	MaxAttempts  int
	ProcessAfter time.Time
	LockedUntil  *time.Time
	LastError    *string
	CreatedAt    time.Time
}

// NotificationPayload is the JSONB payload shape for all Bhejna-bound outbox event types.
// Written fully at insert time. The handler forwards without modification.
type NotificationPayload struct {
	TemplateCode string            `json:"template_code"`
	To           string            `json:"to"`          // E.164 recipient phone number
	LocationID   string            `json:"location_id"` // UUID string, for credential resolution
	Language     string            `json:"language"`    // always "en" for Phase 1
	Components   []json.RawMessage `json:"components"`  // fully constructed Bhejna component objects

	NotificationType string  `json:"notification_type"`     // e.g. "queue_joined", "near_turn"
	CustomerID       *string `json:"customer_id,omitempty"` // nullable UUID string
	SourceType       string  `json:"source_type,omitempty"`
	SourceID         *string `json:"source_id,omitempty"` // nullable UUID string
}

// TemplateToNotificationType maps template_code to notification_type.
var TemplateToNotificationType = map[string]string{
	"bb_queue_joined":          "queue_joined",
	"bb_near_turn":             "near_turn",
	"bb_you_are_next":          "you_are_next",
	"bb_service_feedback":      "feedback_request",
	"bb_staff_otp":             "staff_otp",
	"bb_appointment_confirmed": "appointment_confirmed",
	"bb_appointment_reminder":  "appointment_reminder",
	"bb_weekly_summary":        "weekly_summary",
	"bb_marketing_broadcast":   "marketing_broadcast",
	"bb_queue_cancelled":       "queue_cancelled",
	"bb_queue_snoozed":         "queue_snoozed",
	"bb_shop_closing_early":    "shop_closing_early",
}

type terminalError struct {
	msg string
}

func (e terminalError) Error() string {
	return e.msg
}

func (e terminalError) Is(target error) bool {
	return target.Error() == "terminal: do not retry"
}

func newTerminalError(format string, args ...interface{}) error {
	return terminalError{msg: fmt.Errorf(format, args...).Error()}
}

type Handler struct {
	pool   *pgxpool.Pool
	bhejna bhejna.Client
}

func NewHandler(pool *pgxpool.Pool, bhejna bhejna.Client) *Handler {
	return &Handler{
		pool:   pool,
		bhejna: bhejna,
	}
}

func (h *Handler) Handle(ctx context.Context, pool *pgxpool.Pool, event *OutboxEvent) error {
	var payload NotificationPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return newTerminalError("failed to unmarshal payload: %w", err)
	}

	if event.TenantID == nil {
		return newTerminalError("tenant id is nil")
	}
	tenantUUID, err := uuid.Parse(*event.TenantID)
	if err != nil {
		return newTerminalError("invalid tenant id: %v", err)
	}

	locUUID, err := uuid.Parse(payload.LocationID)
	if err != nil {
		return newTerminalError("invalid location id: %v", err)
	}

	var quotaType string
	if payload.TemplateCode == "bb_marketing_broadcast" {
		quotaType = "whatsapp_marketing"
	} else {
		quotaType = "whatsapp_transactional"
	}

	// Quota check transaction
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Step 1: Auto-create period row
	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_quota_periods
			(id, tenant_id, quota_type, period_start, period_end, included_limit)
		SELECT
			gen_random_uuid(),
			$1,
			$2::VARCHAR,
			date_trunc('month', NOW())::DATE,
			(date_trunc('month', NOW()) + INTERVAL '1 month' - INTERVAL '1 day')::DATE,
			CASE $2::VARCHAR
				WHEN 'whatsapp_marketing'     THEN t.monthly_marketing_quota
				WHEN 'whatsapp_transactional' THEN t.monthly_transactional_quota
			END
		FROM tenants t
		WHERE t.id = $1
		ON CONFLICT (tenant_id, quota_type, period_start) DO NOTHING;
	`, tenantUUID, quotaType)
	if err != nil {
		return err
	}

	// Step 2: Lock the period row
	var periodID string
	var usedCount int
	var includedLimit int
	err = tx.QueryRow(ctx, `
		SELECT id, used_count, included_limit
		FROM tenant_quota_periods
		WHERE tenant_id = $1
		  AND quota_type = $2::VARCHAR
		  AND period_start = date_trunc('month', NOW())::DATE
		FOR UPDATE;
	`, tenantUUID, quotaType).Scan(&periodID, &usedCount, &includedLimit)
	if err != nil {
		return err
	}

	// Step 3: Check limit & Marketing hard block
	if quotaType == "whatsapp_marketing" && usedCount >= includedLimit {
		var custID *uuid.UUID
		if payload.CustomerID != nil && *payload.CustomerID != "" {
			if c, err := uuid.Parse(*payload.CustomerID); err == nil {
				custID = &c
			}
		}
		var srcID *uuid.UUID
		if payload.SourceID != nil && *payload.SourceID != "" {
			if s, err := uuid.Parse(*payload.SourceID); err == nil {
				srcID = &s
			}
		}

		_, _ = tx.Exec(ctx, `
			INSERT INTO notification_events
				(id, tenant_id, location_id, customer_id, channel, notification_type,
				 quota_type, recipient_phone, template_code, status,
				 source_type, source_id, created_at)
			VALUES
				(gen_random_uuid(), $1, $2, $3, 'whatsapp', $4,
				 $5, $6, $7, 'blocked_quota',
				 $8, $9, NOW())`,
			tenantUUID, locUUID, custID, payload.NotificationType,
			quotaType, payload.To, payload.TemplateCode, payload.SourceType, srcID,
		)
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return commitErr
		}
		return newTerminalError("marketing quota exhausted: terminal: do not retry")
	}

	// Transactional warning at 800
	if quotaType == "whatsapp_transactional" && usedCount >= 800 {
		log.Printf("outbox: transactional quota warning: tenant=%s used=%d/%d",
			tenantUUID, usedCount, includedLimit)
	}

	// Step 4: Ledger insert
	res, err := tx.Exec(ctx, `
		INSERT INTO quota_usage_ledger
			(id, tenant_id, quota_type, quota_period_id, usage_count,
			 source_type, source_id, idempotency_key, created_at)
		VALUES
			(gen_random_uuid(), $1, $2, $3, 1,
			 'outbox_notification', $4::UUID, $4, NOW())
		ON CONFLICT (idempotency_key) DO NOTHING;
	`, tenantUUID, quotaType, periodID, event.ID)
	if err != nil {
		return err
	}

	// Step 5: Increment used_count only if new ledger row
	if res.RowsAffected() == 1 {
		_, err = tx.Exec(ctx, `
			UPDATE tenant_quota_periods
			SET    used_count = used_count + 1
			WHERE  id = $1;
		`, periodID)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Bhejna call
	var bhejnaComponents []bhejna.TemplateComponent
	if len(payload.Components) > 0 {
		bhejnaComponents = make([]bhejna.TemplateComponent, len(payload.Components))
		for i, rawComp := range payload.Components {
			if err := json.Unmarshal(rawComp, &bhejnaComponents[i]); err != nil {
				return newTerminalError("failed to unmarshal template component: %v", err)
			}
		}
	}

	lang := payload.Language
	if lang == "" {
		lang = "en"
	}
	sendReq := bhejna.SendTemplateReq{
		To:             payload.To,
		TemplateCode:   payload.TemplateCode,
		Language:       lang,
		Components:     bhejnaComponents,
		IdempotencyKey: "barberbase:outbox:" + event.ID,
	}

	sendResult, sendErr := h.bhejna.SendTemplate(ctx, tenantUUID, locUUID, sendReq)
	if sendErr != nil {
		var bhejnaErr bhejna.BhejnaError
		if errors.As(sendErr, &bhejnaErr) {
			if bhejnaErr.Retriable {
				return sendErr
			}
			h.writeFailedNotificationEvent(ctx, tenantUUID, locUUID, payload, quotaType, sendErr.Error())
			return newTerminalError("bhejna permanent error: %w", sendErr)
		}
		// Treat any other client/network error as retriable by default
		return sendErr
	}

	h.writeSuccessNotificationEvent(ctx, tenantUUID, locUUID, payload, quotaType, sendResult.JobID)
	return nil
}

func (h *Handler) writeSuccessNotificationEvent(ctx context.Context, tenantID uuid.UUID, locationID uuid.UUID, payload NotificationPayload, quotaType string, jobID string) {
	var custID *uuid.UUID
	if payload.CustomerID != nil && *payload.CustomerID != "" {
		if c, err := uuid.Parse(*payload.CustomerID); err == nil {
			custID = &c
		}
	}
	var srcID *uuid.UUID
	if payload.SourceID != nil && *payload.SourceID != "" {
		if s, err := uuid.Parse(*payload.SourceID); err == nil {
			srcID = &s
		}
	}

	_, _ = h.pool.Exec(ctx, `
		INSERT INTO notification_events
			(id, tenant_id, location_id, customer_id,
			 channel, notification_type, quota_type,
			 recipient_phone, template_code,
			 status, provider_message_id,
			 source_type, source_id,
			 created_at, sent_at)
		VALUES
			(gen_random_uuid(), $1, $2, $3,
			 'whatsapp', $4, $5,
			 $6, $7,
			 'sent', $8,
			 $9, $10,
			 NOW(), NOW());
	`, tenantID, locationID, custID, payload.NotificationType, quotaType,
		payload.To, payload.TemplateCode, jobID,
		payload.SourceType, srcID)
}

func (h *Handler) writeFailedNotificationEvent(ctx context.Context, tenantID uuid.UUID, locationID uuid.UUID, payload NotificationPayload, quotaType string, errMsg string) {
	var custID *uuid.UUID
	if payload.CustomerID != nil && *payload.CustomerID != "" {
		if c, err := uuid.Parse(*payload.CustomerID); err == nil {
			custID = &c
		}
	}
	var srcID *uuid.UUID
	if payload.SourceID != nil && *payload.SourceID != "" {
		if s, err := uuid.Parse(*payload.SourceID); err == nil {
			srcID = &s
		}
	}

	_, _ = h.pool.Exec(ctx, `
		INSERT INTO notification_events
			(id, tenant_id, location_id, customer_id,
			 channel, notification_type, quota_type,
			 recipient_phone, template_code,
			 status, error_message,
			 source_type, source_id,
			 created_at, sent_at)
		VALUES
			(gen_random_uuid(), $1, $2, $3,
			 'whatsapp', $4, $5,
			 $6, $7,
			 'failed', $8,
			 $9, $10,
			 NOW(), NOW());
	`, tenantID, locationID, custID, payload.NotificationType, quotaType,
		payload.To, payload.TemplateCode, errMsg,
		payload.SourceType, srcID)
}
