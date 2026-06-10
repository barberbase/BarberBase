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
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"barberbase-core/internal/repository"
)

// ... existing types from before ...

func (r *QueueRepository) CheckInAppointment(
	ctx context.Context,
	tenantID, locationID, appointmentID uuid.UUID,
) (*CheckInAppointmentResult, error) {
	var res CheckInAppointmentResult

	err := repository.WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Step 1 - Upsert-then-lock queue session
		loc, _ := time.LoadLocation("Asia/Kolkata")
		businessDateStr := time.Now().In(loc).Format("2006-01-02")

		_, err := tx.Exec(ctx, `
			INSERT INTO queue_sessions (tenant_id, location_id, business_date)
			VALUES ($1, $2, $3::DATE)
			ON CONFLICT (location_id, business_date) DO NOTHING
		`, tenantID, locationID, businessDateStr)
		if err != nil {
			return err
		}

		var sessionID uuid.UUID
		var lastTokenNumber int
		var prevQueueVersion int
		err = tx.QueryRow(ctx, `
			SELECT id, last_token_number, queue_version
			FROM queue_sessions
			WHERE location_id = $1 AND business_date = $2::DATE
			FOR UPDATE
		`, locationID, businessDateStr).Scan(&sessionID, &lastTokenNumber, &prevQueueVersion)
		if err != nil {
			return err
		}

		// Step 2 - Validate appointment
		var status string
		var customerID uuid.UUID
		var partySize int
		var totalDuration int
		err = tx.QueryRow(ctx, `
			SELECT status, customer_id, party_size, total_duration_minutes
			FROM appointments
			WHERE id = $1 AND tenant_id = $2
		`, appointmentID, tenantID).Scan(&status, &customerID, &partySize, &totalDuration)
		if err != nil {
			return err
		}
		if status != "scheduled" {
			return errors.New("appointment is not scheduled")
		}

		// Step 3 - Create visit
		var visitID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO visits (
				tenant_id, location_id, customer_id,
				entry_type, initiated_via, party_size, total_duration_minutes, status
			) VALUES (
				$1, $2, $3,
				'appointment', 'staff_dashboard', $4, $5, 'active'
			) RETURNING id
		`, tenantID, locationID, customerID, partySize, totalDuration).Scan(&visitID)
		if err != nil {
			return err
		}

		// generate magic link
		expiresAt := time.Now().Add(23 * time.Hour)
		expiresUnix := expiresAt.Unix()
		payload := fmt.Sprintf("%s%s%d", visitID.String(), locationID.String(), expiresUnix)
		mac := hmac.New(sha256.New, []byte(os.Getenv("HMAC_SECRET")))
		mac.Write([]byte(payload))
		magicHash := hex.EncodeToString(mac.Sum(nil))

		_, err = tx.Exec(ctx, `
			UPDATE visits
			SET magic_link_token_hash = $1, magic_link_expires_at = $2
			WHERE id = $3
		`, magicHash, expiresAt, visitID)
		if err != nil {
			return err
		}

		// Step 4 - Snapshot visit_services
		_, err = tx.Exec(ctx, `
			INSERT INTO visit_services (
				visit_id, service_variant_id,
				variant_name_snapshot, group_name_snapshot, category_name_snapshot,
				duration_minutes_snapshot, price_paise_snapshot, sort_order
			)
			SELECT 
				$1, aserv.service_variant_id,
				sv.name, sg.name, sc.name,
				sv.duration_minutes, sv.price_paise, aserv.sort_order
			FROM appointment_services aserv
			JOIN service_variants sv ON sv.id = aserv.service_variant_id
			JOIN service_groups sg ON sg.id = sv.group_id
			JOIN service_categories sc ON sc.id = sg.category_id
			WHERE aserv.appointment_id = $2
		`, visitID, appointmentID)
		if err != nil {
			return err
		}

		// Step 5 - Increment token and create queue_entry
		tokenNumber := lastTokenNumber + 1
		var entryID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO queue_entries (
				visit_id, queue_session_id, customer_id,
				token_number, state, presence_state, is_dispatchable,
				session_channel, priority_group, sort_key, remote_joined_at
			) VALUES (
				$1, $2, $3,
				$4, 'waiting', 'present', true,
				'walk_in', 100, EXTRACT(EPOCH FROM NOW())::BIGINT, NOW()
			) RETURNING id
		`, visitID, sessionID, customerID, tokenNumber).Scan(&entryID)
		if err != nil {
			return err
		}

		// Step 6 - Update appointment status
		_, err = tx.Exec(ctx, `
			UPDATE appointments
			SET status = 'checked_in', resolved_queue_entry_id = $1
			WHERE id = $2
		`, entryID, appointmentID)
		if err != nil {
			return err
		}

		// Step 7 - Increment queue_version
		_, err = tx.Exec(ctx, `
			UPDATE queue_sessions
			SET last_token_number = last_token_number + 1, queue_version = queue_version + 1
			WHERE id = $1
		`, sessionID)
		if err != nil {
			return err
		}

		res = CheckInAppointmentResult{
			VisitID:      visitID,
			QueueEntryID: entryID,
			TokenNumber:  tokenNumber,
			MagicLink:    "", // to be populated
		}
		return nil
	})

	return &res, err
}
