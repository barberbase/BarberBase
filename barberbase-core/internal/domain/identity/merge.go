package identity

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MergeShadowProfile promotes a bsuid-only shadow customer to a real customer
// when a confirmed phone number arrives within the same session.
// Phase 1 scope: update shadow in place only.
// If a real customer with that phone already exists, log the conflict and return nil
// (no hard merge — Phase 1 limitation, document as known limitation).
//
// Must be called inside an existing transaction (tx pgx.Tx).
func MergeShadowProfile(
	ctx context.Context, tx pgx.Tx,
	shadowID uuid.UUID, tenantID uuid.UUID, phone string, bsuid string,
) error {
	// 1. Check if a real customer with that phone already exists
	queryLookup := `
		SELECT id FROM customers
		WHERE tenant_id = $1
		  AND phone_number = $2
		  AND merged_into_customer_id IS NULL
		LIMIT 1
	`
	var existingID uuid.UUID
	err := tx.QueryRow(ctx, queryLookup, tenantID, phone).Scan(&existingID)
	if err == nil {
		log.Printf("[Warning] shadow merge conflict: phone %s already owned by different customer %s", phone, existingID)
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to check existing phone before merge: %w", err)
	}

	// 2. Update the shadow customer profile
	queryUpdate := `
		UPDATE customers
		SET phone_number = $1,
		    is_shadow_profile = false,
		    updated_at = NOW()
		WHERE id = $2
		  AND tenant_id = $3
		  AND merged_into_customer_id IS NULL
	`
	res, err := tx.Exec(ctx, queryUpdate, phone, shadowID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update shadow customer: %w", err)
	}
	if res.RowsAffected() == 0 {
		// Log and skip if shadow doesn't exist or is already merged
		log.Printf("[Warning] shadow customer %s not found or already merged during shadow merge", shadowID)
		return nil
	}

	// 3. Link provider identity
	if bsuid != "" {
		queryIdentity := `
			INSERT INTO customer_identities (customer_id, provider, provider_id)
			VALUES ($1, 'whatsapp', $2)
			ON CONFLICT (provider, provider_id) DO NOTHING
		`
		_, err = tx.Exec(ctx, queryIdentity, shadowID, bsuid)
		if err != nil {
			return fmt.Errorf("failed to link shadow customer provider identity: %w", err)
		}
	}

	return nil
}
