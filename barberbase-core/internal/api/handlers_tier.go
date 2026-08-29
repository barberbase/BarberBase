package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/pricing"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tier admin: the nine routes over T2's tier CRUD and sparse price matrix.
//
// ERROR MAPPING. Single implementation in tierErrorStatus, shared by every
// route so they cannot drift.
//
//	| Error                     | Status | Why                                    |
//	|---------------------------|--------|----------------------------------------|
//	| ErrTierNotFound           | 404    | also covers "not yours" — see below    |
//	| ErrTierInUse              | 409    | body NAMES the blocking barbers        |
//	| pricing.ErrNegativePrice  | 400    | refused, never clamped to zero         |
//	| duplicate (location,name) | 409    | 23505 on staff_tiers_location_id_name  |
//	| duplicate (location,rank) | 409    | 23505 on staff_tiers_location_id_rank  |
//	| cross-tenant tier/variant | 404    | never 403 — see below                  |
//
// Cross-tenant is 404, never 403, on every route: the repository scopes every
// statement by tenant AND location, so "not yours" and "does not exist" are the
// same row count, and a 403 would confirm another shop's tier exists. A raw
// 23503 or 23505 must never reach a caller.
func tierErrorStatus(err error) (int, string) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, repository.ErrTierInUse):
		return http.StatusConflict, "TIER_IN_USE"
	case errors.Is(err, repository.ErrTierNotFound):
		return http.StatusNotFound, "TIER_NOT_FOUND"
	case errors.Is(err, pricing.ErrNegativePrice):
		return http.StatusBadRequest, "NEGATIVE_PRICE"
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		// The two unique constraints an owner can actually collide with.
		if strings.Contains(pgErr.ConstraintName, "rank") {
			return http.StatusConflict, "DUPLICATE_RANK"
		}
		return http.StatusConflict, "DUPLICATE_NAME"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

// respondTierErr writes the mapped status. ErrTierInUse additionally carries the
// blocking barbers, because "cannot delete" without "these four people are in
// it" is a dead end for the owner staring at the screen.
func respondTierErr(w http.ResponseWriter, err error) {
	status, code := tierErrorStatus(err)
	if code == "TIER_IN_USE" {
		respondAdminJSON(w, status, TierInUseError{
			Code:            code,
			Message:         err.Error(),
			BlockingBarbers: blockingBarbers(err),
		})
		return
	}
	respondAdminJSON(w, status, map[string]string{"code": code, "message": err.Error()})
}

// blockingBarbers recovers the names DeactivateTier formatted into the error.
//
// ponytail: parsing our own message beats widening the repository's signature
// for one call site. The format is fixed by tier.go's
// fmt.Errorf("%w: %s", ErrTierInUse, strings.Join(holders, ", ")) — if that
// changes, the A4 test fails loudly rather than silently returning [].
func blockingBarbers(err error) []string {
	rest := strings.TrimPrefix(err.Error(), repository.ErrTierInUse.Error()+": ")
	if rest == "" || rest == err.Error() {
		return []string{}
	}
	return strings.Split(rest, ", ")
}

// tierCaller is the owner/manager gate plus the JWT's tenant and location. Same
// shape as mediaCaller, minus the storage check — tiers need no external
// service, so they work on every deployment.
func (s *Server) tierCaller(w http.ResponseWriter, r *http.Request) (tenantID, locationID uuid.UUID, ok bool) {
	ctx := r.Context()
	if role := auth.RoleFromCtx(ctx); role != "owner" && role != "manager" {
		respondAdminJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if s.Tiers == nil {
		respondAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
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
	return tenantID, locationID, true
}

// ListTiers is GET /admin/tiers.
func (s *Server) ListTiers(w http.ResponseWriter, r *http.Request, params ListTiersParams) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	includeInactive := params.IncludeInactive != nil && *params.IncludeInactive
	rows, err := s.Tiers.ListTiers(r.Context(), tenantID, locationID, includeInactive)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	out := make([]Tier, 0, len(rows))
	for _, t := range rows {
		out = append(out, toTierJSON(t))
	}
	respondAdminJSON(w, http.StatusOK, out)
}

// CreateTier is POST /admin/tiers.
func (s *Server) CreateTier(w http.ResponseWriter, r *http.Request) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	var body CreateTierJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}
	t, err := s.Tiers.CreateTier(r.Context(), tenantID, locationID, body.Name, body.Rank, body.Description)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	respondAdminJSON(w, http.StatusCreated, toTierJSON(t))
}

// UpdateTier is PATCH /admin/tiers/{tier_id}.
func (s *Server) UpdateTier(w http.ResponseWriter, r *http.Request, tierId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	var body UpdateTierJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}
	t, err := s.Tiers.UpdateTier(r.Context(), tenantID, locationID, tierId, body.Name, body.Rank, body.Description)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, toTierJSON(t))
}

// DeactivateTier is DELETE /admin/tiers/{tier_id} — a SOFT delete.
func (s *Server) DeactivateTier(w http.ResponseWriter, r *http.Request, tierId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	if err := s.Tiers.DeactivateTier(r.Context(), tenantID, locationID, tierId); err != nil {
		respondTierErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetDefaultTier is POST /admin/tiers/{tier_id}/default.
func (s *Server) SetDefaultTier(w http.ResponseWriter, r *http.Request, tierId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	if err := s.Tiers.SetDefaultTier(r.Context(), tenantID, locationID, tierId); err != nil {
		respondTierErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AssignStaffTier is PUT /admin/staff/{staff_member_id}/tier.
func (s *Server) AssignStaffTier(w http.ResponseWriter, r *http.Request, staffMemberId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	var body AssignStaffTierJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}
	// One statement validates both ids: the barber by tenant+location, the tier
	// by an EXISTS on the same scope. A cross-tenant either way is 0 rows.
	if err := s.Tiers.AssignStaffTier(r.Context(), tenantID, locationID, staffMemberId, body.TierId); err != nil {
		respondTierErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListResolvedPrices is GET /admin/tiers/{tier_id}/prices — the read side of the
// sparse matrix, one row per active variant whether or not an override exists.
func (s *Server) ListResolvedPrices(w http.ResponseWriter, r *http.Request, tierId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	exists, err := s.Tiers.TierExists(ctx, tenantID, locationID, tierId)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	if !exists {
		respondTierErr(w, repository.ErrTierNotFound)
		return
	}

	variants, err := s.Tiers.ListVariantsForPricing(ctx, tenantID, locationID)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(variants))
	for _, v := range variants {
		ids = append(ids, v.ID)
	}
	overrides, err := s.Tiers.GetOverrides(ctx, tenantID, locationID, tierId, ids)
	if err != nil {
		respondTierErr(w, err)
		return
	}

	// pricing.Resolve already carries Inherited, so the flag is the resolver's
	// answer rather than a second opinion computed here.
	out := make([]ResolvedTierPrice, 0, len(variants))
	for _, v := range variants {
		var ov *pricing.Override
		if o, hit := overrides[v.ID]; hit {
			ov = &o
		}
		res := pricing.Resolve(v, ov)
		out = append(out, ResolvedTierPrice{
			ServiceVariantId:    v.ID,
			VariantName:         v.Name,
			BasePricePaise:      v.PricePaise,
			PricePaise:          res.PricePaise,
			BaseDurationMinutes: v.DurationMinutes,
			DurationMinutes:     res.DurationMinutes,
			IsOffered:           res.IsOffered,
			Inherited:           res.Inherited,
		})
	}
	respondAdminJSON(w, http.StatusOK, out)
}

// UpsertTierPrice is PUT /admin/tiers/{tier_id}/prices/{service_variant_id}.
func (s *Server) UpsertTierPrice(w http.ResponseWriter, r *http.Request, tierId UUIDv7, serviceVariantId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	var body UpsertTierPriceJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}
	if body.PricePaise < 0 {
		respondTierErr(w, pricing.ErrNegativePrice)
		return
	}

	ctx := r.Context()
	// Law 11 on the TIER. UpsertOverride validates the variant but writes the
	// caller's tier_id unchecked, so a cross-tenant tier would otherwise create
	// a row here pointing at another shop's tier.
	exists, err := s.Tiers.TierExists(ctx, tenantID, locationID, tierId)
	if err != nil {
		respondTierErr(w, err)
		return
	}
	if !exists {
		respondTierErr(w, repository.ErrTierNotFound)
		return
	}

	isOffered := body.IsOffered == nil || *body.IsOffered
	if err := s.Tiers.UpsertOverride(ctx, tenantID, locationID, tierId, serviceVariantId,
		body.PricePaise, body.DurationMinutes, isOffered); err != nil {
		respondTierErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BulkApplyTierPrices is POST /admin/tiers/{tier_id}/prices/bulk.
func (s *Server) BulkApplyTierPrices(w http.ResponseWriter, r *http.Request, tierId UUIDv7) {
	tenantID, locationID, ok := s.tierCaller(w, r)
	if !ok {
		return
	}
	var body BulkApplyTierPricesJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code": "INVALID_REQUEST", "message": "failed to decode request body"})
		return
	}
	// XOR, checked here so the caller gets 400 rather than the repository's
	// generic error mapped to 500.
	if (body.DeltaPaise == nil) == (body.Percent == nil) {
		respondAdminJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "INVALID_REQUEST",
			"message": "provide exactly one of delta_paise or percent"})
		return
	}

	affected, err := s.Tiers.BulkApplyTierPrices(r.Context(), tenantID, locationID, tierId,
		repository.BulkAdjustment{DeltaPaise: body.DeltaPaise, Percent: body.Percent})
	if err != nil {
		respondTierErr(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, map[string]int{"affected": affected})
}

func toTierJSON(t repository.TierRow) Tier {
	return Tier{
		Id:          t.ID,
		Name:        t.Name,
		Rank:        t.Rank,
		Description: t.Description,
		IsDefault:   t.IsDefault,
		IsActive:    t.IsActive,
		SortOrder:   t.SortOrder,
	}
}
