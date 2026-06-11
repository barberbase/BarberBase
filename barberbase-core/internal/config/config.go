package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL      string
	JWTSecret        string
	HMACSecret       string
	AESEncryptionKey []byte
	Environment      string
	Port             string

	// Platform Bhejna Mode A Credentials
	BhejnaAPIKey    string
	BhejnaFromPhone string

	PlatformAdminKey string

	VAPIDPublicKey  string // VAPID_PUBLIC_KEY env — base64url EC P-256 public key
	VAPIDPrivateKey string `json:"-"` // VAPID_PRIVATE_KEY env — base64url EC P-256 private key; NEVER log, NEVER return in any response
	VAPIDSubject    string // VAPID_SUBJECT env — contact URI e.g. mailto:ops@barberbase.in
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET environment variable is required")
	}

	hmacSecret := os.Getenv("HMAC_SECRET")
	if hmacSecret == "" {
		return nil, fmt.Errorf("HMAC_SECRET environment variable is required")
	}

	aesKeyHex := os.Getenv("AES_ENCRYPTION_KEY")
	if aesKeyHex == "" {
		return nil, fmt.Errorf("AES_ENCRYPTION_KEY environment variable is required")
	}
	// The key must be exactly 32 bytes for AES-256
	if len(aesKeyHex) != 32 {
		return nil, fmt.Errorf("AES_ENCRYPTION_KEY must be exactly 32 characters/bytes long (found %d)", len(aesKeyHex))
	}
	aesKey := []byte(aesKeyHex)

	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = "development"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Mode A Bhejna config
	bhejnaAPIKey := os.Getenv("BHEJNA_API_KEY")
	bhejnaFromPhone := os.Getenv("BHEJNA_FROM_PHONE")

	// In production, we should validate Mode A platform keys are set, but for testing/Phase 0 dev,
	// we allow them to be empty if not yet configured, returning validation errors only when actually needed.
	// We'll require them if ENVIRONMENT == "production" to be safe.
	if env == "production" {
		if bhejnaAPIKey == "" {
			return nil, fmt.Errorf("BHEJNA_API_KEY is required in production")
		}
		if bhejnaFromPhone == "" {
			return nil, fmt.Errorf("BHEJNA_FROM_PHONE is required in production")
		}
	}

	platformAdminKey := os.Getenv("PLATFORM_ADMIN_KEY")

	vapidPublicKey := os.Getenv("VAPID_PUBLIC_KEY")
	if vapidPublicKey == "" {
		return nil, fmt.Errorf("VAPID_PUBLIC_KEY required")
	}

	vapidPrivateKey := os.Getenv("VAPID_PRIVATE_KEY")
	if vapidPrivateKey == "" {
		return nil, fmt.Errorf("VAPID_PRIVATE_KEY required")
	}

	vapidSubject := os.Getenv("VAPID_SUBJECT")
	if vapidSubject == "" {
		return nil, fmt.Errorf("VAPID_SUBJECT required")
	}

	return &Config{
		DatabaseURL:      dbURL,
		JWTSecret:        jwtSecret,
		HMACSecret:       hmacSecret,
		AESEncryptionKey: aesKey,
		Environment:      env,
		Port:             port,
		BhejnaAPIKey:     bhejnaAPIKey,
		BhejnaFromPhone:  bhejnaFromPhone,
		PlatformAdminKey: platformAdminKey,
		VAPIDPublicKey:   vapidPublicKey,
		VAPIDPrivateKey:  vapidPrivateKey,
		VAPIDSubject:     vapidSubject,
	}, nil
}
