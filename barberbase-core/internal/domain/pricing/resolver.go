// Package pricing resolves what a service costs at a given barber tier.
//
// Everything here is pure: no database, no clock, no I/O. Callers fetch the
// rows and hand them in. That keeps the rules testable without Postgres and
// keeps the sparse-matrix semantics in one readable place.
package pricing

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrNegativePrice is returned when an adjustment would take a price below zero.
// Refused rather than clamped: silently pinning a price at zero is how a shop
// ends up giving away haircuts.
var ErrNegativePrice = errors.New("adjustment would produce a negative price")

// Variant is a service_variants row's base pricing.
type Variant struct {
	ID              uuid.UUID
	Name            string
	PricePaise      int
	DurationMinutes int
}

// Override is a service_variant_tier_prices row: an entry in the SPARSE matrix.
// A nil DurationMinutes means inherit the variant's duration — that is the whole
// reason the column is nullable.
type Override struct {
	PricePaise      int
	DurationMinutes *int
	IsOffered       bool
}

// Resolved is one variant priced at one tier.
type Resolved struct {
	VariantID       uuid.UUID
	VariantName     string
	PricePaise      int
	DurationMinutes int
	IsOffered       bool
	Inherited       bool // true when no override row existed for this (variant, tier)
}

// Selection is N variants priced at one tier.
type Selection struct {
	Items                []Resolved
	TotalPricePaise      int
	TotalDurationMinutes int
	// Available is false when ANY selected variant is not offered at this tier.
	// The tier is unavailable for the selection as a whole — the caller must not
	// drop the variant or fall back to base price.
	Available   bool
	Unavailable []Resolved
}

// Resolve prices one variant at one tier. override is nil when the sparse matrix
// has no row for the pair, which is the common case and means inherit.
//
// buffer_minutes is not tier-varying: it stays on the variant and is untouched
// here.
func Resolve(v Variant, override *Override) Resolved {
	if override == nil {
		return Resolved{
			VariantID:       v.ID,
			VariantName:     v.Name,
			PricePaise:      v.PricePaise,
			DurationMinutes: v.DurationMinutes,
			IsOffered:       true,
			Inherited:       true,
		}
	}
	duration := v.DurationMinutes
	if override.DurationMinutes != nil {
		duration = *override.DurationMinutes
	}
	return Resolved{
		VariantID:       v.ID,
		VariantName:     v.Name,
		PricePaise:      override.PricePaise,
		DurationMinutes: duration,
		IsOffered:       override.IsOffered,
	}
}

// ResolveSelection prices every variant at one tier and totals them.
//
// overrides is keyed by variant ID and holds only the rows that exist; a missing
// key means inherit. If any variant is not offered at this tier the whole
// selection is unavailable, and Unavailable names the offenders so the caller
// can say which service is the problem.
func ResolveSelection(variants []Variant, overrides map[uuid.UUID]Override) Selection {
	sel := Selection{Items: make([]Resolved, 0, len(variants)), Available: true}
	for _, v := range variants {
		var ov *Override
		if o, ok := overrides[v.ID]; ok {
			ov = &o
		}
		r := Resolve(v, ov)
		sel.Items = append(sel.Items, r)
		sel.TotalPricePaise += r.PricePaise
		sel.TotalDurationMinutes += r.DurationMinutes
		if !r.IsOffered {
			sel.Available = false
			sel.Unavailable = append(sel.Unavailable, r)
		}
	}
	return sel
}

// ApplyDelta adds a fixed amount of paise to a base price.
//
// Exact integer arithmetic, never rounded — a rupee delta is already whole
// rupees, and routing it through a rounding step could only ever make
// "350 minus 50" produce something other than 300.
func ApplyDelta(basePaise, deltaPaise int) (int, error) {
	out := basePaise + deltaPaise
	if out < 0 {
		return 0, fmt.Errorf("%w: %d + %d", ErrNegativePrice, basePaise, deltaPaise)
	}
	return out, nil
}

// ApplyPercent adjusts a base price by a whole-number percentage, rounding
// half-up to the nearest whole rupee. x.50 always rounds UP: a price that fell
// when the owner applied a raise is a support ticket nobody wants.
//
// No floating point at any step, including intermediates. The sign is checked
// before rounding, so Go's truncation-toward-zero never sees a negative operand
// — which is what would otherwise break the half-up "+5000" trick.
func ApplyPercent(basePaise, pct int) (int, error) {
	// scaled is in hundredths of paise, so one rupee is 10_000.
	scaled := basePaise * (100 + pct)
	if scaled < 0 {
		return 0, fmt.Errorf("%w: %d at %d%%", ErrNegativePrice, basePaise, pct)
	}
	rupees := (scaled + 5000) / 10000
	return rupees * 100, nil
}
