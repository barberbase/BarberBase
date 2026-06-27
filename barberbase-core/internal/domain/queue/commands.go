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

// CheckoutProductItem is a product sold at checkout.
type CheckoutProductItem struct {
	ProductID uuid.UUID
	Quantity  int
}

// CheckoutPaymentLine is one leg of a split payment.
type CheckoutPaymentLine struct {
	Method              string  // "cash"|"upi"|"card"|"unpaid"|"complimentary"
	AmountPaise         int
	ProviderReferenceID *string // UPI transaction ID; nil for cash/card
}

// CheckoutParams is the validated input to CompleteVisitAndCheckout.
type CheckoutParams struct {
	EntryID             uuid.UUID
	TenantID            uuid.UUID // from JWT context
	LocationID          uuid.UUID // from JWT context
	CallerStaffID       uuid.UUID // from JWT context; used as finalized_by, collected_by
	BusinessDate        time.Time // today in Asia/Kolkata; computed at handler layer
	DiscountAmountPaise int
	DiscountReason      *string
	Products            []CheckoutProductItem
	Payments            []CheckoutPaymentLine
}

// CheckoutResult is returned on success.
type CheckoutResult struct {
	VisitID           uuid.UUID
	SubtotalPaise     int
	DiscountPaise     int
	TotalPaise        int
	PaymentStatus     string // "paid"|"unpaid"|"complimentary"
	FeedbackScheduled bool
	NewQueueVersion   int
}

var (
	ErrInvalidTransition = errors.New("entry not in in_progress state")
	ErrPaymentMismatch   = errors.New("payment sum does not equal total")
	ErrInvalidDiscount   = errors.New("discount exceeds subtotal or is negative")
	ErrProductNotFound   = errors.New("one or more products not found or inactive")
)

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

// GenerateMagicLinkToken rebuilds the HMAC token for a known customer entry.
// Use when re-notifying a customer who already joined (e.g. bb_queue_delayed).
func GenerateMagicLinkToken(customerID, locationID, visitID string, expiresAt time.Time, secret []byte) string {
	return generateMagicLinkToken(customerID, locationID, visitID, expiresAt, secret)
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
	if params.InitiatedVia == "staff_dashboard" {
		if params.PhoneNumber == nil || *params.PhoneNumber == "" {
			presenceState = "unknown"
		} else {
			presenceState = "arrived"
		}
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

	// Law 6: staff walk-in with phone = arrived; audit arrival
	if presenceState == "arrived" {
		_, err = tx.Exec(ctx, `
			INSERT INTO arrival_attempts (tenant_id, location_id, queue_entry_id, method, success)
			VALUES ($1, $2, $3, 'staff', true)`,
			params.TenantID, params.LocationID, entryID)
		if err != nil {
			return nil, fmt.Errorf("insert arrival attempt: %w", err)
		}
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

// ErrEntryNotFound is returned when a queue entry is not found.
var ErrEntryNotFound = errors.New("queue entry not found")

// StartServiceResult is the minimal data returned from the StartService command.
// The handler uses a separate repository read to build the full QueueEntryStaff response.
type StartServiceResult struct {
	EntryID          uuid.UUID
	SessionID        uuid.UUID
	QueueVersion     int
	State            string // will be "in_progress"
	CalledAt         *time.Time
	StartedAt        *time.Time
	AssignedBarberID *uuid.UUID
}

// StartService transitions a queue entry to in_progress.
// Accepts a pgx.Tx — caller is responsible for BEGIN/COMMIT/ROLLBACK via WithTx.
// barberID is the calling staff member's ID (from JWT).
// tenantID is from JWT context (Law 11 — never request body).
func StartService(ctx context.Context, tx pgx.Tx, entryID, barberID, tenantID uuid.UUID) (*StartServiceResult, error) {
	// Step 1 — Lock queue_session FOR UPDATE (Law 1, mandatory first):
	var sessionID uuid.UUID
	var queueVersion int
	err := tx.QueryRow(ctx, `
		SELECT qs.id, qs.queue_version
		FROM queue_sessions qs
		JOIN queue_entries qe ON qe.queue_session_id = qs.id
		WHERE qe.id = $1
		  AND qs.tenant_id = $2
		FOR UPDATE OF qs`, entryID, tenantID).Scan(&sessionID, &queueVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}

	// Step 2 — Lock queue_entry FOR UPDATE:
	var entry struct {
		ID               uuid.UUID
		State            string
		PresenceState    string
		AssignedBarberID *uuid.UUID
	}
	err = tx.QueryRow(ctx, `
		SELECT id, state, presence_state, assigned_barber_id
		FROM queue_entries
		WHERE id = $1
		FOR UPDATE`, entryID).Scan(&entry.ID, &entry.State, &entry.PresenceState, &entry.AssignedBarberID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}

	// Step 3 — Validate transition (call ValidateStart):
	directStart, err := ValidateStart(entry.State, entry.PresenceState)
	if err != nil {
		return nil, err // Return err unchanged — handler maps ErrDirectStartNotArrived and ErrInvalidStateTransition → 422
	}

	// Step 4 — Apply mutation:
	var res StartServiceResult
	res.SessionID = sessionID

	if !directStart {
		// Normal path (directStart=false, state was called):
		err = tx.QueryRow(ctx, `
			UPDATE queue_entries
			SET state         = 'in_progress',
			    started_at    = NOW(),
			    stale_warning = NULL
			WHERE id = $1
			RETURNING id, state, started_at, called_at, assigned_barber_id`,
			entryID).Scan(&res.EntryID, &res.State, &res.StartedAt, &res.CalledAt, &res.AssignedBarberID)
	} else {
		// Direct start path (directStart=true, state was waiting):
		err = tx.QueryRow(ctx, `
			UPDATE queue_entries
			SET state              = 'in_progress',
			    called_at          = NOW(),
			    started_at         = NOW(),
			    assigned_barber_id = $2,
			    stale_warning      = NULL
			WHERE id = $1
			RETURNING id, state, called_at, started_at, assigned_barber_id`,
			entryID, barberID).Scan(&res.EntryID, &res.State, &res.CalledAt, &res.StartedAt, &res.AssignedBarberID)
	}
	if err != nil {
		return nil, err
	}

	// Step 5 — Update staff status:
	_, err = tx.Exec(ctx, `
		UPDATE staff_members
		SET status = 'cutting'
		WHERE id = $1
		  AND tenant_id = $2
		  AND is_active = true`, barberID, tenantID)
	if err != nil {
		return nil, err
	}

	// Step 6 — Increment queue_version:
	err = tx.QueryRow(ctx, `
		UPDATE queue_sessions
		SET queue_version = queue_version + 1
		WHERE id = $1
		RETURNING queue_version`, sessionID).Scan(&res.QueueVersion)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func CompleteVisitAndCheckout(ctx context.Context, pool *pgxpool.Pool, params CheckoutParams) (CheckoutResult, error) {
	var res CheckoutResult
	err := repository.WithTx(ctx, pool, func(tx pgx.Tx) error {
		// Step 1: Lock queue_session FOR UPDATE
		sessionID, _, err := repository.LockSessionForCheckout(ctx, tx, params.LocationID, params.BusinessDate.Format("2006-01-02"))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSessionNotFound
			}
			return fmt.Errorf("lock session: %w", err)
		}

		// Step 2: Lock queue_entry FOR UPDATE; validate state
		entry, err := repository.LockEntryForCheckout(ctx, tx, params.EntryID, params.TenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrEntryNotFound
			}
			return fmt.Errorf("lock entry: %w", err)
		}
		if entry.State != "in_progress" {
			return ErrInvalidTransition
		}

		// Step 3: Lock visit FOR UPDATE
		err = repository.LockVisitForCheckout(ctx, tx, entry.VisitID, params.TenantID)
		if err != nil {
			return fmt.Errorf("lock visit: %w", err)
		}

		// Step 4: Fetch visit_services
		services, err := repository.GetVisitServicesForCheckout(ctx, tx, entry.VisitID)
		if err != nil {
			return fmt.Errorf("get visit services: %w", err)
		}

		// Step 5: Fetch product data
		productIDs := make([]uuid.UUID, 0, len(params.Products))
		for _, p := range params.Products {
			productIDs = append(productIDs, p.ProductID)
		}
		productMap, err := repository.GetProductsForCheckout(ctx, tx, productIDs, params.TenantID)
		if err != nil {
			return fmt.Errorf("get products: %w", err)
		}
		for _, reqProd := range params.Products {
			if _, ok := productMap[reqProd.ProductID]; !ok {
				return ErrProductNotFound
			}
		}

		// Step 6: Compute totals
		var servicesTotalPaise int
		for _, s := range services {
			servicesTotalPaise += s.UnitAmountPaise
		}
		var productsTotalPaise int
		for _, p := range params.Products {
			productsTotalPaise += productMap[p.ProductID].PricePaise * p.Quantity
		}
		subtotalPaise := servicesTotalPaise + productsTotalPaise
		totalPaise := subtotalPaise - params.DiscountAmountPaise

		if params.DiscountAmountPaise < 0 || params.DiscountAmountPaise > subtotalPaise {
			return ErrInvalidDiscount
		}

		var paymentsTotal int
		var unpaidCount, complimentaryCount int
		for _, p := range params.Payments {
			paymentsTotal += p.AmountPaise
			if p.Method == "unpaid" {
				unpaidCount++
			} else if p.Method == "complimentary" {
				complimentaryCount++
			}
		}
		if paymentsTotal != totalPaise {
			return ErrPaymentMismatch
		}

		paymentStatus := "paid"
		if len(params.Payments) > 0 && complimentaryCount == len(params.Payments) {
			paymentStatus = "complimentary"
		} else if unpaidCount > 0 {
			paymentStatus = "unpaid"
		}

		res.SubtotalPaise = subtotalPaise
		res.DiscountPaise = params.DiscountAmountPaise
		res.TotalPaise = totalPaise
		res.PaymentStatus = paymentStatus
		res.VisitID = entry.VisitID

		// Step 7: INSERT visit_charges
		chargeID, err := repository.InsertVisitCharge(ctx, tx, params.TenantID, params.LocationID, entry.VisitID, subtotalPaise, params.DiscountAmountPaise, totalPaise, params.DiscountReason, params.CallerStaffID)
		if err != nil {
			return fmt.Errorf("insert charge: %w", err)
		}

		// Step 8 & 9: INSERT visit_charge_line_items
		var lineItemRows [][]any
		for _, s := range services {
			var staffID *uuid.UUID = entry.AssignedBarberID
			lineItemRows = append(lineItemRows, []any{params.TenantID, chargeID, "service", s.ServiceVariantID, nil, s.VariantNameSnapshot, 1, s.UnitAmountPaise, s.UnitAmountPaise, staffID})
		}
		for _, p := range params.Products {
			var staffID *uuid.UUID = entry.AssignedBarberID
			prod := productMap[p.ProductID]
			unitPrice := prod.PricePaise
			totalPrice := unitPrice * p.Quantity
			lineItemRows = append(lineItemRows, []any{params.TenantID, chargeID, "product", nil, p.ProductID, prod.Name, p.Quantity, unitPrice, totalPrice, staffID})
		}
		if len(lineItemRows) > 0 {
			if err := repository.InsertVisitChargeLineItems(ctx, tx, params.TenantID, chargeID, lineItemRows); err != nil {
				return fmt.Errorf("insert line items: %w", err)
			}
		}

		// Step 10: INSERT visit_payments
		var paymentRows [][]any
		for _, p := range params.Payments {
			paymentRows = append(paymentRows, []any{params.TenantID, params.LocationID, chargeID, p.Method, p.AmountPaise, p.ProviderReferenceID, params.CallerStaffID, time.Now()})
		}
		if len(paymentRows) > 0 {
			if err := repository.InsertVisitPayments(ctx, tx, params.TenantID, params.LocationID, chargeID, params.CallerStaffID, paymentRows); err != nil {
				return fmt.Errorf("insert payments: %w", err)
			}
		}

		// Step 11: UPDATE queue_entries
		if err := repository.MarkEntryCompleted(ctx, tx, params.EntryID, params.TenantID); err != nil {
			return fmt.Errorf("mark entry completed: %w", err)
		}

		// Step 12: UPDATE visits
		if err := repository.MarkVisitCompleted(ctx, tx, entry.VisitID, params.TenantID); err != nil {
			return fmt.Errorf("mark visit completed: %w", err)
		}

		// Step 13: UPDATE customers
		if entry.CustomerID != nil {
			if err := repository.UpdateCustomerMetrics(ctx, tx, *entry.CustomerID, params.TenantID, totalPaise); err != nil {
				return fmt.Errorf("update customer: %w", err)
			}
		}

		// Step 14: UPDATE staff_members
		if entry.AssignedBarberID != nil {
			if err := repository.UpdateStaffIdle(ctx, tx, *entry.AssignedBarberID, params.TenantID); err != nil {
				return fmt.Errorf("update staff idle: %w", err)
			}
		}

		// Step 12.5 — Push trigger: web_push.send (Law 7 — inside tx; Law 21 — push-agnostic correctness)
		// The dispatch handler applies the frequency gate (Law 19) at dispatch time.
		// This step only signals that a checkout occurred at a push-capable location.
		var anyPushEnabled bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM staff_members
				WHERE location_id = $1
				  AND push_enabled = true
				  AND is_active    = true
			)`, params.LocationID,
		).Scan(&anyPushEnabled); err != nil {
			return fmt.Errorf("step 12.5 push check: %w", err)
		}
		if anyPushEnabled {
			pushPayload, err := json.Marshal(struct {
				LocationID string `json:"location_id"`
				TenantID   string `json:"tenant_id"`
			}{
				LocationID: params.LocationID.String(),
				TenantID:   params.TenantID.String(),
			})
			if err != nil {
				return fmt.Errorf("step 12.5 payload marshal: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO outbox_events (tenant_id, type, payload, process_after)
				 VALUES ($1, 'web_push.send', $2, NOW())`,
				params.TenantID, pushPayload,
			); err != nil {
				return fmt.Errorf("step 12.5 push outbox insert: %w", err)
			}
		}

		// Step 15: INSERT outbox_events
		if entry.CustomerID != nil {
			var assignedBarber interface{}
			if entry.AssignedBarberID != nil {
				assignedBarber = entry.AssignedBarberID.String()
			}
			payloadMap := map[string]interface{}{
				"visit_id":           entry.VisitID.String(),
				"customer_id":        entry.CustomerID.String(),
				"location_id":        params.LocationID.String(),
				"tenant_id":          params.TenantID.String(),
				"assigned_barber_id": assignedBarber,
			}
			payloadBytes, err := json.Marshal(payloadMap)
			if err == nil {
				if err := repository.InsertFeedbackOutboxEvent(ctx, tx, params.TenantID, payloadBytes); err != nil {
					return fmt.Errorf("insert outbox event: %w", err)
				}
				res.FeedbackScheduled = true
			}
		}

		// Step 16: Increment queue_version
		newVersion, err := repository.IncrementQueueVersion(ctx, tx, sessionID)
		if err != nil {
			return fmt.Errorf("increment queue version: %w", err)
		}
		res.NewQueueVersion = newVersion

		return nil
	})

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return res, ErrLockTimeout
		}
		return res, err
	}

	return res, nil
}
