package bhejna

import (
	"bytes"
	"context"
	"crypto/aes"
	"log"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LocationWhatsAppConfig struct {
	WhatsAppMode           string // "shared" | "own_number"
	BusinessWhatsAppNumber string // E.164; populated for Mode B only
	BhejnaAPIKeyEncrypted  []byte // AES-256-GCM ciphertext; populated for Mode B only
}

type SendTextReq struct {
	To             string // E.164, already normalized by caller
	Body           string
	IdempotencyKey string // MUST be "barberbase:otp:{staff_otp_id}"
	SenderClass    SenderClass
}

type SendTemplateReq struct {
	To             string
	TemplateCode   string
	Language       string // "en" for all Phase 1 templates
	Components     []TemplateComponent
	IdempotencyKey string // MUST be "barberbase:outbox:{outbox_event_id}"
}

type TemplateComponent struct {
	Type       string              `json:"type"`
	SubType    string              `json:"sub_type,omitempty"`
	Index      int                 `json:"index"`
	Parameters []TemplateParameter `json:"parameters"`
}

type TemplateParameter struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload,omitempty"` // quick_reply buttons only
}

type SendResult struct {
	JobID string // store in notification_events.provider_message_id
}

type BhejnaError struct {
	Code       string
	Retriable  bool
	RequestID  string
	HTTPStatus int
}

func (e BhejnaError) Error() string {
	return fmt.Sprintf("Bhejna API error: Code=%s, Retriable=%t, RequestID=%s, HTTPStatus=%d",
		e.Code, e.Retriable, e.RequestID, e.HTTPStatus)
}

type Client interface {
	SendText(ctx context.Context, tenantID, locationID uuid.UUID, req SendTextReq) (*SendResult, error)
	SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req SendTemplateReq) (*SendResult, error)
}

type bhejnaClient struct {
	pool            *pgxpool.Pool
	aesKey          []byte
	modeAKey        string
	modeAPhone      string
	httpClient      *http.Client
	bhejnaAPIURL    string
}

func NewClient(pool *pgxpool.Pool, aesKey []byte, modeAKey, modeAPhone string) Client {
	apiURL := "https://bhejna-api.codenxtlab.tech"
	return &bhejnaClient{
		pool:         pool,
		aesKey:       aesKey,
		modeAKey:     modeAKey,
		modeAPhone:   modeAPhone,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		bhejnaAPIURL: apiURL,
	}
}

// resolveCredentials queries the database for location-specific credentials and decrypts them if necessary
func (c *bhejnaClient) resolveCredentials(ctx context.Context, tenantID, locationID uuid.UUID, class SenderClass) (apiKey string, fromPhone string, err error) {
	if class == SenderPlatform {
		return c.modeAKey, c.modeAPhone, nil
	}

	var whatsappMode string
	var businessWhatsAppNumber sql.NullString
	var apiKeyEncrypted sql.NullString

	query := `
		SELECT whatsapp_mode,
		       COALESCE(business_whatsapp_number, ''),
		       bhejna_api_key_encrypted
		FROM locations
		WHERE id = $1 AND tenant_id = $2;
	`
	err = c.pool.QueryRow(ctx, query, locationID, tenantID).Scan(&whatsappMode, &businessWhatsAppNumber, &apiKeyEncrypted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", BhejnaError{Retriable: false, Code: "location_not_found"}
		}
		// Any other DB error is retriable
		return "", "", BhejnaError{Retriable: true, Code: "database_error"}
	}

	if whatsappMode == "own_number" {
		if !apiKeyEncrypted.Valid || apiKeyEncrypted.String == "" {
			return "", "", BhejnaError{Retriable: false, Code: "credential_decrypt_failed"}
		}
		decryptedKey, decryptErr := DecryptAESGCM(apiKeyEncrypted.String, c.aesKey)
		if decryptErr != nil {
			return "", "", BhejnaError{Retriable: false, Code: "credential_decrypt_failed"}
		}
		apiKey = decryptedKey
		fromPhone = businessWhatsAppNumber.String
	} else {
		apiKey = c.modeAKey
		fromPhone = c.modeAPhone
	}

	return apiKey, fromPhone, nil
}

// sendHTTPRequest executes the request to the Bhejna API and handles the error taxonomy
func (c *bhejnaClient) sendHTTPRequest(ctx context.Context, apiKey string, payload map[string]interface{}) (*SendResult, error) {
	url := fmt.Sprintf("%s/v1/messages", c.bhejnaAPIURL)

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, BhejnaError{Retriable: false, Code: "payload_marshal_failed"}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, BhejnaError{Retriable: false, Code: "request_creation_failed"}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Differentiate network timeout/errors as retriable
		var netErr net.Error
		if errors.As(err, &netErr) {
			return nil, BhejnaError{Retriable: true, Code: "network_error"}
		}
		return nil, BhejnaError{Retriable: true, Code: "network_error"}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, BhejnaError{Retriable: true, Code: "network_error"}
	}

	if resp.StatusCode == http.StatusAccepted { // 202
		var successResp struct {
			JobID  string `json:"job_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(respBody, &successResp); err != nil {
			return nil, BhejnaError{Retriable: false, Code: "unparseable_success_response", HTTPStatus: resp.StatusCode}
		}
		if successResp.JobID == "" {
			return nil, BhejnaError{Retriable: false, Code: "unexpected_status", HTTPStatus: resp.StatusCode}
		}
		return &SendResult{JobID: successResp.JobID}, nil
	}

	if resp.StatusCode >= 500 {
		return nil, BhejnaError{Retriable: true, HTTPStatus: resp.StatusCode}
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		log.Printf("Bhejna 4xx response body: %s", string(respBody))
		var errorResp struct {
			Success bool `json:"success"`
			Error   struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(respBody, &errorResp); err != nil {
			return nil, BhejnaError{Retriable: false, Code: "unparseable_error", HTTPStatus: resp.StatusCode}
		}
		return nil, BhejnaError{
			Retriable:  errorResp.Error.Retryable,
			Code:       errorResp.Error.Code,
			RequestID:  errorResp.RequestID,
			HTTPStatus: resp.StatusCode,
		}
	}

	// Any other 2xx status (except 202) is treated as unexpected status error
	return nil, BhejnaError{Retriable: false, Code: "unexpected_status", HTTPStatus: resp.StatusCode}
}

func (c *bhejnaClient) SendText(ctx context.Context, tenantID, locationID uuid.UUID, req SendTextReq) (*SendResult, error) {
	class := req.SenderClass
	if class == "" {
		class = SenderCustomer
	}
	apiKey, fromPhone, err := c.resolveCredentials(ctx, tenantID, locationID, class)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"to":                  req.To,
		"from_business_phone": fromPhone,
		"idempotency_key":     req.IdempotencyKey,
		"type":                "text",
		"text": map[string]interface{}{
			"body": req.Body,
		},
	}

	return c.sendHTTPRequest(ctx, apiKey, payload)
}

func (c *bhejnaClient) SendTextPlatform(ctx context.Context, req SendTextReq) (*SendResult, error) {
	req.SenderClass = SenderPlatform
	return c.SendText(ctx, uuid.Nil, uuid.Nil, req)
}


func (c *bhejnaClient) SendTemplate(ctx context.Context, tenantID, locationID uuid.UUID, req SendTemplateReq) (*SendResult, error) {
	class := ClassFor(req.TemplateCode)
	apiKey, fromPhone, err := c.resolveCredentials(ctx, tenantID, locationID, class)
	if err != nil {
		return nil, err
	}

	componentsPayload := make([]map[string]interface{}, len(req.Components))
	for i, comp := range req.Components {
		paramsPayload := make([]map[string]interface{}, len(comp.Parameters))
		for j, param := range comp.Parameters {
			p := map[string]interface{}{"type": param.Type}
			if param.Type == "payload" {
				p["payload"] = param.Payload
			} else {
				p["text"] = param.Text
			}
			paramsPayload[j] = p
		}

		compMap := map[string]interface{}{
			"type":       comp.Type,
			"parameters": paramsPayload,
		}
		if comp.SubType != "" {
			compMap["sub_type"] = comp.SubType
		}
		if comp.Index != 0 || comp.Type == "button" {
			compMap["index"] = comp.Index
		}
		componentsPayload[i] = compMap
	}

	payload := map[string]interface{}{
		"to":                  req.To,
		"from_business_phone": fromPhone,
		"idempotency_key":     req.IdempotencyKey,
		"type":                "template",
		"template": map[string]interface{}{
			"template_code": req.TemplateCode,
			"language":      req.Language,
			"components":    componentsPayload,
		},
	}

	return c.sendHTTPRequest(ctx, apiKey, payload)
}

// AES-256-GCM Helper Decryption Function
func DecryptAESGCM(ciphertextStr string, key []byte) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextStr)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertextBytes := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// EncryptAESGCM is helper function for tests and encryption connect onboarding
func EncryptAESGCM(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// AESGCMEncrypt encrypts a plaintext string with a 32-byte key using AES-256-GCM
func AESGCMEncrypt(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// AESGCMDecrypt decrypts a ciphertext string with a 32-byte key using AES-256-GCM
func AESGCMDecrypt(ciphertext string, key []byte) (string, error) {
	return DecryptAESGCM(ciphertext, key)
}

