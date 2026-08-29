package api

import (
	"net/http"

	"barberbase-core/internal/domain/pricing"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
)

// ListPublicBarbers is GET /public/locations/{location_slug}/barbers — the
// customer's barber picker, and the screen where money is traded against time.
//
// PUBLIC. Everything below is visible to anyone with the shop link, so the
// response carries display name, masked availability, tier, price and wait, and
// nothing else. No phone number, no role, no internal status, no per-barber
// performance of any kind. Fields that do not exist in the schema
// (years_experience, specialities) are not invented here, and there is no
// rating: in a four-chair shop the barbers can see this screen, and a public
// per-employee score is a workplace problem before it is a feature.
func (s *Server) ListPublicBarbers(w http.ResponseWriter, r *http.Request, locationSlug string, params ListPublicBarbersParams) {
	ctx := r.Context()
	location, err := repository.GetLocationBySlug(ctx, s.Pool, locationSlug)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if location == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "location_not_found"})
		return
	}
	locationID, err := uuid.Parse(location.ID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	tenantID, err := uuid.Parse(location.TenantID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	barbers, err := repository.ListPublicBarbers(ctx, s.Pool, locationID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	// Price and wait only mean something once the customer has said what they
	// want done. Without variant_ids the picker still renders — names, tiers and
	// availability — and the numeric fields stay absent rather than zero.
	var priced map[uuid.UUID]int
	var waits map[uuid.UUID]int
	var defaultTierTotal *int
	if params.VariantIds != nil && len(*params.VariantIds) > 0 {
		priced, defaultTierTotal, err = s.priceBarbers(r, tenantID, locationID, barbers, *params.VariantIds)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		load, errLoad := repository.LoadQueueForWait(ctx, s.Pool, locationID)
		if errLoad != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		waits = repository.PublicBarberWaits(barbers, load)
	}

	out := make([]PublicBarber, 0, len(barbers))
	for _, b := range barbers {
		pb := PublicBarber{
			StaffMemberId: b.ID,
			DisplayName:   b.Name,
			Availability:  PublicBarberAvailability(b.Availability),
			AvatarUrl:     publicAvatarURL(b.AvatarKey, s.Config.R2MediaPublicBaseURL),
		}
		if b.TierID != nil {
			pb.Tier = &struct {
				Id   *UUIDv7 `json:"id,omitempty"`
				Name *string `json:"name,omitempty"`
				Rank *int    `json:"rank,omitempty"`
			}{Id: b.TierID, Name: b.TierName, Rank: b.TierRank}
		}
		if priced != nil {
			total := priced[b.ID]
			pb.PricePaise = &total
			if defaultTierTotal != nil {
				delta := total - *defaultTierTotal
				pb.PriceDeltaPaise = &delta
			}
			wait := waits[b.ID]
			pb.EstWaitMinutes = &wait
		}
		out = append(out, pb)
	}
	respondJSON(w, http.StatusOK, out)
}

// priceBarbers totals the requested services at each barber's tier, and at the
// location's default tier for the delta.
//
// Overrides are read once per DISTINCT tier rather than once per barber — four
// barbers in two tiers is two reads, not four. An untiered barber is priced at
// the variants' base rate, which is what pricing.Resolve does with no override.
//
// The default tier may not exist: idx_staff_tiers_one_default is partial and
// permits zero. Then the anchor is nil, price still ships, and the delta is
// omitted rather than silently computed against something else.
func (s *Server) priceBarbers(r *http.Request, tenantID, locationID uuid.UUID,
	barbers []repository.PublicBarberRow, variantIDs []uuid.UUID) (map[uuid.UUID]int, *int, error) {

	ctx := r.Context()
	// A struct over the pool; constructing it here keeps this route working
	// regardless of whether Server.Tiers was wired.
	tiers := repository.TierRepository{Pool: s.Pool}

	variants, err := tiers.GetVariantsForPricing(ctx, tenantID, locationID, variantIDs)
	if err != nil {
		return nil, nil, err
	}

	// Unknown or inactive ids are simply absent from variants — ignored, as the
	// operation description promises, rather than failing the whole page.
	totalFor := func(tierID *uuid.UUID) (int, error) {
		overrides := map[uuid.UUID]pricing.Override{}
		if tierID != nil {
			overrides, err = tiers.GetOverrides(ctx, tenantID, locationID, *tierID, variantIDs)
			if err != nil {
				return 0, err
			}
		}
		return pricing.ResolveSelection(variants, overrides).TotalPricePaise, nil
	}

	byTier := map[uuid.UUID]int{}
	baseTotal, err := totalFor(nil)
	if err != nil {
		return nil, nil, err
	}
	priced := make(map[uuid.UUID]int, len(barbers))
	for _, b := range barbers {
		if b.TierID == nil {
			priced[b.ID] = baseTotal
			continue
		}
		if _, seen := byTier[*b.TierID]; !seen {
			t, errT := totalFor(b.TierID)
			if errT != nil {
				return nil, nil, errT
			}
			byTier[*b.TierID] = t
		}
		priced[b.ID] = byTier[*b.TierID]
	}

	// The delta anchor: the location's default tier, never the cheapest barber.
	// Anchoring to cheapest would render every other barber as an upsell and
	// hide the case this screen exists for — a junior who is cheaper AND sooner.
	tierRows, err := tiers.ListTiers(ctx, tenantID, locationID, false)
	if err != nil {
		return nil, nil, err
	}
	var anchor *int
	for _, t := range tierRows {
		if !t.IsDefault {
			continue
		}
		total, errA := totalFor(&t.ID)
		if errA != nil {
			return nil, nil, errA
		}
		anchor = &total
		break
	}
	return priced, anchor, nil
}

// publicAvatarURL joins the object key to the deployment's public base, or
// returns nil. Never a placeholder: a URL that 404s is worse than an honest
// null, and the fallback monogram is the client's to draw.
func publicAvatarURL(key *string, publicBase string) *string {
	if key == nil || publicBase == "" {
		return nil
	}
	u := publicBase + "/" + *key
	return &u
}
