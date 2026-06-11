package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"barberbase-core/internal/config"
	"barberbase-core/internal/push"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/SherClockHolmes/webpush-go"
)

type PushHandler struct {
	Pool       *pgxpool.Pool
	Config     *config.Config
	HTTPClient webpush.HTTPClient
}

type WebPushSendPayload struct {
	LocationID string `json:"location_id"`
	TenantID   string `json:"tenant_id"`
}

func (s *PushHandler) HandleWebPushSend(ctx context.Context, event *OutboxEvent) error {
	// Step 1 — Parse payload
	var payload WebPushSendPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	if payload.LocationID == "" || payload.TenantID == "" {
		return fmt.Errorf("missing location_id or tenant_id in payload")
	}

	tenantUUID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		return fmt.Errorf("invalid tenant id: %w", err)
	}

	locUUID, err := uuid.Parse(payload.LocationID)
	if err != nil {
		return fmt.Errorf("invalid location id: %w", err)
	}

	// Step 2 — Frequency gate (Law 19)
	var count int
	err = s.Pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM queue_entries qe
		JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		WHERE qs.location_id = $1
		  AND qs.business_date = CURRENT_DATE
		  AND qe.state = 'waiting'
		  AND qe.is_dispatchable = true
		  AND qe.presence_state = 'arrived'
	`, locUUID).Scan(&count)
	if err != nil {
		return fmt.Errorf("frequency gate query failed: %w", err)
	}

	if count == 0 {
		_, err = s.Pool.Exec(ctx, `
			UPDATE outbox_events
			SET status = 'dispatched', dispatched_at = NOW()
			WHERE id = $1
		`, event.ID)
		if err != nil {
			return fmt.Errorf("failed to update outbox event status: %w", err)
		}
		return nil
	}

	// Step 3 — Fetch push-enabled staff
	rows, err := s.Pool.Query(ctx, `
		SELECT id, tenant_id, location_id, push_endpoint, push_p256dh, push_auth
		FROM staff_members
		WHERE location_id = $1
		  AND push_enabled = true
		  AND is_active = true
	`, locUUID)
	if err != nil {
		return fmt.Errorf("failed to query push-enabled staff: %w", err)
	}
	defer rows.Close()

	type staffMember struct {
		ID           string
		TenantID     string
		LocationID   string
		PushEndpoint *string
		PushP256dh   *string
		PushAuth     *string
	}

	var staffList []staffMember
	for rows.Next() {
		var sm staffMember
		err := rows.Scan(&sm.ID, &sm.TenantID, &sm.LocationID, &sm.PushEndpoint, &sm.PushP256dh, &sm.PushAuth)
		if err != nil {
			return fmt.Errorf("failed to scan staff member: %w", err)
		}
		staffList = append(staffList, sm)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration error: %w", err)
	}

	if len(staffList) == 0 {
		_, err = s.Pool.Exec(ctx, `
			UPDATE outbox_events
			SET status = 'dispatched', dispatched_at = NOW()
			WHERE id = $1
		`, event.ID)
		if err != nil {
			return fmt.Errorf("failed to update outbox event status: %w", err)
		}
		return nil
	}

	// Step 4 — Per staff member loop
	for _, sm := range staffList {
		if sm.PushEndpoint == nil || sm.PushP256dh == nil || sm.PushAuth == nil {
			continue
		}

		_ = func(sm staffMember) error {
			smIDUUID, err := uuid.Parse(sm.ID)
			if err != nil {
				log.Printf("web_push: invalid staff member id: %v", err)
				return nil
			}

			// 4a. Generate PAT
			pat, err := push.GeneratePAT([]byte(s.Config.HMACSecret), sm.ID, sm.LocationID)
			if err != nil {
				log.Printf("web_push: failed to generate PAT for staff %s: %v", sm.ID, err)
				s.writeFailedPushEvent(ctx, tenantUUID, locUUID, smIDUUID, err.Error())
				return nil
			}

			// 4b. Build message JSON
			apiURL := os.Getenv("API_BASE_URL")
			if apiURL == "" {
				apiURL = "https://api.barberbase.in/v1"
			}
			msg := map[string]interface{}{
				"pat":           pat,
				"api_url":       apiURL,
				"location_id":   payload.LocationID,
				"waiting_count": count,
				"title":         "BarberBase",
				"body":          fmt.Sprintf("%d waiting — tap to call next", count),
			}
			payloadBytes, err := json.Marshal(msg)
			if err != nil {
				log.Printf("web_push: failed to marshal message: %v", err)
				s.writeFailedPushEvent(ctx, tenantUUID, locUUID, smIDUUID, err.Error())
				return nil
			}

			// 4c. Encrypt and send via webpush-go
			resp, err := webpush.SendNotification(
				payloadBytes,
				&webpush.Subscription{
					Endpoint: *sm.PushEndpoint,
					Keys: webpush.Keys{
						P256dh: *sm.PushP256dh,
						Auth:   *sm.PushAuth,
					},
				},
				&webpush.Options{
					HTTPClient:      s.HTTPClient,
					Subscriber:      s.Config.VAPIDSubject,
					VAPIDPublicKey:  s.Config.VAPIDPublicKey,
					VAPIDPrivateKey: s.Config.VAPIDPrivateKey,
					Urgency:         webpush.UrgencyHigh,
					TTL:             3600,
				},
			)
			if resp != nil {
				defer resp.Body.Close()
			}

			if err != nil {
				log.Printf("web_push: send notification failed: %v", err)
				s.writeFailedPushEvent(ctx, tenantUUID, locUUID, smIDUUID, err.Error())
				return nil
			}

			// 4d. On HTTP 410 Gone — disable first, log second
			if resp.StatusCode == http.StatusGone {
				log.Printf("web_push: 410 Gone for staff %s, disabling push", sm.ID)
				_, dbErr := s.Pool.Exec(ctx, `
					UPDATE staff_members
					SET push_enabled = false,
						push_endpoint = NULL,
						push_p256dh = NULL,
						push_auth = NULL
					WHERE id = $1
				`, smIDUUID)
				if dbErr != nil {
					log.Printf("web_push: failed to disable push for staff: %v", dbErr)
				}

				_, dbErr = s.Pool.Exec(ctx, `
					INSERT INTO notification_events (
						tenant_id, location_id, channel, notification_type,
						source_type, source_id, status, error_message, created_at
					) VALUES (
						$1, $2, 'web_push', 'push_call_next',
						'staff_member', $3, 'failed', '410_gone', NOW()
					)
				`, tenantUUID, locUUID, smIDUUID)
				if dbErr != nil {
					log.Printf("web_push: failed to write 410_gone event: %v", dbErr)
				}
				return nil
			}

			// 4e. On HTTP 2xx — insert sent event
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				_, dbErr := s.Pool.Exec(ctx, `
					INSERT INTO notification_events (
						tenant_id, location_id, channel, notification_type,
						customer_id, recipient_phone,
						source_type, source_id,
						status, sent_at, created_at
					) VALUES (
						$1, $2, 'web_push', 'push_call_next',
						NULL, NULL,
						'staff_member', $3,
						'sent', NOW(), NOW()
					)
				`, tenantUUID, locUUID, smIDUUID)
				if dbErr != nil {
					log.Printf("web_push: failed to write success event: %v", dbErr)
				}
				return nil
			}

			// 4f. On any other error
			statusStr := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)
			log.Printf("web_push: push returned non-2xx status for staff %s: %s", sm.ID, statusStr)
			s.writeFailedPushEvent(ctx, tenantUUID, locUUID, smIDUUID, statusStr)
			return nil
		}(sm)
	}

	// Step 5 — Mark outbox dispatched
	_, err = s.Pool.Exec(ctx, `
		UPDATE outbox_events
		SET status = 'dispatched', dispatched_at = NOW()
		WHERE id = $1
	`, event.ID)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event dispatched: %w", err)
	}

	return nil
}

func (s *PushHandler) writeFailedPushEvent(ctx context.Context, tenantUUID, locUUID, smIDUUID uuid.UUID, errMsg string) {
	_, dbErr := s.Pool.Exec(ctx, `
		INSERT INTO notification_events (
			tenant_id, location_id, channel, notification_type,
			customer_id, recipient_phone,
			source_type, source_id,
			status, error_message, created_at
		) VALUES (
			$1, $2, 'web_push', 'push_call_next',
			NULL, NULL,
			'staff_member', $3,
			'failed', $4, NOW()
		)
	`, tenantUUID, locUUID, smIDUUID, errMsg)
	if dbErr != nil {
		log.Printf("web_push: failed to write failed push event: %v", dbErr)
	}
}
