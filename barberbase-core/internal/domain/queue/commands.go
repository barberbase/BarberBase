package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"barberbase-core/internal/repository"
)

// Commands is the domain service for all queue mutations.
// C2.2–C2.6 add methods to this type (JoinQueue, CallNext, Start, etc.).
// Every method must call lockSession as its first operation inside the tx.
type Commands struct {
	pool *pgxpool.Pool
}

// NewCommands constructs the queue domain service.
// Wire once in cmd/server/main.go; attach to api.Server.
func NewCommands(pool *pgxpool.Pool) *Commands {
	return &Commands{pool: pool}
}

// lockSession is the single enforced entry point for the upsert-then-lock pattern.
// Every queue mutation method calls this first inside its transaction.
// Package-private; not exposed outside the queue domain package.
func lockSession(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, locationID uuid.UUID,
	businessDate time.Time,
) (*repository.QueueSession, error) {
	return repository.EnsureAndLockQueueSession(ctx, tx, tenantID, locationID, businessDate)
}

// Error definitions for queue mutations
var (
	ErrRequestInFlight  = errors.New("request in flight, retry")
	ErrShopNotAccepting = errors.New("shop not accepting new customers")
	ErrQueueFull        = errors.New("queue is at capacity")
	ErrInvalidVariants  = errors.New("one or more variant IDs not found")
	ErrInactiveVariant  = errors.New("one or more variants are not available")
	ErrInvalidBarber    = errors.New("requested barber is not available")
)

type ErrAlreadyInQueue struct {
	ExistingEntryID uuid.UUID
}

func (e *ErrAlreadyInQueue) Error() string {
	return "customer already in queue"
}

type JoinQueueParams struct {
	TenantID          uuid.UUID
	LocationID        uuid.UUID
	VariantIDs        []uuid.UUID
	PartySize         int
	CustomerName      *string
	PhoneNumber       *string // E.164 normalized before calling
	BSUID             *string
	RequestedBarberID *uuid.UUID
	IdempotencyKey    string
	InitiatedVia      string // 'whatsapp'|'web_form'|'staff_dashboard'|'ai_agent'
	MaxQueueSize      int    // resolved from locations before calling
	HMACSecret        []byte
}

type JoinQueueResult struct {
	QueueEntryID       uuid.UUID
	VisitID            uuid.UUID
	TokenNumber        int
	NewQueueVersion    int
	PresenceState      string
	MagicLinkToken     string
	MagicLinkExpiresAt time.Time
	WhatsAppSent       bool
	IsIdempotentReplay bool
	StoredResponse     []byte // non-nil on idempotent replay
}

type localJoinQueueResponse struct {
	QueueEntry     localQueueEntryPublic `json:"queue_entry"`
	MagicLinkToken string                `json:"magic_link_token"`
	MagicLinkURL   string                `json:"magic_link_url"`
	WhatsAppSent   bool                  `json:"whatsapp_sent"`
}

type localQueueEntryPublic struct {
	ID                   uuid.UUID          `json:"id"`
	TokenNumber          int                `json:"token_number"`
	State                string             `json:"state"`
	PresenceState        string             `json:"presence_state"`
	PositionAhead        int                `json:"position_ahead"`
	EstimatedWaitMinutes int                `json:"estimated_wait_minutes"`
	Services             []localServiceJSON `json:"services"`
	PartySize            int                `json:"party_size"`
	MagicLinkExpiresAt   *string            `json:"magic_link_expires_at,omitempty"`
	ShopName             string             `json:"shop_name"`
	LocationName         string             `json:"location_name"`
}

type localServiceJSON struct {
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
}

func generateMagicLinkToken(customerIDStr, locationIDStr, visitIDStr string, expiresAt time.Time, secret []byte) string {
	tokenPayload := customerIDStr + "|" + locationIDStr + "|" + visitIDStr + "|" + strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(tokenPayload))
	hashed := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(hashed)
}

func JoinQueue(ctx context.Context, tx pgx.Tx, params JoinQueueParams) (*JoinQueueResult, error) {
	// ── STEP 0: IDEMPOTENCY GATE (first op in tx) ──
	var idempotencyRowID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO idempotency_keys (tenant_id, key, endpoint, created_at)
		VALUES ($1, $2, 'queue.join', NOW())
		ON CONFLICT (tenant_id, key, endpoint) DO NOTHING
		RETURNING id`, params.TenantID, params.IdempotencyKey).Scan(&idempotencyRowID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Case A — no row returned: check if key is active or request is in flight
			var responseStatus *int
			var responseBody []byte
			var expiresAt time.Time
			errSelect := tx.QueryRow(ctx, `
				SELECT response_status, response_body, expires_at FROM idempotency_keys
				WHERE tenant_id = $1 AND key = $2 AND endpoint = 'queue.join'`,
				params.TenantID, params.IdempotencyKey).Scan(&responseStatus, &responseBody, &expiresAt)
			if errSelect != nil {
				return nil, fmt.Errorf("lookup idempotency key: %w", errSelect)
			}

			if expiresAt.Before(time.Now()) {
				// The key is expired. Delete it to satisfy the UNIQUE constraint, then insert a new one.
				_, errDel := tx.Exec(ctx, `
					DELETE FROM idempotency_keys
					WHERE tenant_id = $1 AND key = $2 AND endpoint = 'queue.join'`,
					params.TenantID, params.IdempotencyKey)
				if errDel != nil {
					return nil, fmt.Errorf("delete expired idempotency key: %w", errDel)
				}
				err = tx.QueryRow(ctx, `
					INSERT INTO idempotency_keys (tenant_id, key, endpoint, created_at)
					VALUES ($1, $2, 'queue.join', NOW())
					RETURNING id`, params.TenantID, params.IdempotencyKey).Scan(&idempotencyRowID)
				if err != nil {
					return nil, fmt.Errorf("re-insert idempotency key: %w", err)
				}
			} else {
				// Active key
				if responseBody != nil {
					// response_body IS NOT NULL -> COMMIT tx, return stored response
					return &JoinQueueResult{
						IsIdempotentReplay: true,
						StoredResponse:     responseBody,
					}, nil
				} else {
					// response_body IS NULL -> COMMIT tx, return 409
					return nil, ErrRequestInFlight
				}
			}
		} else {
			return nil, fmt.Errorf("insert idempotency key: %w", err)
		}
	}

	// ── STEP 1: UPSERT + LOCK QUEUE SESSION (Law 1) ──
	_, err = tx.Exec(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status, queue_version, last_token_number)
		VALUES ($1, $2, CURRENT_DATE, 'active', 0, 0)
		ON CONFLICT (location_id, business_date) DO NOTHING`, params.TenantID, params.LocationID)
	if err != nil {
		return nil, fmt.Errorf("upsert queue session: %w", err)
	}

	var sessionID uuid.UUID
	var lastTokenNumber int
	var queueVersion int
	var sessionStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, last_token_number, queue_version, status
		FROM queue_sessions
		WHERE location_id = $1 AND business_date = CURRENT_DATE
		FOR UPDATE`, params.LocationID).Scan(&sessionID, &lastTokenNumber, &queueVersion, &sessionStatus)
	if err != nil {
		return nil, fmt.Errorf("lock queue session: %w", err)
	}

	// ── STEP 2: VALIDATE SESSION + CAPACITY ──
	if sessionStatus == "closed" || sessionStatus == "archived" || sessionStatus == "paused" {
		return nil, ErrShopNotAccepting
	}

	var activeCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM queue_entries
		WHERE queue_session_id = $1 AND state IN ('waiting', 'called', 'in_progress')`, sessionID).Scan(&activeCount)
	if err != nil {
		return nil, fmt.Errorf("count active entries: %w", err)
	}

	if activeCount >= params.MaxQueueSize {
		return nil, ErrQueueFull
	}

	// ── STEP 3: RESOLVE OR CREATE CUSTOMER ──
	var customerID *uuid.UUID
	if params.PhoneNumber != nil && *params.PhoneNumber != "" {
		var cID uuid.UUID
		var existingName *string
		err = tx.QueryRow(ctx, `
			SELECT id, name FROM customers
			WHERE tenant_id = $1 AND phone_number = $2 AND merged_into_customer_id IS NULL
			FOR UPDATE`, params.TenantID, *params.PhoneNumber).Scan(&cID, &existingName)
		if err == nil {
			customerID = &cID
			if params.CustomerName != nil && *params.CustomerName != "" && (existingName == nil || *existingName == "") {
				_, err = tx.Exec(ctx, `
					UPDATE customers SET name = $1, updated_at = NOW() WHERE id = $2`, params.CustomerName, cID)
				if err != nil {
					return nil, fmt.Errorf("update customer name: %w", err)
				}
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				INSERT INTO customers (tenant_id, phone_number, name, created_at, updated_at)
				VALUES ($1, $2, $3, NOW(), NOW())
				ON CONFLICT (tenant_id, phone_number) DO NOTHING
				RETURNING id`, params.TenantID, *params.PhoneNumber, params.CustomerName).Scan(&cID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					// Concurrent insert occurred. Re-select.
					err = tx.QueryRow(ctx, `
						SELECT id, name FROM customers
						WHERE tenant_id = $1 AND phone_number = $2 AND merged_into_customer_id IS NULL
						FOR UPDATE`, params.TenantID, *params.PhoneNumber).Scan(&cID, &existingName)
					if err != nil {
						return nil, fmt.Errorf("re-select customer after conflict: %w", err)
					}
					customerID = &cID
					if params.CustomerName != nil && *params.CustomerName != "" && (existingName == nil || *existingName == "") {
						_, err = tx.Exec(ctx, `
							UPDATE customers SET name = $1, updated_at = NOW() WHERE id = $2`, params.CustomerName, cID)
						if err != nil {
							return nil, fmt.Errorf("update customer name after conflict: %w", err)
						}
					}
				} else {
					return nil, fmt.Errorf("insert customer: %w", err)
				}
			} else {
				customerID = &cID
			}
		} else {
			return nil, fmt.Errorf("lookup customer: %w", err)
		}

		if params.BSUID != nil && *params.BSUID != "" && customerID != nil {
			_, err = tx.Exec(ctx, `
				INSERT INTO customer_identities (customer_id, provider, provider_id)
				VALUES ($1, 'whatsapp', $2)
				ON CONFLICT (provider, provider_id) DO NOTHING`, *customerID, *params.BSUID)
			if err != nil {
				return nil, fmt.Errorf("insert customer identity: %w", err)
			}
		}
	}

	// Barber validation
	if params.RequestedBarberID != nil {
		var exists bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM staff_members
				WHERE id = $1 AND location_id = $2 AND is_active = true
			)`, params.RequestedBarberID, params.LocationID).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("validate requested barber: %w", err)
		}
		if !exists {
			return nil, ErrInvalidBarber
		}
	}

	// ── STEP 4: VALIDATE VARIANT IDs + COMPUTE TOTALS ──
	type serviceDetail struct {
		id              uuid.UUID
		name            string
		groupName       string
		categoryName    string
		durationMinutes int
		pricePaise      int
		isActive        bool
		groupActive     bool
		catActive       bool
	}

	rows, err := tx.Query(ctx, `
		SELECT sv.id, sv.name AS variant_name, sg.name AS group_name,
		       sc.name AS category_name, sv.duration_minutes, sv.price_paise,
		       sv.is_active, sg.is_active AS group_active, sc.is_active AS cat_active
		FROM service_variants sv
		JOIN service_groups sg ON sg.id = sv.group_id
		JOIN service_categories sc ON sc.id = sg.category_id
		WHERE sv.id = ANY($1) AND sv.location_id = $2`, params.VariantIDs, params.LocationID)
	if err != nil {
		return nil, fmt.Errorf("query service variants: %w", err)
	}
	defer rows.Close()

	variantMap := make(map[uuid.UUID]serviceDetail)
	for rows.Next() {
		var sd serviceDetail
		err = rows.Scan(&sd.id, &sd.name, &sd.groupName, &sd.categoryName, &sd.durationMinutes, &sd.pricePaise, &sd.isActive, &sd.groupActive, &sd.catActive)
		if err != nil {
			return nil, fmt.Errorf("scan service variant: %w", err)
		}
		variantMap[sd.id] = sd
	}

	for _, id := range params.VariantIDs {
		sd, ok := variantMap[id]
		if !ok {
			return nil, ErrInvalidVariants
		}
		if !sd.isActive || !sd.groupActive || !sd.catActive {
			return nil, ErrInactiveVariant
		}
	}

	var totalDurationMinutes int
	for _, id := range params.VariantIDs {
		totalDurationMinutes += variantMap[id].durationMinutes
	}
	totalDurationMinutes = totalDurationMinutes * params.PartySize

	// ── STEP 5: GENERATE VISIT IDs + MAGIC LINK TOKEN ──
	visitID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate visit id: %w", err)
	}

	var magicLinkToken string
	var magicLinkTokenHash *string
	var magicLinkExpiresAt *time.Time
	var expiresAtVal time.Time

	if customerID != nil {
		expiresAtVal = time.Now().Add(23 * time.Hour)
		magicLinkExpiresAt = &expiresAtVal
		token := generateMagicLinkToken(customerID.String(), params.LocationID.String(), visitID.String(), expiresAtVal, params.HMACSecret)
		magicLinkToken = token
		magicLinkTokenHash = &token
	}

	// ── STEP 6: INSERT VISIT ──
	err = repository.InsertVisit(ctx, tx, &repository.VisitRow{
		ID:                   visitID,
		TenantID:             params.TenantID,
		LocationID:           params.LocationID,
		CustomerID:           customerID,
		EntryType:            "walk_in",
		Status:               "active",
		InitiatedVia:         params.InitiatedVia,
		PartySize:            params.PartySize,
		TotalDurationMinutes: totalDurationMinutes,
		MagicLinkTokenHash:   magicLinkTokenHash,
		MagicLinkExpiresAt:   magicLinkExpiresAt,
		IdempotencyKey:       &params.IdempotencyKey,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Catch duplicate insert failure on visits.idempotency_key
			var existingVisitID uuid.UUID
			errLookup := tx.QueryRow(ctx, `
				SELECT id FROM visits WHERE idempotency_key = $1`, params.IdempotencyKey).Scan(&existingVisitID)
			if errLookup == nil {
				var existingEntryID uuid.UUID
				errEntryLookup := tx.QueryRow(ctx, `
					SELECT id FROM queue_entries WHERE visit_id = $1`, existingVisitID).Scan(&existingEntryID)
				if errEntryLookup == nil {
					return nil, &ErrAlreadyInQueue{ExistingEntryID: existingEntryID}
				}
			}
		}
		return nil, fmt.Errorf("insert visit: %w", err)
	}

	// ── STEP 7: INSERT VISIT_SERVICES (immutable snapshot, Law 10) ──
	visitServices := make([]repository.VisitServiceRow, len(params.VariantIDs))
	for i, id := range params.VariantIDs {
		sd := variantMap[id]
		variantID := id
		visitServices[i] = repository.VisitServiceRow{
			ServiceVariantID: &variantID,
			VariantName:      sd.name,
			GroupName:        sd.groupName,
			CategoryName:     sd.categoryName,
			DurationMinutes:  sd.durationMinutes,
			PricePaise:       sd.pricePaise,
			SortOrder:        i,
		}
	}
	err = repository.InsertVisitServices(ctx, tx, visitID, visitServices)
	if err != nil {
		return nil, fmt.Errorf("insert visit services: %w", err)
	}

	// ── STEP 8: INSERT QUEUE_ENTRY ──
	tokenNumber := lastTokenNumber + 1
	entryID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate queue entry id: %w", err)
	}

	presenceState := "remote"
	if params.InitiatedVia == "staff_dashboard" && (params.PhoneNumber == nil || *params.PhoneNumber == "") {
		presenceState = "unknown"
	}

	sessionChannel := "web"
	if params.InitiatedVia == "whatsapp" || params.InitiatedVia == "ai_agent" {
		sessionChannel = "whatsapp"
	}

	sortKey := time.Now().UnixMilli()

	if customerID != nil {
		existing, errGet := repository.GetQueueEntryByCustomer(ctx, tx, sessionID, *customerID)
		if errGet == nil && existing != nil {
			return nil, &ErrAlreadyInQueue{ExistingEntryID: existing.ID}
		}
	}

	err = repository.InsertQueueEntry(ctx, tx, &repository.QueueEntryRow{
		ID:                entryID,
		VisitID:           visitID,
		QueueSessionID:    sessionID,
		CustomerID:        customerID,
		TokenNumber:       tokenNumber,
		State:             "waiting",
		PresenceState:     presenceState,
		SessionChannel:    sessionChannel,
		IsDispatchable:    true,
		RequestedBarberID: params.RequestedBarberID,
		PriorityGroup:     100,
		SortKey:           sortKey,
		RemoteJoinedAt:    time.Now(),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, &ErrAlreadyInQueue{}
		}
		return nil, fmt.Errorf("insert queue entry: %w", err)
	}

	// ── STEP 9: UPDATE QUEUE SESSION ──
	var newQueueVersion int
	err = tx.QueryRow(ctx, `
		UPDATE queue_sessions
		SET last_token_number = last_token_number + 1,
		    queue_version     = queue_version + 1
		WHERE id = $1
		RETURNING queue_version`, sessionID).Scan(&newQueueVersion)
	if err != nil {
		return nil, fmt.Errorf("update queue session: %w", err)
	}

	// ── STEP 10: INSERT OUTBOX EVENT (Law 7 — inside tx) ──
	var whatsAppSent bool
	if params.PhoneNumber != nil && *params.PhoneNumber != "" && customerID != nil {
		payloadMap := map[string]interface{}{
			"template_id":    "bb_queue_joined",
			"visit_id":        visitID.String(),
			"location_id":     params.LocationID.String(),
			"queue_entry_id":  entryID.String(),
			"token_number":    tokenNumber,
			"customer_id":     customerID.String(),
		}
		payloadBytes, errMarshal := json.Marshal(payloadMap)
		if errMarshal != nil {
			return nil, fmt.Errorf("marshal outbox payload: %w", errMarshal)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (tenant_id, type, payload, status, process_after, created_at)
			VALUES ($1, 'notification.send', $2, 'pending', NOW(), NOW())`,
			params.TenantID, payloadBytes)
		if err != nil {
			return nil, fmt.Errorf("insert outbox event: %w", err)
		}
		if params.InitiatedVia != "staff_dashboard" {
			whatsAppSent = true
		}
	}

	// ── STEP 11: UPDATE IDEMPOTENCY KEY ──
	var locationName string
	err = tx.QueryRow(ctx, `SELECT name FROM locations WHERE id = $1`, params.LocationID).Scan(&locationName)
	if err != nil {
		return nil, fmt.Errorf("query location name for idempotency: %w", err)
	}

	localServices := make([]localServiceJSON, len(params.VariantIDs))
	for i, id := range params.VariantIDs {
		sd := variantMap[id]
		localServices[i] = localServiceJSON{
			Name:            sd.name,
			DurationMinutes: sd.durationMinutes,
		}
	}

	var expiresAtPtr *string
	if magicLinkExpiresAt != nil {
		formatted := magicLinkExpiresAt.Format(time.RFC3339)
		expiresAtPtr = &formatted
	}

	resp := localJoinQueueResponse{
		QueueEntry: localQueueEntryPublic{
			ID:                   entryID,
			TokenNumber:          tokenNumber,
			State:                "waiting",
			PresenceState:        presenceState,
			PositionAhead:        0,
			EstimatedWaitMinutes: totalDurationMinutes,
			Services:             localServices,
			PartySize:            params.PartySize,
			MagicLinkExpiresAt:   expiresAtPtr,
			ShopName:             locationName,
			LocationName:         locationName,
		},
		MagicLinkToken: magicLinkToken,
		MagicLinkURL:   "https://barbers.app/q/status?t=" + magicLinkToken,
		WhatsAppSent:   whatsAppSent,
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal idempotency response: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET response_status = 200,
		    response_body = $1
		WHERE tenant_id = $2 AND key = $3 AND endpoint = 'queue.join'`,
		respBytes, params.TenantID, params.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("update idempotency key response: %w", err)
	}

	return &JoinQueueResult{
		QueueEntryID:       entryID,
		VisitID:            visitID,
		TokenNumber:        tokenNumber,
		NewQueueVersion:    newQueueVersion,
		PresenceState:      presenceState,
		MagicLinkToken:     magicLinkToken,
		MagicLinkExpiresAt: expiresAtVal,
		WhatsAppSent:       whatsAppSent,
		IsIdempotentReplay: false,
	}, nil
}

type CallNextParams struct {
	TenantID      uuid.UUID
	LocationID    uuid.UUID
	StaffMemberID uuid.UUID
}

type CallNextOutput struct {
	Entry        *repository.QueueEntryStaffRow
	QueueVersion int
}

type ErrNoDispatchable struct {
	WaitingRemoteCount int
}

func (e ErrNoDispatchable) Error() string { return "no dispatchable customers" }

var ErrSessionNotFound = errors.New("no active queue session for today")
var ErrLockTimeout = errors.New("lock timeout")

func CallNext(ctx context.Context, pool *pgxpool.Pool, params CallNextParams) (CallNextOutput, error) {
	// 1. Call repo.GetLocationRoutingMode
	routingMode, timezone, err := repository.GetLocationRoutingMode(ctx, pool, params.LocationID)
	if err != nil {
		return CallNextOutput{}, ErrNoDispatchable{0}
	}

	// 2. Compute businessDate using the location timezone
	tz, err := time.LoadLocation(timezone)
	if err != nil {
		tz = time.UTC
	}
	businessDate := time.Now().In(tz).Truncate(24 * time.Hour).Format("2006-01-02")

	// 3. Run repo.CallNextTx inside a transaction
	var entryID uuid.UUID
	var newQueueVersion int
	repoParams := repository.CallNextParams{
		TenantID:      params.TenantID,
		LocationID:    params.LocationID,
		StaffMemberID: params.StaffMemberID,
	}

	errTx := repository.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		_, entryID, _, newQueueVersion, txErr = repository.CallNextTx(ctx, tx, repoParams, routingMode, businessDate)
		return txErr
	})

	if errTx != nil {
		var repoErr repository.ErrRepoNoDispatchable
		if errors.As(errTx, &repoErr) {
			return CallNextOutput{}, ErrNoDispatchable{WaitingRemoteCount: repoErr.WaitingRemoteCount}
		}
		if errors.Is(errTx, repository.ErrRepoSessionNotFound) {
			return CallNextOutput{}, ErrSessionNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(errTx, &pgErr) && pgErr.Code == "55P03" {
			return CallNextOutput{}, ErrLockTimeout
		}
		return CallNextOutput{}, errTx
	}

	// 12. Call repo.GetEntryStaffView
	row, err := repository.GetEntryStaffView(ctx, pool, entryID)
	if err != nil {
		return CallNextOutput{}, fmt.Errorf("get entry staff view: %w", err)
	}

	// 13. Return CallNextOutput
	return CallNextOutput{
		Entry:        row,
		QueueVersion: newQueueVersion,
	}, nil
}

