package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
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

	// Cloudflare R2 — MEDIA bucket. Deliberately separate from the R2_* backup
	// bucket credentials: a token that can write customer media must not also be
	// able to read or delete database dumps.
	//
	// All optional, on the same reasoning as VAPID above (Law 21): media is a
	// convenience layer over the queue. A deployment with no R2 credentials must
	// boot and run every queue workflow, with presign returning 503 instead.
	R2AccountID            string
	R2MediaBucket          string
	R2MediaAccessKeyID     string
	R2MediaSecretAccessKey string `json:"-"` // NEVER log, NEVER return in any response
	R2MediaPublicBaseURL   string

	// Media caps. Env-overridable; the defaults are the operating values.
	MediaMaxBytes      int // MEDIA_MAX_BYTES — headroom over the ~120KB the client resizes to
	MediaMaxPerVariant int // MEDIA_MAX_PER_VARIANT
	MediaReapBatch     int // MEDIA_REAP_BATCH — bounds one reaper tick on a 1GB droplet
}

// envInt reads a positive integer env var, falling back to def. A malformed or
// non-positive value takes the default rather than failing the boot: these are
// operational tuning knobs, not credentials.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		log.Printf("[Config] %s=%q is not a positive integer — using %d", name, v, def)
	}
	return def
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

	// Law 21: push is a convenience layer — the server must boot and run every
	// core workflow with zero push infrastructure. VAPID keys are optional; when
	// absent, push sends fail gracefully in the outbox dispatch handler (logged
	// as failed notification_events) and never touch queue correctness.
	vapidPublicKey := os.Getenv("VAPID_PUBLIC_KEY")
	vapidPrivateKey := os.Getenv("VAPID_PRIVATE_KEY")
	vapidSubject := os.Getenv("VAPID_SUBJECT")
	if vapidPublicKey == "" || vapidPrivateKey == "" || vapidSubject == "" {
		log.Printf("[Config] VAPID keys not fully configured — web push disabled (Law 21: queue correctness unaffected)")
	}

	// Media storage. Absent credentials disable media, never the server.
	r2AccountID := os.Getenv("R2_ACCOUNT_ID")
	r2MediaBucket := os.Getenv("R2_MEDIA_BUCKET_NAME")
	r2MediaKeyID := os.Getenv("R2_MEDIA_ACCESS_KEY_ID")
	r2MediaSecret := os.Getenv("R2_MEDIA_SECRET_ACCESS_KEY")
	if r2AccountID == "" || r2MediaBucket == "" || r2MediaKeyID == "" || r2MediaSecret == "" {
		log.Printf("[Config] R2 media credentials not fully configured — image upload disabled " +
			"(queue correctness unaffected)")
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

		R2AccountID:            r2AccountID,
		R2MediaBucket:          r2MediaBucket,
		R2MediaAccessKeyID:     r2MediaKeyID,
		R2MediaSecretAccessKey: r2MediaSecret,
		R2MediaPublicBaseURL:   os.Getenv("R2_MEDIA_PUBLIC_BASE_URL"),

		MediaMaxBytes:      envInt("MEDIA_MAX_BYTES", 300*1024),
		MediaMaxPerVariant: envInt("MEDIA_MAX_PER_VARIANT", 6),
		MediaReapBatch:     envInt("MEDIA_REAP_BATCH", 100),
	}, nil
}
