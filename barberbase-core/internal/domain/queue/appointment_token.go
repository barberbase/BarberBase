package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrAppointmentTokenInvalid = errors.New("invalid or expired appointment token")

// VerifyAppointmentToken checks HMAC and expiry of an appointment magic-link
// token and returns the appointment ID. Mint lives in BookAppointment (same
// package) — keep the two in sync.
func VerifyAppointmentToken(token string) (uuid.UUID, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	mac := hmac.New(sha256.New, []byte(os.Getenv("HMAC_SECRET")))
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	fields := strings.Split(string(raw), ":")
	if len(fields) != 2 {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	id, err := uuid.Parse(fields[0])
	if err != nil {
		return uuid.Nil, ErrAppointmentTokenInvalid
	}
	return id, nil
}

// CancelScheduledAppointment flips a scheduled appointment to
// customer-cancelled and sweeps its pending day-before reminder, inside the
// caller's transaction. Both cancel entry points (WhatsApp CANCEL_APT button
// and the /q/appointment page) route through here. Returns false when the row
// was no longer 'scheduled' (already cancelled / checked in / unknown).
func CancelScheduledAppointment(ctx context.Context, tx pgx.Tx, aptID uuid.UUID) (bool, error) {
	ct, err := tx.Exec(ctx, `
		UPDATE appointments
		SET status = 'cancelled', cancelled_at = NOW(), cancelled_by = 'customer', updated_at = NOW()
		WHERE id = $1 AND status = 'scheduled'
	`, aptID)
	if err != nil {
		return false, err
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}
	_, err = tx.Exec(ctx, `
		DELETE FROM outbox_events
		WHERE status = 'pending'
		  AND type = 'notification.send'
		  AND payload->>'template_code' = 'bb_appointment_reminder'
		  AND payload->>'source_id' = $1
	`, aptID.String())
	if err != nil {
		return false, err
	}
	return true, nil
}
