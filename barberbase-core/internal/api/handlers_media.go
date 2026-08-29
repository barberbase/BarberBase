package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/media"
	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Media routes: presign → (browser PUTs to R2) → commit, plus list and archive.
// THE IMAGE BYTES NEVER TOUCH THIS SERVER in either direction — see the package
// comment on internal/domain/media.
//
// ERROR MAPPING. This table is the contract. It lived only in M2's report until
// O5; mediaErrorStatus below is its single implementation, and all four routes
// go through it so they cannot drift apart.
//
//	| Error                | Status | Why                                        |
//	|----------------------|--------|--------------------------------------------|
//	| ErrRateLimited       | 429    | + Retry-After, taken from RateLimitedError |
//	| r2.ErrNotConfigured  | 503    | media is off on this deployment, not gone  |
//	| r2.ErrUnavailable    | 503    | R2 unreachable; the row stays pending      |
//	| ErrNotUploaded       | 409    | object is not there yet; upload, then retry|
//	| ErrTooLarge          | 413    | object exceeds MEDIA_MAX_BYTES             |
//	| ErrWrongType         | 415    | object is not image/webp                   |
//	| ErrBadContentHash    | 400    | not 64 lowercase hex                       |
//	| ErrBadPurpose        | 400    | unknown purpose, or missing its id         |
//	| ErrVariantFull       | 409    | variant already holds MEDIA_MAX_PER_VARIANT|
//	| ErrAssetNotFound     | 404    | no such asset in this tenant AND location  |
//
// Anything unlisted is 500. ErrAssetNotFound is deliberately 404 rather than
// 403: the repository scopes every read by tenant and location, so "not yours"
// and "does not exist" are the same answer and must look the same from outside.
func mediaErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, media.ErrRateLimited):
		return http.StatusTooManyRequests, "RATE_LIMITED"
	case errors.Is(err, r2.ErrNotConfigured):
		return http.StatusServiceUnavailable, "MEDIA_NOT_CONFIGURED"
	case errors.Is(err, r2.ErrUnavailable):
		return http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE"
	case errors.Is(err, media.ErrNotUploaded):
		return http.StatusConflict, "NOT_UPLOADED"
	case errors.Is(err, media.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "TOO_LARGE"
	case errors.Is(err, media.ErrWrongType):
		return http.StatusUnsupportedMediaType, "WRONG_TYPE"
	case errors.Is(err, repository.ErrBadContentHash):
		return http.StatusBadRequest, "BAD_CONTENT_HASH"
	case errors.Is(err, media.ErrBadPurpose):
		return http.StatusBadRequest, "BAD_PURPOSE"
	case errors.Is(err, repository.ErrVariantFull):
		return http.StatusConflict, "VARIANT_FULL"
	case errors.Is(err, repository.ErrAssetNotFound):
		return http.StatusNotFound, "ASSET_NOT_FOUND"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

// respondMediaErr writes the mapped status. Retry-After rides along on 429,
// carried by the error itself so the delay is never re-derived here.
func respondMediaErr(w http.ResponseWriter, err error) {
	status, code := mediaErrorStatus(err)
	var rl media.RateLimitedError
	if errors.As(err, &rl) {
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds()+0.999)))
	}
	respondAdminJSON(w, status, map[string]string{"code": code, "message": err.Error()})
}

// mediaCaller runs the owner/manager gate and pulls tenant + location + staff
// from the JWT claims, never from the request (Law 11). It also refuses every
// media route with 503 when storage is unconfigured — prod runs that way today,
// and Archive alone would otherwise "succeed" against Postgres while its R2
// object was never scheduled for anything.
func (s *Server) mediaCaller(w http.ResponseWriter, r *http.Request) (tenantID, locationID, staffID uuid.UUID, ok bool) {
	ctx := r.Context()
	role := auth.RoleFromCtx(ctx)
	if role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if s.Media == nil || !s.Media.Store.Configured() {
		respondAdminJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "MEDIA_NOT_CONFIGURED", "message": r2.ErrNotConfigured.Error()})
		return
	}
	tenantID, err := uuid.Parse(auth.TenantIDFromCtx(ctx))
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	locationID, err = uuid.Parse(auth.LocationIDFromCtx(ctx))
	if err != nil {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	staffID, _ = uuid.Parse(auth.StaffMemberIDFromCtx(ctx))
	return tenantID, locationID, staffID, true
}

// ownsReferencedRow enforces Law 11 on the ids in a presign BODY.
//
// The repository cannot do this: CreatePending inserts the caller's tenant_id
// beside a client-supplied service_variant_id, and the foreign key only proves
// that variant exists SOMEWHERE. Without this check a shop could presign against
// another shop's variant, and since the render path is keyed on
// service_variant_id alone, its image would surface on that shop's service.
// 404, not 403 — the same "not yours is not found" rule the asset routes use.
func (s *Server) ownsReferencedRow(ctx context.Context, table string, id uuid.UUID, tenantID, locationID uuid.UUID) error {
	var found uuid.UUID
	q := `SELECT id FROM service_variants WHERE id = $1 AND tenant_id = $2 AND location_id = $3`
	if table == "staff_members" {
		q = `SELECT id FROM staff_members WHERE id = $1 AND tenant_id = $2 AND location_id = $3`
	}
	if err := s.Pool.QueryRow(ctx, q, id, tenantID, locationID).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrAssetNotFound
		}
		return err
	}
	return nil
}

// PresignMedia is POST /admin/media/presign.
func (s *Server) PresignMedia(w http.ResponseWriter, r *http.Request) {
	tenantID, locationID, staffID, ok := s.mediaCaller(w, r)
	if !ok {
		return
	}
	var body PresignMediaJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}

	ctx := r.Context()
	if body.ServiceVariantId != nil {
		if err := s.ownsReferencedRow(ctx, "service_variants", *body.ServiceVariantId, tenantID, locationID); err != nil {
			respondMediaErr(w, err)
			return
		}
	}
	if body.StaffMemberId != nil {
		if err := s.ownsReferencedRow(ctx, "staff_members", *body.StaffMemberId, tenantID, locationID); err != nil {
			respondMediaErr(w, err)
			return
		}
	}

	out, err := s.Media.Presign(ctx, media.PresignInput{
		TenantID:      tenantID,
		LocationID:    locationID,
		StaffMemberID: staffID,
		Purpose:       string(body.Purpose),
		ContentHash:   body.ContentHash,
		VariantID:     body.ServiceVariantId,
		StaffID:       body.StaffMemberId,
	}, time.Now())
	if err != nil {
		respondMediaErr(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, MediaPresignResponse{
		MediaAssetId: out.MediaAssetID,
		R2Key:        out.R2Key,
		UploadUrl:    out.UploadURL,
		ExpiresAt:    out.ExpiresAt,
	})
}

// CommitMedia is POST /admin/media/{media_asset_id}/commit.
func (s *Server) CommitMedia(w http.ResponseWriter, r *http.Request, mediaAssetId UUIDv7) {
	tenantID, locationID, _, ok := s.mediaCaller(w, r)
	if !ok {
		return
	}
	// Body is optional: a commit with no alt_text is legitimate.
	var body CommitMediaJSONRequestBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	asset, err := s.Media.Commit(r.Context(), tenantID, locationID, mediaAssetId, body.AltText)
	if err != nil {
		respondMediaErr(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, toMediaAssetJSON(asset, s.Config.R2MediaPublicBaseURL))
}

// ArchiveMedia is DELETE /admin/media/{media_asset_id}.
func (s *Server) ArchiveMedia(w http.ResponseWriter, r *http.Request, mediaAssetId UUIDv7) {
	tenantID, locationID, _, ok := s.mediaCaller(w, r)
	if !ok {
		return
	}
	if err := s.Media.Archive(r.Context(), tenantID, locationID, mediaAssetId); err != nil {
		respondMediaErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListMedia is GET /admin/media.
func (s *Server) ListMedia(w http.ResponseWriter, r *http.Request, params ListMediaParams) {
	tenantID, locationID, _, ok := s.mediaCaller(w, r)
	if !ok {
		return
	}
	f := repository.MediaListFilters{
		ServiceVariantID: params.ServiceVariantId,
		StaffMemberID:    params.StaffMemberId,
		IncludeArchived:  params.IncludeArchived != nil && *params.IncludeArchived,
	}
	if params.Purpose != nil {
		p := string(*params.Purpose)
		f.Purpose = &p
	}

	assets, err := s.Media.Repo.ListAssets(r.Context(), tenantID, locationID, f)
	if err != nil {
		respondMediaErr(w, err)
		return
	}
	out := make([]MediaAsset, 0, len(assets))
	for _, a := range assets {
		out = append(out, toMediaAssetJSON(a, s.Config.R2MediaPublicBaseURL))
	}
	respondAdminJSON(w, http.StatusOK, out)
}

// toMediaAssetJSON maps the repository row onto the generated wire type.
// public_url is omitted rather than emitted half-built when no public base is
// configured — a bare r2_key is not a URL and must never look like one.
func toMediaAssetJSON(a repository.MediaAsset, publicBase string) MediaAsset {
	out := MediaAsset{
		Id:               a.ID,
		Purpose:          MediaAssetPurpose(a.Purpose),
		ServiceVariantId: a.ServiceVariantID,
		StaffMemberId:    a.StaffMemberID,
		R2Key:            a.R2Key,
		ContentHash:      a.ContentHash,
		Bytes:            a.Bytes,
		AltText:          a.AltText,
		SortOrder:        a.SortOrder,
		IsPrimary:        a.IsPrimary,
		Status:           MediaAssetStatus(a.Status),
		CreatedAt:        a.CreatedAt,
		CommittedAt:      a.CommittedAt,
	}
	if publicBase != "" {
		u := publicBase + "/" + a.R2Key
		out.PublicUrl = &u
	}
	return out
}
