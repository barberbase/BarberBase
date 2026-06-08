package identity

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"barberbase-core/internal/repository"
)

// ResolveCustomerIdentity is the entry point for customer identity resolution.
// Routes to the masked or non-masked path based on phone presence.
func ResolveCustomerIdentity(
	ctx context.Context, db *pgxpool.Pool,
	tenantID uuid.UUID, phone string, bsuid string, displayName string,
) (uuid.UUID, error) {
	return repository.ResolveOrCreateCustomer(ctx, db, tenantID, phone, bsuid, displayName)
}
