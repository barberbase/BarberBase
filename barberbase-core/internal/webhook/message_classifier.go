package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"barberbase-core/internal/repository"
)

var ratingButtonRegex = regexp.MustCompile(`^RATING:([1-5]):(.+)$`)

func (p *Processor) classifyRatingButton(ctx context.Context, tenantID uuid.UUID, customerID *uuid.UUID, buttonPayload string) error {
	matches := ratingButtonRegex.FindStringSubmatch(buttonPayload)
	if len(matches) != 3 {
		return nil
	}
	ratingVal, err := strconv.Atoi(matches[1])
	if err != nil || ratingVal < 1 || ratingVal > 5 {
		return nil
	}
	visitIDStr := matches[2]
	visitID, err := uuid.Parse(visitIDStr)
	if err != nil {
		return nil
	}

	var (
		frID            uuid.UUID
		frTenantID      uuid.UUID
		frLocationID    uuid.UUID
		frVisitID       uuid.UUID
		frCustomerID    *uuid.UUID
		frStaffMemberID *uuid.UUID
		frExpiresAt     *time.Time
		frStatus        string
	)

	query := `
		SELECT fr.id, fr.tenant_id, fr.location_id, fr.visit_id,
		       fr.customer_id, fr.staff_member_id, fr.expires_at, fr.status
		FROM feedback_requests fr
		WHERE fr.visit_id  = $1
		  AND fr.tenant_id = $2
		LIMIT 1
	`
	err = p.pool.QueryRow(ctx, query, visitID, tenantID).Scan(
		&frID, &frTenantID, &frLocationID, &frVisitID,
		&frCustomerID, &frStaffMemberID, &frExpiresAt, &frStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	if frStatus == "responded" {
		return nil
	}

	isLate := false
	if frExpiresAt != nil && time.Now().After(*frExpiresAt) {
		isLate = true
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var responseID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO feedback_responses (
			tenant_id, location_id, feedback_request_id, visit_id,
			customer_id, staff_member_id, rating, source, is_late, received_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, 'whatsapp', $8, NOW()
		)
		ON CONFLICT (tenant_id, feedback_request_id) DO NOTHING
		RETURNING id
	`, tenantID, frLocationID, frID, frVisitID, frCustomerID, frStaffMemberID, ratingVal, isLate).Scan(&responseID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return nil
		}
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE feedback_requests
		SET status = 'responded',
		    updated_at = NOW()
		WHERE id = $1
	`, frID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (p *Processor) classifyRatingPlainText(ctx context.Context, tenantID uuid.UUID, customerID *uuid.UUID, body string, msg ClassifiedMessage) error {
	bodyTrimmed := strings.TrimSpace(body)
	if len(bodyTrimmed) != 1 || bodyTrimmed[0] < '1' || bodyTrimmed[0] > '5' {
		return p.handleUnknown(ctx, msg)
	}
	ratingVal := int(bodyTrimmed[0] - '0')

	var (
		frID            uuid.UUID
		frTenantID      uuid.UUID
		frLocationID    uuid.UUID
		frVisitID       uuid.UUID
		frCustomerID    *uuid.UUID
		frStaffMemberID *uuid.UUID
		frExpiresAt     *time.Time
		frStatus        string
	)

	query := `
		SELECT fr.id, fr.tenant_id, fr.location_id, fr.visit_id,
		       fr.customer_id, fr.staff_member_id, fr.expires_at, fr.status
		FROM feedback_requests fr
		WHERE fr.tenant_id = $1
		  AND fr.customer_id = $2
		  AND fr.status IN ('scheduled', 'sent')
		ORDER BY fr.created_at DESC
		LIMIT 1
	`
	err := p.pool.QueryRow(ctx, query, tenantID, customerID).Scan(
		&frID, &frTenantID, &frLocationID, &frVisitID,
		&frCustomerID, &frStaffMemberID, &frExpiresAt, &frStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p.handleUnknown(ctx, msg)
		}
		return err
	}

	if frStatus == "responded" {
		return nil
	}

	isLate := false
	if frExpiresAt != nil && time.Now().After(*frExpiresAt) {
		isLate = true
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var responseID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO feedback_responses (
			tenant_id, location_id, feedback_request_id, visit_id,
			customer_id, staff_member_id, rating, source, is_late, received_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, 'whatsapp', $8, NOW()
		)
		ON CONFLICT (tenant_id, feedback_request_id) DO NOTHING
		RETURNING id
	`, tenantID, frLocationID, frID, frVisitID, frCustomerID, frStaffMemberID, ratingVal, isLate).Scan(&responseID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return nil
		}
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE feedback_requests
		SET status = 'responded',
		    updated_at = NOW()
		WHERE id = $1
	`, frID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type MessageAction int

const (
	ActionJoin          MessageAction = iota // "JOIN {slug} {token_code}"
	ActionOnTheWay                           // button_payload "ON_THE_WAY:{entry_id}"
	ActionCancel                             // button_payload "CANCEL:{entry_id}"
	ActionCancelApt                          // button_payload "CANCEL_APT:{apt_id}"
	ActionRatingButton                       // button_payload "RATING:{n}:{visit_id}"
	ActionOptOutButton                       // button_payload "OPT_OUT_MARKETING"
	ActionPlainRating                        // body exactly "1"–"5"
	ActionStop                               // body "STOP" or "UNSUBSCRIBE"
	ActionStatusUpdated                      // event_type = "message.status_updated" — no-op
	ActionUnknown                            // send help text
)

type ClassifiedMessage struct {
	Action      MessageAction
	SenderPhone string     // E.164 normalized; "" if masked
	BSUID       string     // sender.bsuid; "" if absent
	IsMasked    bool       // true when sender.is_phone_masked
	DisplayName string     // sender.display_name

	// ActionJoin only
	SlugFromBody string    // display context; EXACT equality only, never LIKE
	TokenCode    string    // authoritative tenant anchor

	// Button-payload actions
	EntryID  string        // UUID string: ON_THE_WAY, CANCEL
	VisitID  string        // UUID string: RATING button
	AptID    string        // UUID string: CANCEL_APT
	Rating   int           // 1–5

	// Set by ingress handler; carried on webhook_events row
	LocationID *uuid.UUID  // nil = Mode A; non-nil = Mode B (already known)
}

// SSEBroadcaster is implemented by the SSE manager (built in C2.x).
// Wire NoopBroadcaster until that unit is committed.
type SSEBroadcaster interface {
	Broadcast(locationID uuid.UUID, queueVersion int)
}

type NoopBroadcaster struct{}

func (NoopBroadcaster) Broadcast(_ uuid.UUID, _ int) {}

type bhejnaPayload struct {
	BhejnaEventID       string `json:"bhejna_event_id"`
	EventType           string `json:"event_type"`
	Channel             string `json:"channel"`
	BusinessPhoneNumber string `json:"business_phone_number"`
	Sender              *struct {
		WhatsappIdentifier string `json:"whatsapp_identifier"`
		Bsuid              string `json:"bsuid"`
		PhoneNumber        string `json:"phone_number"`
		IsPhoneMasked      bool   `json:"is_phone_masked"`
		DisplayName        string `json:"display_name"`
	} `json:"sender"`
	Message *struct {
		MetaMessageID  string `json:"meta_message_id"`
		Type           string `json:"type"`
		Body           string `json:"body"`
		ButtonPayload  string `json:"button_payload"`
	} `json:"message"`
}

// Classify parses the inbound Bhejna JSON payload and classifies it.
func Classify(payload []byte, locationID *uuid.UUID) (ClassifiedMessage, error) {
	var raw bhejnaPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return ClassifiedMessage{}, err
	}

	classified := ClassifiedMessage{
		LocationID: locationID,
	}

	if raw.EventType == "message.status_updated" {
		classified.Action = ActionStatusUpdated
		return classified, nil
	}

	if raw.EventType != "message.received" {
		classified.Action = ActionUnknown
		return classified, nil
	}

	if raw.Sender != nil {
		classified.SenderPhone = repository.NormalizeE164(raw.Sender.PhoneNumber)
		classified.BSUID = raw.Sender.Bsuid
		classified.IsMasked = raw.Sender.IsPhoneMasked
		classified.DisplayName = raw.Sender.DisplayName
	}

	if raw.Message == nil {
		classified.Action = ActionUnknown
		return classified, nil
	}

	msg := raw.Message

	// Button payload takes precedence
	if msg.ButtonPayload != "" {
		buttonPayload := msg.ButtonPayload
		if strings.HasPrefix(buttonPayload, "ON_THE_WAY:") {
			classified.Action = ActionOnTheWay
			classified.EntryID = strings.TrimPrefix(buttonPayload, "ON_THE_WAY:")
		} else if strings.HasPrefix(buttonPayload, "CANCEL_APT:") {
			classified.Action = ActionCancelApt
			classified.AptID = strings.TrimPrefix(buttonPayload, "CANCEL_APT:")
		} else if strings.HasPrefix(buttonPayload, "CANCEL:") {
			classified.Action = ActionCancel
			classified.EntryID = strings.TrimPrefix(buttonPayload, "CANCEL:")
		} else if strings.HasPrefix(buttonPayload, "RATING:") {
			parts := strings.Split(buttonPayload, ":")
			if len(parts) == 3 {
				ratingVal, err := strconv.Atoi(parts[1])
				if err == nil && ratingVal >= 1 && ratingVal <= 5 {
					classified.Action = ActionRatingButton
					classified.Rating = ratingVal
					classified.VisitID = parts[2]
				} else {
					classified.Action = ActionUnknown
				}
			} else {
				classified.Action = ActionUnknown
			}
		} else if buttonPayload == "OPT_OUT_MARKETING" {
			classified.Action = ActionOptOutButton
		} else {
			classified.Action = ActionUnknown
		}
		return classified, nil
	}

	// Body based (type == text)
	if msg.Type == "text" {
		bodyTrimmed := strings.TrimSpace(msg.Body)
		upperBody := strings.ToUpper(bodyTrimmed)

		if strings.HasPrefix(upperBody, "JOIN ") {
			parts := strings.Fields(bodyTrimmed)
			if len(parts) < 3 {
				classified.Action = ActionUnknown
				return classified, nil
			}
			classified.Action = ActionJoin
			classified.TokenCode = parts[len(parts)-1]
			classified.SlugFromBody = strings.Join(parts[1:len(parts)-1], " ")
			return classified, nil
		}

		if len(bodyTrimmed) == 1 && bodyTrimmed[0] >= '1' && bodyTrimmed[0] <= '5' {
			classified.Action = ActionPlainRating
			classified.Rating = int(bodyTrimmed[0] - '0')
			return classified, nil
		}

		if upperBody == "STOP" || upperBody == "UNSUBSCRIBE" {
			classified.Action = ActionStop
			return classified, nil
		}
	}

	classified.Action = ActionUnknown
	return classified, nil
}
