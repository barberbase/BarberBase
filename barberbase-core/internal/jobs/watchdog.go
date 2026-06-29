package jobs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"barberbase-core/internal/config"
	"barberbase-core/internal/realtime"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	advisoryLockWatchdog      = int64(0xBBC401) // 12304385
	advisoryLockEndOfDay      = int64(0xBBC402) // 12304386
	advisoryLockWeeklySummary = int64(0xBBC403) // 12304387
)

type Watchdog struct {
	db      *pgxpool.Pool
	manager *realtime.Manager
	cfg     *config.Config
}

func NewWatchdog(db *pgxpool.Pool, manager *realtime.Manager, cfg *config.Config) *Watchdog {
	return &Watchdog{
		db:      db,
		manager: manager,
		cfg:     cfg,
	}
}

func (w *Watchdog) Start(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Run immediately on start
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Watchdog) tick(ctx context.Context) {
	var acquired bool
	err := w.db.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockWatchdog).Scan(&acquired)
	if err != nil || !acquired {
		return
	}
	defer w.db.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockWatchdog)

	w.runJob(ctx)
}

type session struct {
	ID                          uuid.UUID
	TenantID                    uuid.UUID
	LocationID                  uuid.UUID
	NotifyPeopleAhead           int
	NotifyWaitMinutes           int
	StaleCalledWarningMinutes   int
	StaleCalledCriticalMinutes  int
	InProgressWarningMinutes    int
	InProgressConfirmMinutes    int
	InProgressCriticalMinutes   int
	LocationName                string
}

func (w *Watchdog) runJob(ctx context.Context) {
	rows, err := w.db.Query(ctx, `
		SELECT qs.id, qs.tenant_id, qs.location_id,
		       l.notify_when_people_ahead, l.notify_when_wait_minutes,
		       l.stale_called_warning_minutes, l.stale_called_critical_minutes,
		       l.in_progress_warning_minutes, l.in_progress_confirm_minutes,
		       l.in_progress_critical_minutes,
		       l.name AS location_name
		FROM queue_sessions qs
		JOIN locations l ON l.id = qs.location_id
		WHERE qs.business_date = (NOW() AT TIME ZONE l.timezone)::DATE
		  AND qs.status IN ('active', 'ending')
		  AND l.is_active = true
	`)
	if err != nil {
		log.Printf("Watchdog: failed to query active sessions: %v", err)
		return
	}
	defer rows.Close()

	var sessions []session
	for rows.Next() {
		var s session
		err := rows.Scan(
			&s.ID, &s.TenantID, &s.LocationID,
			&s.NotifyPeopleAhead, &s.NotifyWaitMinutes,
			&s.StaleCalledWarningMinutes, &s.StaleCalledCriticalMinutes,
			&s.InProgressWarningMinutes, &s.InProgressConfirmMinutes,
			&s.InProgressCriticalMinutes,
			&s.LocationName,
		)
		if err != nil {
			log.Printf("Watchdog: failed to scan session row: %v", err)
			continue
		}
		sessions = append(sessions, s)
	}
	rows.Close()

	for _, s := range sessions {
		w.checkNearTurn(ctx, s)
		w.checkAutoSnooze(ctx, s)
		w.updateStaleWarnings(ctx, s)
	}
}

type candidate struct {
	EntryID              uuid.UUID
	VisitID              uuid.UUID
	CustomerID           uuid.UUID
	TokenNumber          int
	MagicLinkExpiresAt   time.Time
	CustomerPhone        string
	PeopleAhead          int
	EstimatedWaitMinutes int
}

func (w *Watchdog) checkNearTurn(ctx context.Context, s session) {
	rows, err := w.db.Query(ctx, `
		SELECT
		    qe.id                   AS entry_id,
		    qe.visit_id,
		    qe.customer_id,
		    qe.token_number,
		    v.magic_link_expires_at,
		    c.phone_number          AS customer_phone,
		    -- Count how many dispatchable waiting entries are ordered ahead of this one
		    (SELECT COUNT(*) FROM queue_entries x
		     WHERE x.queue_session_id = qe.queue_session_id
		       AND x.state = 'waiting' AND x.is_dispatchable = true
		       AND (x.priority_group < qe.priority_group
		            OR (x.priority_group = qe.priority_group AND x.sort_key < qe.sort_key))
		    ) AS people_ahead,
		    -- Estimated wait = sum of total_duration_minutes of entries ahead
		    COALESCE(
		      (SELECT SUM(v2.total_duration_minutes)
		       FROM queue_entries x2
		       JOIN visits v2 ON v2.id = x2.visit_id
		       WHERE x2.queue_session_id = qe.queue_session_id
		         AND x2.state = 'waiting' AND x2.is_dispatchable = true
		         AND (x2.priority_group < qe.priority_group
		              OR (x2.priority_group = qe.priority_group AND x2.sort_key < qe.sort_key))
		      ), 0
		    ) AS estimated_wait_minutes
		FROM queue_entries qe
		JOIN visits v ON v.id = qe.visit_id
		JOIN customers c ON c.id = qe.customer_id
		WHERE qe.queue_session_id = $1
		  AND qe.state = 'waiting'
		  AND qe.is_dispatchable = true
		  AND qe.presence_state = 'remote'
		  AND qe.session_channel = 'whatsapp'
		  AND qe.near_turn_notified_at IS NULL
		  AND qe.customer_id IS NOT NULL
	`, s.ID)
	if err != nil {
		log.Printf("Watchdog near-turn query failed: %v", err)
		return
	}
	defer rows.Close()

	var candidates []candidate
	for rows.Next() {
		var cand candidate
		err := rows.Scan(
			&cand.EntryID, &cand.VisitID, &cand.CustomerID, &cand.TokenNumber,
			&cand.MagicLinkExpiresAt, &cand.CustomerPhone,
			&cand.PeopleAhead, &cand.EstimatedWaitMinutes,
		)
		if err != nil {
			log.Printf("Watchdog scan candidate failed: %v", err)
			continue
		}
		candidates = append(candidates, cand)
	}
	rows.Close()

	for _, cand := range candidates {
		if cand.PeopleAhead <= s.NotifyPeopleAhead || cand.EstimatedWaitMinutes <= s.NotifyWaitMinutes {
			w.triggerNearTurn(ctx, s, cand)
		}
	}
}

func (w *Watchdog) triggerNearTurn(ctx context.Context, s session, cand candidate) {
	var newQueueVersion int
	err := repository.WithTx(ctx, w.db, func(tx pgx.Tx) error {
		// Law 1: lock session first
		var sessionLockID uuid.UUID
		err := tx.QueryRow(ctx, "SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE", s.ID).Scan(&sessionLockID)
		if err != nil {
			return err
		}

		// Idempotency guards in the UPDATE itself
		res, err := tx.Exec(ctx, `
			UPDATE queue_entries
			SET presence_state = 'notified',
			    near_turn_notified_at = NOW()
			WHERE id = $1
			  AND presence_state = 'remote'
			  AND near_turn_notified_at IS NULL
		`, cand.EntryID)
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return fmt.Errorf("idempotency guard hit: entry %s already notified", cand.EntryID)
		}

		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions 
			SET queue_version = queue_version + 1 
			WHERE id = $1
			RETURNING queue_version
		`, s.ID).Scan(&newQueueVersion)
		if err != nil {
			return err
		}

		magicLinkToken := generateMagicLinkToken(cand.CustomerID.String(), s.LocationID.String(), cand.VisitID.String(), cand.MagicLinkExpiresAt, []byte(w.cfg.HMACSecret))

		outboxPayload := map[string]interface{}{
			"template_code":       "bb_near_turn",
			"to":                  cand.CustomerPhone,
			"from_business_phone": w.cfg.BhejnaFromPhone,
			"components": []interface{}{
				map[string]interface{}{
					"type": "body",
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": s.LocationName},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(cand.PeopleAhead)},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(cand.EstimatedWaitMinutes)},
						map[string]interface{}{"type": "text", "text": cand.EntryID.String()},
					},
				},
				map[string]interface{}{
					"type":     "button",
					"sub_type": "url",
					"index":    1,
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": magicLinkToken},
					},
				},
			},
		}

		payloadBytes, err := json.Marshal(outboxPayload)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (tenant_id, type, payload, process_after)
			VALUES ($1, 'notification.send', $2, NOW())
		`, s.TenantID, payloadBytes)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("Watchdog near-turn trigger failed for entry %s: %v", cand.EntryID, err)
		return
	}

	w.manager.Broadcast(s.LocationID.String(), realtime.SSEEvent{
		Type:         "queue_changed",
		LocationID:   s.LocationID.String(),
		QueueVersion: newQueueVersion,
	})
}

func (w *Watchdog) checkAutoSnooze(ctx context.Context, s session) {
	var top struct {
		ID             uuid.UUID
		PresenceState  string
		SessionChannel string
		VisitID        uuid.UUID
		CustomerID     *uuid.UUID
		TokenNumber    int
	}
	err := w.db.QueryRow(ctx, `
		SELECT id, presence_state, session_channel, visit_id, customer_id, token_number
		FROM queue_entries
		WHERE queue_session_id = $1
		  AND state = 'waiting'
		  AND is_dispatchable = true
		ORDER BY priority_group ASC, sort_key ASC
		LIMIT 1
	`, s.ID).Scan(&top.ID, &top.PresenceState, &top.SessionChannel, &top.VisitID, &top.CustomerID, &top.TokenNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		log.Printf("Watchdog checkAutoSnooze query failed: %v", err)
		return
	}

	if top.PresenceState == "remote" || top.PresenceState == "notified" {
		w.triggerAutoSnooze(ctx, s, top)
	}
}

func (w *Watchdog) triggerAutoSnooze(ctx context.Context, s session, top struct {
	ID             uuid.UUID
	PresenceState  string
	SessionChannel string
	VisitID        uuid.UUID
	CustomerID     *uuid.UUID
	TokenNumber    int
}) {
	var newQueueVersion int
	var customerPhone string
	var magicLinkExpiresAt time.Time

	if top.SessionChannel == "whatsapp" && top.CustomerID != nil {
		err := w.db.QueryRow(ctx, `
			SELECT c.phone_number, v.magic_link_expires_at
			FROM customers c
			JOIN visits v ON v.id = $1
			WHERE c.id = $2
		`, top.VisitID, *top.CustomerID).Scan(&customerPhone, &magicLinkExpiresAt)
		if err != nil {
			log.Printf("Watchdog: failed to get customer info for snooze: %v", err)
			return
		}
	}

	err := repository.WithTx(ctx, w.db, func(tx pgx.Tx) error {
		// Law 1: lock session first
		var sessionLockID uuid.UUID
		err := tx.QueryRow(ctx, "SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE", s.ID).Scan(&sessionLockID)
		if err != nil {
			return err
		}

		res, err := tx.Exec(ctx, `
			UPDATE queue_entries
			SET presence_state = 'snoozed',
			    is_dispatchable = false,
			    snoozed_at = NOW()
			WHERE id = $1
			  AND presence_state IN ('remote', 'notified')
			  AND state = 'waiting'
		`, top.ID)
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return fmt.Errorf("snooze idempotency guard hit: entry %s already snoozed", top.ID)
		}

		err = tx.QueryRow(ctx, `
			UPDATE queue_sessions 
			SET queue_version = queue_version + 1 
			WHERE id = $1
			RETURNING queue_version
		`, s.ID).Scan(&newQueueVersion)
		if err != nil {
			return err
		}

		if top.SessionChannel == "whatsapp" && top.CustomerID != nil {
			magicLinkToken := generateMagicLinkToken(top.CustomerID.String(), s.LocationID.String(), top.VisitID.String(), magicLinkExpiresAt, []byte(w.cfg.HMACSecret))

			outboxPayload := map[string]interface{}{
				"template_code":       "bb_queue_snoozed",
				"to":                  customerPhone,
				"from_business_phone": w.cfg.BhejnaFromPhone,
				"components": []interface{}{
					map[string]interface{}{
						"type": "body",
						"parameters": []interface{}{
							map[string]interface{}{"type": "text", "text": s.LocationName},
							map[string]interface{}{"type": "text", "text": strconv.Itoa(top.TokenNumber)},
						},
					},
					map[string]interface{}{
						"type":     "button",
						"sub_type": "url",
						"index":    0,
						"parameters": []interface{}{
							map[string]interface{}{"type": "text", "text": magicLinkToken},
						},
					},
				},
			}

			payloadBytes, err := json.Marshal(outboxPayload)
			if err != nil {
				return err
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO outbox_events (tenant_id, type, payload, process_after)
				VALUES ($1, 'notification.send', $2, NOW())
			`, s.TenantID, payloadBytes)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Watchdog snooze trigger failed for entry %s: %v", top.ID, err)
		return
	}

	w.manager.Broadcast(s.LocationID.String(), realtime.SSEEvent{
		Type:         "queue_changed",
		LocationID:   s.LocationID.String(),
		QueueVersion: newQueueVersion,
	})
}

func (w *Watchdog) updateStaleWarnings(ctx context.Context, s session) {
	_, err := w.db.Exec(ctx, `
		UPDATE queue_entries
		SET stale_warning = CASE
			WHEN NOW() > called_at + ($2 * INTERVAL '1 minute') THEN 'called_critical'
			WHEN NOW() > called_at + ($3 * INTERVAL '1 minute') THEN 'called_warning'
			ELSE NULL
		END
		WHERE queue_session_id = $1
		  AND state = 'called'
		  AND called_at IS NOT NULL
	`, s.ID, s.StaleCalledCriticalMinutes, s.StaleCalledWarningMinutes)
	if err != nil {
		log.Printf("Watchdog: failed to update stale warnings for called entries of session %s: %v", s.ID, err)
	}

	_, err = w.db.Exec(ctx, `
		UPDATE queue_entries qe
		SET stale_warning = CASE
			WHEN NOW() > qe.started_at + ((v.total_duration_minutes + $2) * INTERVAL '1 minute') THEN 'in_progress_critical'
			WHEN NOW() > qe.started_at + ((v.total_duration_minutes + $3) * INTERVAL '1 minute') THEN 'in_progress_confirm'
			WHEN NOW() > qe.started_at + ((v.total_duration_minutes + $4) * INTERVAL '1 minute') THEN 'in_progress_warning'
			ELSE NULL
		END
		FROM visits v
		WHERE qe.queue_session_id = $1
		  AND qe.state = 'in_progress'
		  AND qe.started_at IS NOT NULL
		  AND v.id = qe.visit_id
	`, s.ID, s.InProgressCriticalMinutes, s.InProgressConfirmMinutes, s.InProgressWarningMinutes)
	if err != nil {
		log.Printf("Watchdog: failed to update stale warnings for in_progress entries of session %s: %v", s.ID, err)
	}
}

func generateMagicLinkToken(customerIDStr, locationIDStr, visitIDStr string, expiresAt time.Time, secret []byte) string {
	tokenPayload := customerIDStr + "|" + locationIDStr + "|" + visitIDStr + "|" + strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(tokenPayload))
	hashed := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(hashed)
}
