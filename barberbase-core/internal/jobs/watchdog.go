package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"barberbase-core/internal/config"
	"barberbase-core/internal/domain/queue"
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

	// Grace period: minimum wall-clock minutes since near_turn_notified_at
	// before an entry is auto-snooze-eligible. Never notified = never snoozed.
	snoozeGraceMinutes = 5
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
	ID                         uuid.UUID
	TenantID                   uuid.UUID
	LocationID                 uuid.UUID
	NotifyPeopleAhead          int
	NotifyWaitMinutes          int
	StaleCalledWarningMinutes  int
	StaleCalledCriticalMinutes int
	InProgressWarningMinutes   int
	InProgressConfirmMinutes   int
	InProgressCriticalMinutes  int
	LocationName               string
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
		w.checkTierAvailability(ctx, s)
		w.updateStaleWarnings(ctx, s)
	}
}

// [B7] MagicLinkExpiresAt is a pointer because visits.magic_link_expires_at
// (001:703) is nullable, and is NULL for every anonymous walk-in — the link is
// keyed to a customer, and an anonymous visit has none. Scanning it into a bare
// time.Time failed the whole row, checkNearTurn logged and skipped it,
// near_turn_notified_at stayed NULL, and checkAutoSnooze requires that column
// non-NULL — so an anonymous walk-in could never be auto-snoozed and sat at the
// head of the queue indefinitely. nil means "no magic link", which is the normal
// case here, never an error.
type candidate struct {
	EntryID              uuid.UUID
	VisitID              uuid.UUID
	CustomerID           *uuid.UUID // nil for anonymous web entries
	SessionChannel       string
	TokenNumber          int
	MagicLinkExpiresAt   *time.Time
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
		    qe.session_channel,
		    qe.token_number,
		    v.magic_link_expires_at,
		    COALESCE(c.phone_number, '') AS customer_phone,
		    -- Count how many dispatchable waiting entries are ordered ahead of this
		    -- one AND compete for the same barbers. [T3] The tier disjunct is true
		    -- for every row when required_tier_id is NULL throughout, so a shop with
		    -- no tiers counts exactly what it counted before.
		    (SELECT COUNT(*) FROM queue_entries x
		     WHERE x.queue_session_id = qe.queue_session_id
		       AND x.state = 'waiting' AND x.is_dispatchable = true
		       AND (x.priority_group < qe.priority_group
		            OR (x.priority_group = qe.priority_group AND x.sort_key < qe.sort_key))
		       AND (x.required_tier_id IS NULL
		            OR qe.required_tier_id IS NULL
		            OR x.required_tier_id = qe.required_tier_id)
		    ) AS people_ahead,
		    -- Estimated wait = sum of total_duration_minutes of competing entries ahead.
		    -- Deliberately NOT divided by the eligible barber count: this number also
		    -- gates when bb_near_turn fires, and introducing a divisor here would
		    -- change that threshold for every existing shop, tiered or not. It
		    -- therefore reads lower than the customer-facing estimate from
		    -- repository.TierScopedWait. Flagged, not silently unified.
		    COALESCE(
		      (SELECT SUM(v2.total_duration_minutes)
		       FROM queue_entries x2
		       JOIN visits v2 ON v2.id = x2.visit_id
		       WHERE x2.queue_session_id = qe.queue_session_id
		         AND x2.state = 'waiting' AND x2.is_dispatchable = true
		         AND (x2.priority_group < qe.priority_group
		              OR (x2.priority_group = qe.priority_group AND x2.sort_key < qe.sort_key))
		         AND (x2.required_tier_id IS NULL
		              OR qe.required_tier_id IS NULL
		              OR x2.required_tier_id = qe.required_tier_id)
		      ), 0
		    ) AS estimated_wait_minutes
		FROM queue_entries qe
		JOIN visits v ON v.id = qe.visit_id
		LEFT JOIN customers c ON c.id = qe.customer_id
		WHERE qe.queue_session_id = $1
		  AND qe.state = 'waiting'
		  AND qe.is_dispatchable = true
		  AND qe.presence_state = 'remote'
		  AND qe.near_turn_notified_at IS NULL
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
			&cand.EntryID, &cand.VisitID, &cand.CustomerID, &cand.SessionChannel, &cand.TokenNumber,
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

		// H4: the marking above is channel-agnostic — web entries need
		// near_turn_notified_at for snooze eligibility, and their near-turn
		// signal is the SSE/status page. The WhatsApp template only goes to
		// whatsapp-channel entries with a resolvable customer (Law 7: the
		// insert stays inside this transaction).
		// [B7] A nil expiry joins that list: no magic link was ever issued for this
		// visit, so there is no link to put in a template. The marking above has
		// already happened, which is the whole point — the entry stays
		// snooze-eligible either way.
		if cand.SessionChannel != "whatsapp" || cand.CustomerID == nil || cand.MagicLinkExpiresAt == nil {
			return nil
		}

		magicLinkToken := queue.GenerateMagicLinkToken(cand.CustomerID.String(), s.LocationID.String(), cand.VisitID.String(), *cand.MagicLinkExpiresAt, []byte(w.cfg.HMACSecret))

		outboxPayload := map[string]interface{}{
			"template_code":       "bb_near_turn",
			"to":                  cand.CustomerPhone,
			"from_business_phone": w.cfg.BhejnaFromPhone,
			"location_id":         s.LocationID.String(),
			"notification_type":   "near_turn",
			"customer_id":         cand.CustomerID.String(),
			"source_type":         "queue_entry", // dedup key: same template + entry within window sends once
			"source_id":           cand.EntryID.String(),
			"components": []interface{}{
				map[string]interface{}{
					"type": "body",
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": s.LocationName},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(cand.PeopleAhead)},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(cand.EstimatedWaitMinutes)},
					},
				},
				map[string]interface{}{
					"type":     "button",
					"sub_type": "quick_reply",
					"index":    0,
					"parameters": []interface{}{
						map[string]interface{}{"type": "payload", "payload": "ON_THE_WAY:" + cand.EntryID.String()},
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
	var graceElapsed bool
	// Grace period uses DB wall-clock (NOW() vs near_turn_notified_at), so a
	// notification set earlier in this same tick cannot satisfy it.
	// near_turn_notified_at IS NULL (never notified) is never snooze-eligible.
	// [T3] "Next in dispatch order" now means next among entries some barber can
	// actually take. Without this an entry waiting on the shop's only Senior gets
	// snoozed because a Junior went idle — punished for a constraint the shop
	// sold them. An ineligible head is passed over as a snooze candidate, not
	// snoozed; it stays waiting and dispatchable.
	//
	// 'idle', not "on shift": snooze means a barber is free and waiting on you.
	// A barber mid-cut is not waiting on anyone. That is a deliberately stricter
	// test than the ETA divisor's status <> 'offline'.
	//
	// required_tier_id IS NULL short-circuits, so a shop with no tiers never
	// evaluates the EXISTS and behaves exactly as it did before T3.
	err := w.db.QueryRow(ctx, `
		SELECT id, presence_state, session_channel, visit_id, customer_id, token_number,
		       (near_turn_notified_at IS NOT NULL
		        AND near_turn_notified_at <= NOW() - ($2 * INTERVAL '1 minute')) AS grace_elapsed
		FROM queue_entries
		WHERE queue_session_id = $1
		  AND state = 'waiting'
		  AND is_dispatchable = true
		  AND (required_tier_id IS NULL
		       OR EXISTS (SELECT 1 FROM staff_members sm
		                  WHERE sm.location_id = $3
		                    AND sm.is_active = true
		                    AND sm.tier_id = queue_entries.required_tier_id
		                    AND sm.status = 'idle'))
		ORDER BY priority_group ASC, sort_key ASC
		LIMIT 1
	`, s.ID, snoozeGraceMinutes, s.LocationID).Scan(&top.ID, &top.PresenceState, &top.SessionChannel, &top.VisitID, &top.CustomerID, &top.TokenNumber, &graceElapsed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		log.Printf("Watchdog checkAutoSnooze query failed: %v", err)
		return
	}

	if graceElapsed && (top.PresenceState == "remote" || top.PresenceState == "notified") {
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
	var magicLinkExpiresAt *time.Time // [B7] nullable, same column as above

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
			  AND near_turn_notified_at IS NOT NULL
			  AND near_turn_notified_at <= NOW() - ($2 * INTERVAL '1 minute')
		`, top.ID, snoozeGraceMinutes)
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

		if top.SessionChannel == "whatsapp" && top.CustomerID != nil && magicLinkExpiresAt != nil {
			magicLinkToken := queue.GenerateMagicLinkToken(top.CustomerID.String(), s.LocationID.String(), top.VisitID.String(), *magicLinkExpiresAt, []byte(w.cfg.HMACSecret))

			outboxPayload := map[string]interface{}{
				"template_code":       "bb_queue_snoozed",
				"to":                  customerPhone,
				"from_business_phone": w.cfg.BhejnaFromPhone,
				"location_id":         s.LocationID.String(),
				"notification_type":   "queue_snoozed",
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

// tierUnavailableWarning marks a waiting entry whose required tier has nobody
// on shift. It rides stale_warning, which is already a free-form nullable string
// on the wire (QueueEntryStaff) with no enum in the spec and no CHECK on the
// column — so the shop sees the condition today without a schema or spec change.
const tierUnavailableWarning = "tier_unavailable"

// checkTierAvailability implements [T3] D4. An entry whose required tier has zero
// on-shift barbers is unservable and, before this, nobody noticed: it simply sat
// there while the queue moved around it.
//
// On-shift is status <> 'offline', so a barber on break still counts — a break
// ends, going offline does not. Only the shop sees this; no WhatsApp template is
// sent, because bb_tier_unavailable needs manual Meta submission and belongs to T4.
//
// The set and clear are two narrow statements rather than one CASE. A CASE that
// writes NULL in its ELSE branch would clear every waiting entry's stale_warning
// on every 60-second tick. Nothing else writes that column for waiting rows today,
// which makes such a statement safe by coincidence rather than by construction —
// and the day a unit adds a waiting-state warning, it would be silently wiped
// once a minute.
//
// So the set requires stale_warning IS NULL, not merely "not already ours".
// stale_warning holds one value, so somebody has to win; first writer wins, and
// this unit is never the one that clobbers. The clear touches only rows whose
// value is exactly the one we wrote. In both directions a warning this unit did
// not set comes out of a tick unchanged.
func (w *Watchdog) checkTierAvailability(ctx context.Context, s session) {
	var changed bool
	var newQueueVersion int

	err := repository.WithTx(ctx, w.db, func(tx pgx.Tx) error {
		// Law 1: lock session first.
		var sessionLockID uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM queue_sessions WHERE id = $1 FOR UPDATE", s.ID).
			Scan(&sessionLockID); err != nil {
			return err
		}

		set, err := tx.Exec(ctx, `
			UPDATE queue_entries qe SET stale_warning = $3
			WHERE qe.queue_session_id = $1
			  AND qe.state = 'waiting'
			  AND qe.required_tier_id IS NOT NULL
			  AND qe.stale_warning IS NULL
			  AND NOT EXISTS (SELECT 1 FROM staff_members sm
			                  WHERE sm.location_id = $2
			                    AND sm.is_active = true
			                    AND sm.tier_id = qe.required_tier_id
			                    AND sm.status <> 'offline')
		`, s.ID, s.LocationID, tierUnavailableWarning)
		if err != nil {
			return err
		}

		cleared, err := tx.Exec(ctx, `
			UPDATE queue_entries qe SET stale_warning = NULL
			WHERE qe.queue_session_id = $1
			  AND qe.state = 'waiting'
			  AND qe.stale_warning = $3
			  AND EXISTS (SELECT 1 FROM staff_members sm
			              WHERE sm.location_id = $2
			                AND sm.is_active = true
			                AND sm.tier_id = qe.required_tier_id
			                AND sm.status <> 'offline')
		`, s.ID, s.LocationID, tierUnavailableWarning)
		if err != nil {
			return err
		}

		// Both statements are no-ops when nothing transitions. Bumping the
		// version unconditionally would make a 60-second tick force a refetch
		// from every connected client of every idle shop, forever.
		changed = set.RowsAffected() > 0 || cleared.RowsAffected() > 0
		if !changed {
			return nil
		}

		return tx.QueryRow(ctx, `
			UPDATE queue_sessions
			SET queue_version = queue_version + 1
			WHERE id = $1
			RETURNING queue_version
		`, s.ID).Scan(&newQueueVersion)
	})

	if err != nil {
		log.Printf("Watchdog tier-availability check failed for session %s: %v", s.ID, err)
		return
	}
	if !changed {
		return
	}

	// Law 8: broadcast after commit.
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
