package queue

import "time"

// VariantForResolver is a minimal view of service_variants needed for resolution.
// Populated by the repository layer before calling ResolveBookingOptions.
type VariantForResolver struct {
	ID                  string // UUID
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
}

// BookingResolverInput carries all pre-loaded data the resolver needs.
// DB calls happen in the handler before constructing this struct.
type BookingResolverInput struct {
	Variants             []VariantForResolver
	PartySize            int
	// From locations table
	MaxTotalQueueSize    int
	AllowOvertimeMinutes int
	OperationMode        string // 'walk_in_only' | 'appointment_only' | 'hybrid'
	// Computed shop status (override > hours)
	ShopStatus           string // 'open' | 'closing_soon' | 'temporarily_closed' | 'closed'
	// From location_hours for today (nil if location is closed today)
	ClosesAt             *time.Time // location-timezone wall clock time
	IsOpenToday          bool
	CurrentTime          time.Time
	// From queue_sessions + queue_entries
	QueueLength          int
	EstimatedWaitMinutes int
}

// BookingResolverResult maps directly to the BookingOptions OpenAPI schema.
type BookingResolverResult struct {
	TotalDurationMinutes int      `json:"total_duration_minutes"`
	TotalPricePaise      int      `json:"total_price_paise"`
	// JSON key: allowed_entry_methods (NOT allowed_modes)
	AllowedEntryMethods  []string `json:"allowed_entry_methods"` // "walk_in" | "appointment"
	BlockedReason        *string  `json:"blocked_reason,omitempty"`  // "shop_closed" | "queue_full" | "requires_appointment" | "closing_time_exceeded"
	QueueLength          int      `json:"queue_length"`
	EstimatedWaitMinutes int      `json:"estimated_wait_minutes"`
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func remove(slice []string, item string) []string {
	var out []string
	for _, s := range slice {
		if s != item {
			out = append(out, s)
		}
	}
	return out
}

// ResolveBookingOptions applies booking rules in deterministic priority order:
// 1. Variant rules (requires_appointment wins unconditionally)
// 2. Shop state / timing gates (remove walk_in only)
// 3. operation_mode narrows further
func ResolveBookingOptions(in BookingResolverInput) BookingResolverResult {
	// Step 1: totals
	totalDuration := 0
	totalPrice := 0
	for _, v := range in.Variants {
		totalDuration += v.DurationMinutes
		totalPrice += v.PricePaise
	}
	totalDuration *= in.PartySize

	// Step 2: variant-level rules (strictest wins)
	anyRequiresAppointment := false
	allAllowWalkIn := true
	allAllowAppointment := true
	for _, v := range in.Variants {
		if v.RequiresAppointment {
			anyRequiresAppointment = true
		}
		if !v.AllowWalkIn {
			allAllowWalkIn = false
		}
		if !v.AllowAppointment {
			allAllowAppointment = false
		}
	}

	allowedSet := []string{}
	var blockedReason *string

	if anyRequiresAppointment {
		// requires_appointment overrides everything — walk_in is never allowed
		if allAllowAppointment {
			allowedSet = []string{"appointment"}
		}
		// If also !allAllowAppointment: allowedSet stays empty
		r := "requires_appointment"
		blockedReason = &r
	} else {
		if allAllowWalkIn {
			allowedSet = append(allowedSet, "walk_in")
		}
		if allAllowAppointment {
			allowedSet = append(allowedSet, "appointment")
		}
	}

	// Step 3: shop state / timing gates — only removes walk_in
	if contains(allowedSet, "walk_in") {
		if in.ShopStatus == "closed" || in.ShopStatus == "temporarily_closed" {
			r := "shop_closed"
			blockedReason = &r
			allowedSet = remove(allowedSet, "walk_in")
		} else if !in.IsOpenToday {
			r := "shop_closed"
			blockedReason = &r
			allowedSet = remove(allowedSet, "walk_in")
		} else if in.ClosesAt != nil {
			deadline := in.ClosesAt.Add(time.Duration(in.AllowOvertimeMinutes) * time.Minute)
			serviceEnd := in.CurrentTime.Add(time.Duration(totalDuration) * time.Minute)
			if serviceEnd.After(deadline) {
				r := "closing_time_exceeded"
				blockedReason = &r
				allowedSet = remove(allowedSet, "walk_in")
			}
		}
	}

	if contains(allowedSet, "walk_in") && in.QueueLength >= in.MaxTotalQueueSize {
		r := "queue_full"
		blockedReason = &r
		allowedSet = remove(allowedSet, "walk_in")
	}

	// Step 4: operation_mode filter
	switch in.OperationMode {
	case "walk_in_only":
		allowedSet = remove(allowedSet, "appointment")
	case "appointment_only":
		allowedSet = remove(allowedSet, "walk_in")
		if !contains(allowedSet, "walk_in") && blockedReason == nil {
			r := "requires_appointment"
			blockedReason = &r
		}
	}

	if len(allowedSet) > 0 {
		blockedReason = nil
	}

	return BookingResolverResult{
		TotalDurationMinutes: totalDuration,
		TotalPricePaise:      totalPrice,
		AllowedEntryMethods:  allowedSet,
		BlockedReason:        blockedReason,
		QueueLength:          in.QueueLength,
		EstimatedWaitMinutes: in.EstimatedWaitMinutes,
	}
}
