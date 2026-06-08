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

	"github.com/jackc/pgx/v5"
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
	// 1. Read full body (Mandatory buffering)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "BAD_REQUEST",
			"message": "failed to read request body",
		})
		return
	}

	// 2. Load location by locationId path parameter (asserting deleted_at IS NULL)
	ctx := r.Context()
	cfg, err := repository.GetLocationWebhookConfig(ctx, s.Pool, locationId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": "location not found",
			})
			return
		}
		log.Printf("[Error] Database query failed when resolving Mode B config: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "INTERNAL_ERROR",
			"message": "internal server error",
		})
		return
	}

	// Validate own_number mode
	if cfg.WhatsAppMode != "own_number" {
		respondJSON(w, http.StatusNotFound, map[string]string{
			"code":    "NOT_FOUND",
			"message": "location is not in own_number mode",
		})
		return
	}

	// 3. Decrypt bhejna_webhook_secret_encrypted
	decryptedSecret, err := bhejna.DecryptAESGCM(cfg.BhejnaWebhookSecretEncrypted, s.Config.AESEncryptionKey)
	if err != nil {
		log.Printf("[Error] Failed to decrypt webhook secret for location %s: %v", locationId, err)
		respondJSON(w, http.StatusNotFound, map[string]string{
			"code":    "NOT_FOUND",
			"message": "location configuration is invalid",
		})
		return
	}

	// 4. Validate HMAC signature
	signature := r.Header.Get(BhejnaSignatureHeader)
	if !ValidateSignature(bodyBytes, decryptedSecret, signature) {
		respondJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "UNAUTHORIZED",
			"message": "invalid signature",
		})
		return
	}

	// From this point onward, return 200 OK regardless of processing outcomes (Law 9)
	w.WriteHeader(http.StatusOK)

	// 5. Extract event details
	var payload struct {
		EventID   string `json:"bhejna_event_id"`
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("[Warning] Failed to unmarshal Mode B webhook payload: %v", err)
		return
	}

	if payload.EventID == "" {
		log.Printf("[Warning] Mode B webhook missing event ID")
		return
	}

	// 6. Ingest webhook event asynchronously (insert into DB)
	err = repository.InsertWebhookEvent(ctx, s.Pool, payload.EventID, payload.EventType, &cfg.TenantID, &locationId, bodyBytes)
	if err != nil {
		log.Printf("[Error] Failed to insert Mode B webhook event: %v", err)
	}
}
