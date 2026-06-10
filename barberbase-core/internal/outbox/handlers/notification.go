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
	"barberbase-core/internal/repository"
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
	repo   *repository.Repository
}

func NewHandler(pool *pgxpool.Pool, bhejna bhejna.Client) *Handler {
	return &Handler{
		pool:   pool,
		bhejna: bhejna,
		repo:   &repository.Repository{Pool: pool},
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

	quotaType := quotaTypeForTemplate(payload.TemplateCode)

	eventID, err := uuid.Parse(event.ID)
	if err != nil {
		return newTerminalError("invalid event id: %v", err)
	}

	blocked, err := consumeQuota(ctx, pool, h.repo, tenantUUID, eventID, payload.TemplateCode)
	if err != nil {
		return err
	}

	if blocked {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)

		_, err = tx.Exec(ctx, `
			INSERT INTO notification_events (
				id, tenant_id, location_id, channel, notification_type,
				quota_type, recipient_phone, template_code, status,
				source_type, source_id, created_at
			) VALUES (
				gen_random_uuid(), $1, $2, 'whatsapp', 'marketing_broadcast',
				'whatsapp_marketing', $3, $4, 'blocked_quota',
				'outbox_event', $5, NOW()
			)`,
			tenantUUID, locUUID, payload.To, payload.TemplateCode, eventID,
		)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE outbox_events
			SET status='failed', last_error='quota_exhausted'
			WHERE id = $1`, eventID)
		if err != nil {
			return err
		}

		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return newTerminalError("quota_exhausted")
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

func quotaTypeForTemplate(templateCode string) string {
	if templateCode == "bb_marketing_broadcast" {
		return "whatsapp_marketing"
	}
	return "whatsapp_transactional"
}

func consumeQuota(
	ctx context.Context,
	pool *pgxpool.Pool,
	repo *repository.Repository,
	tenantID uuid.UUID,
	outboxEventID uuid.UUID,
	templateCode string,
) (blocked bool, err error) {
	quotaType := quotaTypeForTemplate(templateCode)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	period, err := repo.UpsertAndLockQuotaPeriod(ctx, tx, tenantID, quotaType)
	if err != nil {
		return false, err
	}

	if quotaType == "whatsapp_marketing" && period.UsedCount >= period.IncludedLimit {
		if err = tx.Commit(ctx); err != nil {
			return false, err
		}
		return true, nil
	}

	// Transactional warning at 800
	if quotaType == "whatsapp_transactional" && period.UsedCount >= 800 {
		log.Printf("outbox: transactional quota warning: tenant=%s used=%d/%d",
			tenantID, period.UsedCount, period.IncludedLimit)
	}

	inserted, err := repo.InsertQuotaLedgerIdempotent(ctx, tx, tenantID, quotaType, period.PeriodID, outboxEventID)
	if err != nil {
		return false, err
	}

	if inserted {
		err = repo.IncrementQuotaPeriodUsed(ctx, tx, period.PeriodID)
		if err != nil {
			return false, err
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return false, err
	}
	return false, nil
}
