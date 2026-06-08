# Purpose
Documents the appointment lifecycle, booking resolver logic, and how appointments convert to queue entries at check-in time.
 
# Use This File When
- Implementing POST /v1/appointments/book
- Implementing the booking-options resolver
- Understanding when an appointment becomes a queue_entry
- Implementing appointment confirmation and reminder notifications
# Do Not Use This File For
- Walk-in queue joining (→ `10_customer_journey.md`)
- Queue locking for when appointment check-in creates a queue_entry (→ `05_queue_locking_transactions.md`)
- Template parameters (→ `09_notifications_templates.md`)
# Related Files
- `001_complete_schema.sql` — `appointments`, `checkin_intents` tables
- `openapi.yaml` — `/appointments/book`, `/public/locations/{id}/booking-options`
- `05_queue_locking_transactions.md` — booking resolver logic, checkout transaction
- `09_notifications_templates.md` — Templates 6 (confirmed) and 7 (reminder)
# Source Of Truth Priority
`001_complete_schema.sql` for appointment schema. `openapi.yaml` for endpoint contract.
 
---
 
## Appointment vs Queue Entry
 
| Concept | Meaning |
|---|---|
| `appointments` | A future booking intent. Scheduled. Not operational. |
| `queue_entries` | The operational token. Created only when customer physically checks in on the day. |
 
An appointment does NOT create a queue_entry at booking time. It creates one at check-in on the appointment day.
 
---
 
## Appointment Lifecycle
 
```
'scheduled' → 'checked_in'     Customer arrives, staff converts to queue_entry
'scheduled' → 'cancelled'      Customer or staff cancels before the day
'scheduled' → 'no_show'        Appointment day passes, customer never arrived
'scheduled' → 'rescheduled'    Phase 2 (not implemented)
'checked_in' is terminal for this lifecycle (queue_entry takes over)
```
 
---
 
## Booking Resolver
 
```
POST /v1/public/locations/{id}/booking-options
Input: variant_ids[], party_size
 
1. total_duration_minutes = Σ(variant.duration_minutes) × party_size
2. allowed_modes determination:
     - ANY variant with requires_appointment=true → only 'appointment' allowed
     - ALL variants allow_walk_in=true → add 'walk_in' to allowed
     - ALL variants allow_appointment=true → add 'appointment' to allowed
3. Walk-in time gate:
     current_time + total_duration_minutes > closing_time + allow_overtime_minutes
     → remove 'walk_in'
4. Queue size gate:
     queue_length >= location.max_total_queue_size
     → remove 'walk_in'
5. operation_mode filter:
     'walk_in_only' → remove 'appointment'
     'appointment_only' → remove 'walk_in'
     'hybrid' → keep both if still allowed
6. If allowed = [] → return blocked_reason
```
 
Returns: `{ total_duration_minutes, total_price_paise, allowed_modes[], queue_length, estimated_wait_minutes, blocked_reason? }`
 
---
 
## Book Appointment Transaction
 
```
POST /v1/appointments/book
Auth: Staff-created only in Phase 1. Public booking UI deferred to Phase 1.5.
 
BEGIN
  1. Validate idempotency_key (see 05_queue_locking_transactions.md)
  2. Validate slot availability:
       Check no overlapping appointment for requested barber (if requested)
       Check shop is open on that day
  3. Resolve/create customer by phone_number
  4. INSERT appointment (status='scheduled')
  5. Snapshot variant_ids into appointment.variant_ids (JSONB)
  6. INSERT outbox_event: bb_appointment_confirmed (process_after=NOW())
  7. INSERT outbox_event: bb_appointment_reminder
       (process_after = day_before_appointment at 6 PM)
COMMIT
```
 
Returns: `AppointmentResponse { id, scheduled_start_at, status, services[], magic_link }`
 
---
 
## Appointment Check-in (Appointment Day)
 
When a customer arrives for their appointment:
 
```
Staff action (or customer scan):
  POST /v1/staff/appointments/{appointment_id}/checkin (StaffJWT)
 
BEGIN
  Lock queue_session FOR UPDATE
  Validate appointment.status = 'scheduled'
  Validate appointment.scheduled_start_at is today
  INSERT visit (source='appointment')
  INSERT visit_services (snapshot from appointment.variant_ids)
  INSERT queue_entry:
    state = 'waiting'
    presence = 'arrived'   ← appointment check-in = physical presence assumed
    is_dispatchable = true
    priority_group = 50    ← appointments get higher priority than walk-ins (100)
  UPDATE appointment.status = 'checked_in'
  queue_version++
COMMIT → SSE broadcast
```
 
---
 
## Phase 1 Booking Constraint
 
**Staff-created only in Phase 1.** The public appointment date-picker UI is deferred to Phase 1.5.
 
In Phase 1, appointments are created by:
- Staff from the dashboard (creating on behalf of a customer)
- Via WhatsApp BOOK flow (when implemented — uses same endpoint)
`initiated_via` field captures the source: `'staff_dashboard'` | `'whatsapp'` | `'web_form'` | `'ai_agent'`
 
---
 
## Service Catalog Structure
 
Three-level hierarchy:
```
service_categories  (tab label: "Hair" / "Beard" / "Skin", gender-tagged)
    └── service_groups  (e.g. "Fade", "Hair Color", "Threading")
            └── service_variants  (bookable leaf: "Mid Fade", price_paise, duration_minutes)
```
 
`service_variants.is_popular = true` → surfaces on shop landing page quick-pick section.
 
`visit_services` snapshots the variant data at booking time:
- `variant_name_snapshot`, `group_name_snapshot`, `category_name_snapshot`
- `duration_minutes_snapshot`, `price_paise_snapshot`
**These snapshots are immutable (Law 10).** If a shop changes prices next month, historical visits are unaffected.
