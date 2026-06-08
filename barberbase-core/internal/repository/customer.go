package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NormalizeE164 normalizes a raw phone number.
// 1. Strip all non-digit characters from raw
// 2. If result is empty → return "" (masked number)
// 3. Prepend '+' → return "+<digits>"
func NormalizeE164(raw string) string {
	var sb strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	digits := sb.String()
	if digits == "" {
		return ""
	}
	return "+" + digits
}

// ResolveOrCreateCustomer resolves the canonical customer_id for an inbound message.
// Called once per message.received after tenantID is known.
func ResolveOrCreateCustomer(
	ctx context.Context, pool *pgxpool.Pool,
	tenantID uuid.UUID, phone string, bsuid string, displayName string,
) (uuid.UUID, error) {
	phone = NormalizeE164(phone)

	if phone != "" {
		// Attempt lookup first
		var id uuid.UUID
		queryLookup := `
			SELECT id FROM customers
			WHERE tenant_id = $1
			  AND phone_number = $2
			  AND merged_into_customer_id IS NULL
			LIMIT 1
		`
		err := pool.QueryRow(ctx, queryLookup, tenantID, phone).Scan(&id)
		if err == nil {
			// Found, now insert identity if bsuid present
			if bsuid != "" {
				queryIdentity := `
					INSERT INTO customer_identities (customer_id, provider, provider_id)
					VALUES ($1, 'whatsapp', $2)
					ON CONFLICT (provider, provider_id) DO NOTHING
				`
				_, _ = pool.Exec(ctx, queryIdentity, id, bsuid)
			}
			return id, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("failed to lookup customer: %w", err)
		}

		// Not found, insert new customer
		queryInsert := `
			INSERT INTO customers (tenant_id, phone_number, name)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, phone_number) DO NOTHING
			RETURNING id
		`
		err = pool.QueryRow(ctx, queryInsert, tenantID, phone, displayName).Scan(&id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT DO NOTHING returned no row, retry lookup
				err = pool.QueryRow(ctx, queryLookup, tenantID, phone).Scan(&id)
				if err != nil {
					return uuid.Nil, fmt.Errorf("failed to lookup customer after insert conflict: %w", err)
				}
			} else {
				return uuid.Nil, fmt.Errorf("failed to insert customer: %w", err)
			}
		}

		// Insert identity if bsuid present
		if bsuid != "" {
			queryIdentity := `
				INSERT INTO customer_identities (customer_id, provider, provider_id)
				VALUES ($1, 'whatsapp', $2)
				ON CONFLICT (provider, provider_id) DO NOTHING
			`
			_, _ = pool.Exec(ctx, queryIdentity, id, bsuid)
		}
		return id, nil
	} else {
		// Masked path (phone == "")
		if bsuid == "" {
			return uuid.Nil, errors.New("bsuid is required for masked phone customer resolution")
		}

		queryLookupMasked := `
			SELECT ci.customer_id
			FROM customer_identities ci
			JOIN customers c ON c.id = ci.customer_id
			WHERE ci.provider = 'whatsapp'
			  AND ci.provider_id = $1
			  AND c.tenant_id = $2
			  AND c.merged_into_customer_id IS NULL
			LIMIT 1
		`
		var id uuid.UUID
		err := pool.QueryRow(ctx, queryLookupMasked, bsuid, tenantID).Scan(&id)
		if err == nil {
			return id, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("failed to lookup masked customer: %w", err)
		}

		// Not found, create shadow profile
		queryInsertShadow := `
			INSERT INTO customers (tenant_id, is_shadow_profile)
			VALUES ($1, true)
			RETURNING id
		`
		err = pool.QueryRow(ctx, queryInsertShadow, tenantID).Scan(&id)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to insert shadow customer: %w", err)
		}

		queryInsertIdentity := `
			INSERT INTO customer_identities (customer_id, provider, provider_id)
			VALUES ($1, 'whatsapp', $2)
			ON CONFLICT (provider, provider_id) DO NOTHING
		`
		_, err = pool.Exec(ctx, queryInsertIdentity, id, bsuid)
		if err != nil {
			// If conflict happens here, someone created it concurrently, re-select
			var reselectID uuid.UUID
			errReselect := pool.QueryRow(ctx, queryLookupMasked, bsuid, tenantID).Scan(&reselectID)
			if errReselect == nil {
				return reselectID, nil
			}
			return uuid.Nil, fmt.Errorf("failed to insert shadow customer identity: %w", err)
		}

		return id, nil
	}
}
