package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"barberbase-core/internal/bhejna"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestReceiveBhejnaWebhookModeB_HMACMismatch(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupAdminTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Seed a location with Mode B credentials
	secretPlain := "my-bhejna-webhook-secret-key-12345"
	encryptedSecret, err := bhejna.AESGCMEncrypt(secretPlain, s.Config.AESEncryptionKey)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE locations SET
			whatsapp_mode = 'own_number',
			bhejna_webhook_secret_encrypted = $1
		WHERE id = $2 AND tenant_id = $3
	`, encryptedSecret, locationID, tenantID)
	require.NoError(t, err)

	// 2. Request body
	body := map[string]interface{}{
		"bhejna_event_id": "event-123",
		"event_type":     "message.received",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/webhooks/bhejna/loc/%s", locationID), bytes.NewReader(jsonBody))
	req.Header.Set("X-Bhejna-Signature", "sha256=invalidhmachere")
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestReceiveBhejnaWebhookModeB_SharedModeLocation(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupAdminTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	// Ensure location is in shared mode (default)
	_, err := pool.Exec(ctx, `
		UPDATE locations SET
			whatsapp_mode = 'shared',
			bhejna_webhook_secret_encrypted = NULL
		WHERE id = $1 AND tenant_id = $2
	`, locationID, tenantID)
	require.NoError(t, err)

	body := map[string]interface{}{
		"bhejna_event_id": "event-123",
		"event_type":     "message.received",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/webhooks/bhejna/loc/%s", locationID), bytes.NewReader(jsonBody))
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReceiveBhejnaWebhookModeB_Valid(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupAdminTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Seed Mode B credentials
	secretPlain := "my-bhejna-webhook-secret-key-12345"
	encryptedSecret, err := bhejna.AESGCMEncrypt(secretPlain, s.Config.AESEncryptionKey)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE locations SET
			whatsapp_mode = 'own_number',
			bhejna_webhook_secret_encrypted = $1
		WHERE id = $2 AND tenant_id = $3
	`, encryptedSecret, locationID, tenantID)
	require.NoError(t, err)

	// 2. Request body with unique event ID
	eventID := uuid.New().String()
	body := map[string]interface{}{
		"bhejna_event_id": eventID,
		"event_type":     "message.received",
		// Add some decoy tenant_id to make sure it's ignored (Law 11)
		"tenant_id":      uuid.New().String(),
	}
	jsonBody, _ := json.Marshal(body)

	// Calculate correct HMAC
	mac := hmac.New(sha256.New, []byte(secretPlain))
	mac.Write(jsonBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/webhooks/bhejna/loc/%s", locationID), bytes.NewReader(jsonBody))
	req.Header.Set("X-Bhejna-Signature", expectedSig)
	rec := httptest.NewRecorder()

	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)
	require.Equal(t, http.StatusOK, rec.Code)

	// 3. Verify webhook_events row
	var dbTenantID, dbLocationID uuid.UUID
	var dbPayload []byte
	var dbStatus string
	err = pool.QueryRow(ctx, `
		SELECT tenant_id, location_id, payload, status
		FROM webhook_events WHERE external_event_id = $1
	`, eventID).Scan(&dbTenantID, &dbLocationID, &dbPayload, &dbStatus)
	require.NoError(t, err)

	require.Equal(t, tenantID, dbTenantID) // tenant_id resolved from locations row (Law 11)
	require.Equal(t, locationID, dbLocationID)
	require.Equal(t, "pending", dbStatus)

	var payloadMap map[string]interface{}
	err = json.Unmarshal(dbPayload, &payloadMap)
	require.NoError(t, err)
	require.Equal(t, eventID, payloadMap["bhejna_event_id"])
}

func TestReceiveBhejnaWebhookModeB_InsertFailureFallback(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupAdminTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Seed Mode B credentials
	secretPlain := "my-bhejna-webhook-secret-key-12345"
	encryptedSecret, err := bhejna.AESGCMEncrypt(secretPlain, s.Config.AESEncryptionKey)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE locations SET
			whatsapp_mode = 'own_number',
			bhejna_webhook_secret_encrypted = $1
		WHERE id = $2 AND tenant_id = $3
	`, encryptedSecret, locationID, tenantID)
	require.NoError(t, err)

	// 2. Request body with empty event ID (violates DB non-null/constraint or causes processing issue)
	// Let's make sure it fails database constraint by passing empty string or similar if DB forces it.
	// Wait, webhook_events.external_event_id is NOT NULL, so passing empty/null will fail database insert.
	body := map[string]interface{}{
		"bhejna_event_id": "", // empty event ID triggers insert failure
		"event_type":     "message.received",
	}
	jsonBody, _ := json.Marshal(body)

	mac := hmac.New(sha256.New, []byte(secretPlain))
	mac.Write(jsonBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/webhooks/bhejna/loc/%s", locationID), bytes.NewReader(jsonBody))
	req.Header.Set("X-Bhejna-Signature", expectedSig)
	rec := httptest.NewRecorder()

	// Should still return 200 to prevent retry storm (Law 9)
	s.ReceiveBhejnaWebhookModeB(rec, req, locationID)
	require.Equal(t, http.StatusOK, rec.Code)
}
