package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LocationWebhookConfig struct {
	TenantID                     uuid.UUID
	WhatsAppMode                 string
	BhejnaWebhookSecretEncrypted string
}

// GetLocationWebhookConfig resolves a location's Bhejna webhook configuration.
// It queries by location ID and asserts that the location is not soft-deleted.
func GetLocationWebhookConfig(ctx context.Context, pool *pgxpool.Pool, locationID uuid.UUID) (*LocationWebhookConfig, error) {
	var cfg LocationWebhookConfig
	query := `
		SELECT tenant_id, whatsapp_mode, COALESCE(bhejna_webhook_secret_encrypted, '')
		FROM locations
		WHERE id = $1 AND is_active = true
	`
	err := pool.QueryRow(ctx, query, locationID).Scan(&cfg.TenantID, &cfg.WhatsAppMode, &cfg.BhejnaWebhookSecretEncrypted)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// InsertWebhookEvent inserts an incoming Bhejna webhook event into the database.
// It uses ON CONFLICT (source, external_event_id) DO NOTHING for idempotency.
func InsertWebhookEvent(
	ctx context.Context,
	pool *pgxpool.Pool,
	externalEventID string,
	eventType string,
	tenantID *uuid.UUID,
	locationID *uuid.UUID,
	payload []byte,
) error {
	query := `
		INSERT INTO webhook_events (source, external_event_id, event_type, tenant_id, location_id, payload, status)
		VALUES ('bhejna', $1, $2, $3, $4, $5, 'pending')
		ON CONFLICT (source, external_event_id) DO NOTHING
	`
	_, err := pool.Exec(ctx, query, externalEventID, eventType, tenantID, locationID, payload)
	if err != nil {
		return fmt.Errorf("failed to insert webhook event: %w", err)
	}
	return nil
}
