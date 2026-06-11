package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"

	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
)

const (
	BhejnaSignatureHeader = "X-Bhejna-Signature"
	BhejnaSignaturePrefix = "sha256="
)

// ValidateSignature validates the signature of the incoming request body using the secret.
// Returns true if valid, false otherwise.
func ValidateSignature(body []byte, secret string, signatureHeader string) bool {
	// Law 16: Verify header name and format in live Bhejna portal before ship.
	if len(signatureHeader) <= len(BhejnaSignaturePrefix) || signatureHeader[:len(BhejnaSignaturePrefix)] != BhejnaSignaturePrefix {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedMAC := mac.Sum(nil)
	expectedHex := hex.EncodeToString(expectedMAC)

	// Constant-time comparison
	expectedHeader := BhejnaSignaturePrefix + expectedHex
	return hmac.Equal([]byte(expectedHeader), []byte(signatureHeader))
}

// ReceiveBhejnaWebhook handles POST /webhooks/bhejna (Mode A - shared platform number)
func (s *Server) ReceiveBhejnaWebhook(w http.ResponseWriter, r *http.Request) {
	// 1. Read full body (Mandatory buffering)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "BAD_REQUEST",
			"message": "failed to read request body",
		})
		return
	}

	// 2. Validate HMAC signature
	signature := r.Header.Get(BhejnaSignatureHeader)
	secret := os.Getenv("BHEJNA_WEBHOOK_SECRET")

	if !ValidateSignature(bodyBytes, secret, signature) {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid signature",
		})
		return
	}

	// From this point onward, return 200 OK regardless of processing outcomes (Law 9)
	w.WriteHeader(http.StatusOK)

	// 3. Extract event details
	var payload struct {
		EventID   string `json:"bhejna_event_id"`
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		// Log and swallow the error, returning 200
		log.Printf("[Warning] Failed to unmarshal Mode A webhook payload: %v", err)
		return
	}

	if payload.EventID == "" {
		log.Printf("[Warning] Mode A webhook missing event ID")
		return
	}

	// 4. Ingest webhook event asynchronously (insert into DB)
	ctx := r.Context()
	err = repository.InsertWebhookEvent(ctx, s.Pool, payload.EventID, payload.EventType, nil, nil, bodyBytes)
	if err != nil {
		log.Printf("[Error] Failed to insert Mode A webhook event: %v", err)
	}
}

// ReceiveBhejnaWebhookModeB handles POST /webhooks/bhejna/loc/{location_id} (Mode B - shop's own number)
func (s *Server) ReceiveBhejnaWebhookModeB(w http.ResponseWriter, r *http.Request, locationId UUIDv7) {
	// 1. Read raw body (Mandatory buffering)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 2. Extract locationID from path param (UUID). Invalid UUID -> 404.
	locationID := uuid.UUID(locationId)
	if locationID == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ctx := r.Context()

	// 3. Fetch Mode B config
	cfg, err := repository.GetLocationForModeBWebhook(ctx, s.Pool, locationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 4. Decrypt webhook secret
	webhookSecret, err := bhejna.AESGCMDecrypt(cfg.BhejnaWebhookSecretEncrypted, s.Config.AESEncryptionKey)
	if err != nil {
		log.Printf("[Error] Webhook secret decryption failed for location %s: %v", locationID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 5. HMAC-SHA256 verification
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(rawBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	actualSig := r.Header.Get("X-Bhejna-Signature")
	if !hmac.Equal([]byte(actualSig), []byte(expectedSig)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// From this point onward, return 200 OK regardless of processing outcomes (Law 9)
	w.WriteHeader(http.StatusOK)

	// 6. Parse bhejna_event_id and event_type from rawBody JSON
	var payload struct {
		BhejnaEventID string `json:"bhejna_event_id"`
		EventType     string `json:"event_type"`
	}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		log.Printf("[Warning] Failed to unmarshal Mode B webhook payload: %v", err)
		return
	}

	// 7. INSERT webhook_events
	_, insertErr := s.Pool.Exec(ctx, `
		INSERT INTO webhook_events
			(source, external_event_id, event_type, tenant_id, location_id, payload, status)
		VALUES
			('bhejna', $1, $2, $3, $4, $5, 'pending')
		ON CONFLICT (source, external_event_id) DO NOTHING
	`, payload.BhejnaEventID, payload.EventType, cfg.TenantID, locationID, rawBody)
	if insertErr != nil {
		log.Printf("[Error] Failed to insert Mode B webhook event: %v", insertErr)
	}
}
