// Package media orchestrates the presign → upload → commit pipeline.
//
// THE BYTES NEVER TOUCH THE DROPLET. The server issues a presigned PUT, the
// browser uploads straight to R2, and the server verifies by HEAD. On 1GB of RAM
// with GOMEMLIMIT=250MiB, decoding one 12MP phone JPEG costs ~48MB; a handful of
// concurrent uploads would be fatal. So there is no multipart parsing, no image
// decode, and no io.Copy of an object body anywhere in this package or in
// internal/r2 — a property asserted mechanically by TestNoImageBytesOnTheDroplet.
package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// Errors callers map to responses. The status code each one maps to is the
// table in internal/api/handlers_media.go, above mediaErrorStatus — which is
// also the only implementation of that mapping.
var (
	// ErrRateLimited carries its own retry delay so the handler can set
	// Retry-After without re-deriving it. O5 added that handler; the limiter is
	// still asserted here as well, because it is domain behaviour.
	ErrRateLimited = errors.New("too many presign requests")
	// ErrNotUploaded means commit was called before the object existed.
	ErrNotUploaded = errors.New("object has not been uploaded yet")
	// ErrTooLarge and ErrWrongType are commit-time verification failures.
	ErrTooLarge   = errors.New("object exceeds the maximum allowed size")
	ErrWrongType  = errors.New("object content-type must be image/webp")
	ErrBadPurpose = errors.New("unknown media purpose")
)

// RateLimitedError carries the delay until the next token.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e RateLimitedError) Error() string {
	return fmt.Sprintf("%v: retry after %s", ErrRateLimited, e.RetryAfter)
}
func (e RateLimitedError) Unwrap() error { return ErrRateLimited }

// presignLimiters follows the pattern used in four other places
// (handlers_device.go:36, handlers_push.go:23, handlers_public.go:31,
// auth/otp.go:12): a package-level sync.Map keyed by id.
var presignLimiters sync.Map

// 10 presigns per minute per staff member: one token every 6s, burst of 10.
func getPresignLimiter(staffMemberID string) *rate.Limiter {
	v, _ := presignLimiters.LoadOrStore(staffMemberID, rate.NewLimiter(rate.Every(6*time.Second), 10))
	return v.(*rate.Limiter)
}

const (
	// PurgeGrace is how long an archived object survives before the outbox
	// handler removes it from R2 — long enough to undo a mistaken delete.
	PurgeGrace = 7 * 24 * time.Hour
	// ReapAge is how long a presigned upload may stay unconfirmed.
	ReapAge = time.Hour
	// PresignTTL is the upload window handed to the browser.
	PresignTTL = 15 * time.Minute
	// RequiredContentType — the client resizes and encodes before uploading.
	RequiredContentType = "image/webp"
)

// Service is the media pipeline. It is constructed per request in the eventual
// handler, matching how repositories are used elsewhere in this codebase.
type Service struct {
	Repo          *repository.MediaRepository
	Store         *r2.Store
	MaxBytes      int
	MaxPerVariant int
}

// PresignInput is deliberately explicit about identity: TenantID and LocationID
// come from JWT claims, never from a request body (Law 11).
type PresignInput struct {
	TenantID      uuid.UUID
	LocationID    uuid.UUID
	StaffMemberID uuid.UUID
	Purpose       string
	ContentHash   string
	VariantID     *uuid.UUID
	StaffID       *uuid.UUID
}

// PresignOutput is what the browser needs to upload.
type PresignOutput struct {
	MediaAssetID uuid.UUID
	R2Key        string
	UploadURL    string
	ExpiresAt    time.Time
}

// buildKey is the key format. Content-hashed, so objects are immutable and
// cacheable forever, and re-uploading identical bytes is a no-op rather than a
// second object. No PII: only opaque UUIDs and a hash.
//
// The 16-hex prefix is 64 bits — ample against accidental collision within one
// location's keyspace, which is the only scope a key can collide in.
func buildKey(purpose, contentHash string, locationID uuid.UUID, variantID, staffID *uuid.UUID) (string, error) {
	short := contentHash[:16]
	switch purpose {
	case "service_ref":
		if variantID == nil {
			return "", fmt.Errorf("%w: service_ref requires a service_variant_id", ErrBadPurpose)
		}
		return fmt.Sprintf("svc/%s/%s/%s.webp", locationID, *variantID, short), nil
	case "location_logo":
		return fmt.Sprintf("loc/%s/logo/%s.webp", locationID, short), nil
	case "location_cover":
		return fmt.Sprintf("loc/%s/cover/%s.webp", locationID, short), nil
	case "staff_avatar":
		if staffID == nil {
			return "", fmt.Errorf("%w: staff_avatar requires a staff_member_id", ErrBadPurpose)
		}
		return fmt.Sprintf("stf/%s/%s/%s.webp", locationID, *staffID, short), nil
	default:
		return "", ErrBadPurpose
	}
}

// Presign records a pending asset and returns a URL the browser PUTs to.
//
// Presigning the same bytes twice for the same variant is idempotent: the r2_key
// is content-derived, so the insert collides and returns the existing row with a
// fresh URL. One row, not two. An abandoned attempt is collected by the reaper
// an hour later.
func (s *Service) Presign(ctx context.Context, in PresignInput, now time.Time) (PresignOutput, error) {
	if !s.Store.Configured() {
		return PresignOutput{}, r2.ErrNotConfigured
	}
	lim := getPresignLimiter(in.StaffMemberID.String())
	if !lim.Allow() {
		return PresignOutput{}, RateLimitedError{RetryAfter: 6 * time.Second}
	}
	if err := repository.ValidateContentHash(in.ContentHash); err != nil {
		return PresignOutput{}, err
	}
	key, err := buildKey(in.Purpose, in.ContentHash, in.LocationID, in.VariantID, in.StaffID)
	if err != nil {
		return PresignOutput{}, err
	}

	asset, err := s.Repo.CreatePending(ctx, in.TenantID, in.LocationID, in.Purpose, key,
		in.ContentHash, in.VariantID, in.StaffID)
	if err != nil {
		return PresignOutput{}, err
	}

	url, err := s.Store.PresignPut(key, PresignTTL, now)
	if err != nil {
		return PresignOutput{}, err
	}
	return PresignOutput{
		MediaAssetID: asset.ID,
		R2Key:        key,
		UploadURL:    url,
		ExpiresAt:    now.Add(PresignTTL),
	}, nil
}

// Commit verifies the uploaded object by HEAD and flips the row to ready.
//
// Idempotent: a second identical commit returns the asset with committed_at
// unchanged. R2 being unreachable leaves the row pending and returns a retryable
// error — no data is lost, and the reaper will collect the row if the retry
// never comes.
func (s *Service) Commit(ctx context.Context, tenantID, locationID, assetID uuid.UUID, altText *string) (repository.MediaAsset, error) {
	if !s.Store.Configured() {
		return repository.MediaAsset{}, r2.ErrNotConfigured
	}
	asset, err := s.Repo.GetForCommit(ctx, tenantID, locationID, assetID)
	if err != nil {
		return repository.MediaAsset{}, err
	}
	if asset.Status == "ready" {
		return asset, nil // already committed; do not re-HEAD, do not re-stamp
	}

	info, err := s.Store.Head(asset.R2Key)
	switch {
	case errors.Is(err, r2.ErrNotFound):
		return repository.MediaAsset{}, ErrNotUploaded
	case err != nil:
		return repository.MediaAsset{}, err // ErrUnavailable — row stays pending
	}

	if s.MaxBytes > 0 && info.ContentLength > int64(s.MaxBytes) {
		return repository.MediaAsset{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrTooLarge, info.ContentLength, s.MaxBytes)
	}
	// Content-Type may carry parameters (e.g. "image/webp; charset=binary").
	if ct := strings.TrimSpace(strings.Split(info.ContentType, ";")[0]); ct != RequiredContentType {
		return repository.MediaAsset{}, fmt.Errorf("%w: got %q", ErrWrongType, ct)
	}

	// The ETag is logged, not stored. media_assets has no column for it and 003
	// is frozen; adding one for this alone would put a fifth migration in front
	// of a prod database that only just caught up. It is a useful signal for
	// detecting an object replaced under an existing key — a candidate for the
	// next migration, not a reason to open one.
	LogCommit(asset.ID, asset.R2Key, info.ETag, info.ContentLength)

	return s.Repo.MarkReady(ctx, tenantID, locationID, assetID, int(info.ContentLength),
		s.MaxPerVariant, altText)
}

// Archive soft-deletes and schedules the R2 purge in one transaction (Law 7).
func (s *Service) Archive(ctx context.Context, tenantID, locationID, assetID uuid.UUID) error {
	return s.Repo.ArchiveWithPurge(ctx, tenantID, locationID, assetID, PurgeGrace)
}
