package pricing

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func minutes(n int) *int { return &n }

var (
	fade   = Variant{ID: uuid.New(), Name: "Mid Fade", PricePaise: 25000, DurationMinutes: 30}
	beard  = Variant{ID: uuid.New(), Name: "Beard Trim", PricePaise: 15000, DurationMinutes: 15}
	colour = Variant{ID: uuid.New(), Name: "Colour", PricePaise: 120000, DurationMinutes: 90}
)

// TestResolve is A1: the sparse-inheritance rule, one variant at a time.
func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name         string
		override     *Override
		wantPrice    int
		wantDuration int
		wantOffered  bool
		wantInherit  bool
	}{
		{
			name:      "no_override_inherits_both",
			override:  nil,
			wantPrice: 25000, wantDuration: 30, wantOffered: true, wantInherit: true,
		},
		{
			name:      "override_price_only_inherits_duration",
			override:  &Override{PricePaise: 45000, DurationMinutes: nil, IsOffered: true},
			wantPrice: 45000, wantDuration: 30, wantOffered: true,
		},
		{
			name:      "override_price_and_duration",
			override:  &Override{PricePaise: 45000, DurationMinutes: minutes(45), IsOffered: true},
			wantPrice: 45000, wantDuration: 45, wantOffered: true,
		},
		{
			name:      "not_offered_is_a_row_not_a_null_price",
			override:  &Override{PricePaise: 0, DurationMinutes: nil, IsOffered: false},
			wantPrice: 0, wantDuration: 30, wantOffered: false,
		},
		{
			name:      "override_equal_to_base_still_resolves",
			override:  &Override{PricePaise: 25000, DurationMinutes: minutes(30), IsOffered: true},
			wantPrice: 25000, wantDuration: 30, wantOffered: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(fade, tc.override)
			require.Equal(t, tc.wantPrice, got.PricePaise)
			require.Equal(t, tc.wantDuration, got.DurationMinutes)
			require.Equal(t, tc.wantOffered, got.IsOffered)
			require.Equal(t, tc.wantInherit, got.Inherited)
			require.Equal(t, fade.ID, got.VariantID)
			require.Equal(t, "Mid Fade", got.VariantName)
		})
	}
}

// TestResolveSelection is A2 and A3: totals across a mix of inherited and
// overridden variants, and the all-or-nothing availability rule.
func TestResolveSelection(t *testing.T) {
	t.Run("A3_totals_across_mixed_inheritance", func(t *testing.T) {
		sel := ResolveSelection(
			[]Variant{fade, beard, colour},
			map[uuid.UUID]Override{
				fade.ID:  {PricePaise: 45000, DurationMinutes: minutes(45), IsOffered: true},
				beard.ID: {PricePaise: 20000, DurationMinutes: nil, IsOffered: true},
				// colour inherits entirely.
			})
		require.True(t, sel.Available)
		require.Len(t, sel.Items, 3)
		// 45000 + 20000 + 120000
		require.Equal(t, 185000, sel.TotalPricePaise)
		// 45 (override) + 15 (inherited) + 90 (inherited)
		require.Equal(t, 150, sel.TotalDurationMinutes)
		require.False(t, sel.Items[0].Inherited)
		require.False(t, sel.Items[1].Inherited)
		require.True(t, sel.Items[2].Inherited, "colour had no override row")
	})

	t.Run("A3_all_inherited", func(t *testing.T) {
		sel := ResolveSelection([]Variant{fade, beard}, map[uuid.UUID]Override{})
		require.True(t, sel.Available)
		require.Equal(t, 40000, sel.TotalPricePaise)
		require.Equal(t, 45, sel.TotalDurationMinutes)
	})

	t.Run("A2_one_unoffered_variant_sinks_the_selection", func(t *testing.T) {
		sel := ResolveSelection(
			[]Variant{fade, colour},
			map[uuid.UUID]Override{
				colour.ID: {PricePaise: 0, IsOffered: false}, // juniors don't do colour
			})
		require.False(t, sel.Available, "A2: the tier is unavailable for the selection as a whole")
		require.Len(t, sel.Unavailable, 1)
		require.Equal(t, colour.ID, sel.Unavailable[0].VariantID)
		require.Equal(t, "Colour", sel.Unavailable[0].VariantName, "A2: the offender must be identifiable")
		// The offered variant is still resolved — the caller decides what to show.
		require.Len(t, sel.Items, 2)
		require.Equal(t, 25000, sel.Items[0].PricePaise)
	})

	t.Run("A2_no_silent_fallback_to_base", func(t *testing.T) {
		sel := ResolveSelection([]Variant{colour},
			map[uuid.UUID]Override{colour.ID: {PricePaise: 0, IsOffered: false}})
		require.False(t, sel.Available)
		require.NotEqual(t, colour.PricePaise, sel.TotalPricePaise,
			"A2: an unoffered variant must not quietly fall back to the base price")
	})

	t.Run("empty_selection", func(t *testing.T) {
		sel := ResolveSelection(nil, nil)
		require.True(t, sel.Available)
		require.Zero(t, sel.TotalPricePaise)
		require.Zero(t, sel.TotalDurationMinutes)
	})
}

// TestApplyDelta and TestApplyPercent are A11 and A12: the stated rounding rule,
// exercised on the exact table in the brief. Rupee inputs and expectations are
// spelled out so a reader can check the arithmetic without converting paise.
func TestApplyDelta(t *testing.T) {
	got, err := ApplyDelta(35000, -5000)
	require.NoError(t, err)
	require.Equal(t, 30000, got, "A11: ₹350 − ₹50 is exactly ₹300, never rounded")

	got, err = ApplyDelta(35000, 10000)
	require.NoError(t, err)
	require.Equal(t, 45000, got)

	// A12
	_, err = ApplyDelta(35000, -40000)
	require.ErrorIs(t, err, ErrNegativePrice, "A12: refused, not clamped to zero")

	got, err = ApplyDelta(35000, -35000)
	require.NoError(t, err)
	require.Zero(t, got, "exactly zero is allowed; below zero is not")
}

func TestApplyPercent(t *testing.T) {
	for _, tc := range []struct {
		rupeesIn  int
		pct       int
		rupeesOut int
		why       string
	}{
		{350, 10, 385, "38500 exact"},
		{355, 10, 391, "39050 → half-up → 39100"},
		{345, 10, 380, "37950 → half-up → 38000"},
		{299, 10, 329, "32890 → nearest → 32900"},
		{350, -10, 315, "31500 exact"},
		{355, -10, 320, "31950 → half-up → 32000"},
		{350, 0, 350, "no change"},
		{350, -100, 0, "minus everything is zero, not an error"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			got, err := ApplyPercent(tc.rupeesIn*100, tc.pct)
			require.NoError(t, err)
			require.Equal(t, tc.rupeesOut*100, got,
				"A11: ₹%d at %+d%% should be ₹%d (%s)", tc.rupeesIn, tc.pct, tc.rupeesOut, tc.why)
			require.Zero(t, got%100, "A11: prices stay whole rupees")
		})
	}

	// A12: below -100% would go negative. Refused before any rounding, so Go's
	// truncation-toward-zero never sees a negative operand.
	_, err := ApplyPercent(35000, -150)
	require.ErrorIs(t, err, ErrNegativePrice)
}

// TestNoFloatingPointDrift walks a wide range and asserts every result is a
// whole number of rupees. A float creeping into the percentage path would show
// up here as a stray paise.
func TestNoFloatingPointDrift(t *testing.T) {
	for base := 100; base <= 500000; base += 137 {
		for _, pct := range []int{-99, -50, -10, -1, 0, 1, 10, 50, 100, 200} {
			got, err := ApplyPercent(base, pct)
			require.NoError(t, err)
			require.Zero(t, got%100, "base=%d pct=%d gave %d, not a whole rupee", base, pct, got)
			require.GreaterOrEqual(t, got, 0)
		}
	}
}
