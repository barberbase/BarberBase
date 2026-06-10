package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	VisitID      uuid.UUID
	QueueEntryID uuid.UUID
	TokenNumber  int
	MagicLink    string // generated from visit's magic_link_token_hash
}

type QueueRepository struct {
	Pool *pgxpool.Pool
}

func (r *QueueRepository) BookAppointment(ctx context.Context, req BookAppointmentRequest) (*BookAppointmentResult, error) {
	var res *BookAppointmentResult
	err := repository.WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Step 1 — Idempotency key insert
		var ikID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO idempotency_keys(id, tenant_id, key, endpoint, created_at)
			VALUES (gen_random_uuid(), $1, $2, 'appointment.book', NOW())
			ON CONFLICT (tenant_id, key, endpoint) DO NOTHING
			RETURNING id;
		`, req.LocationID, req.IdempotencyKey).Scan(&ikID)
		
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
			`, req.LocationID, req.IdempotencyKey).Scan(&respStatus, &respBody)
			if err != nil {
				return err
			}
			if respStatus != nil {
				// Replay
				var cachedRes BookAppointmentResult
				if err := json.Unmarshal(respBody, &cachedRes); err != nil {
					return err
				}
				res = &cachedRes
				return nil
			}
			return errors.New("request still in flight") // return HTTP 409
		}

		// (I'll refine the queries later)
		return nil
	})
	return res, err
}

