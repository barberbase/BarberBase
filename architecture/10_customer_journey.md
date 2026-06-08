# Purpose
Documents the complete customer journey from landing page to post-service feedback, including every API call, state transition, and WhatsApp notification. Also covers the in-app browser environment, magic link page states, and critical design constraints for the WhatsApp webview.
 
# Use This File When
- Implementing the public shop landing page
- Implementing the magic link status page
- Tracing a customer's end-to-end flow for debugging
- Implementing the checkin-intent → WhatsApp join path
# Do Not Use This File For
- Queue state transition rules (→ `04_queue_state_machine.md`)
- Locking and transaction details (→ `05_queue_locking_transactions.md`)
- Auth/token mechanics (→ `03_auth_identity.md`)
- Template parameters (→ `09_notifications_templates.md`)
# Related Files
- `04_queue_state_machine.md`
- `03_auth_identity.md`
- `08_sse_realtime.md`
- `09_notifications_templates.md`
- `12_staff_dashboard_frontend.md`
# Source Of Truth Priority
`openapi.yaml` for API contracts. `001_complete_schema.sql` for `checkin_intents` schema.
 
---
 
## Complete Customer Journey (17 Steps)
 
```
Step 1:  Customer opens https://barberbase.in/star-salon/koramangala
         Page: open/closed status, queue length, estimated wait, service catalog
 
Step 2:  Selects services: e.g. "Mid Fade + Beard Trim"
 
Step 3:  POST /v1/public/locations/{id}/booking-options
         Body: { variant_ids, party_size }
         Returns: total_duration=40min, total_price_paise=30000,
                  allowed=['walk_in'], queue_length=6, estimated_wait=45min
 
Step 4:  Customer taps [Join via WhatsApp]
         POST /v1/public/locations/{id}/checkin-intents
         Body: { variant_ids, party_size, customer_name (optional) }
         Returns: token_code="JN8K4P",
                  deep_link="https://wa.me/+912212345678?text=JOIN%20STAR-SALON%20JN8K4P"
         Page shows: "WhatsApp will open. Press Send to confirm."
 
Step 5:  WhatsApp opens pre-filled message. Customer taps Send.
         Browser tab is now irrelevant — WhatsApp carries the session.
 
Step 6:  Bhejna receives Meta webhook → normalizes → forwards:
         POST /v1/webhooks/bhejna
         Go: HMAC verify → INSERT webhook_events ON CONFLICT DO NOTHING → 200 OK
 
Step 7:  Webhook worker processes event:
         - Classify: "JOIN STAR-SALON JN8K4P"
         - Resolve location via slug "star-salon"
         - Normalize sender.phone_number → E.164 → resolve/create customer
         - SELECT checkin_intent WHERE token_code='JN8K4P' AND status='created'
         - Validate: not expired, shop open
         BEGIN
           Lock queue_session FOR UPDATE (auto-create if needed)
           INSERT visit
           INSERT visit_services (snapshot variant data — immutable)
           INSERT queue_entry (state='waiting', presence='remote', token=18)
           last_token_number++, queue_version++
           UPDATE checkin_intent status='resolved'
           INSERT outbox_event: bb_queue_joined
         COMMIT → SSE broadcast
 
Step 8:  Outbox worker sends bb_queue_joined via Bhejna:
         "✂️ You're in the Queue! Token #18 | 6 ahead | ~45 min
          [Check My Status]  [Cancel My Spot]"
 
Step 9:  Customer taps [Check My Status]
         Opens: https://barberbase.in/q/status?t=eyJ...
         (WhatsApp in-app browser)
         Page: SSR render of current position → SSE connection established
 
Step 10: Watchdog detects 2 people ahead (or wait ≤ 20min):
         INSERT outbox_event: bb_near_turn
         UPDATE queue_entry: presence='notified', near_turn_notified_at=NOW()
         Bhejna sends: "⚡ Almost Your Turn! | [I'm On My Way 🏃] [Check Status]"
 
Step 11: Customer taps [I'm On My Way 🏃]
         Bhejna webhook: button_payload="ON_THE_WAY:{entry_id}"
         Webhook worker → UPDATE presence='on_the_way' → SSE ping
         Magic link page updates: shows PIN entry form
 
Step 12: Customer arrives. Reads PIN "4729" on counter card.
         Opens magic link (already on phone) → enters PIN
         POST /v1/queue/confirm-arrival { method:"pin", pin:"4729" }
         Rate limit check → bcrypt verify → presence='arrived', is_dispatchable=true
         SSE ping → staff dashboard shows ✅ ARRIVED indicator
 
Step 13: Barber taps "Call Next" on dashboard
         POST /v1/staff/queue/call-next (StaffJWT)
         BEGIN
           Lock queue_session FOR UPDATE
           Find: state='waiting', is_dispatchable=true, presence='arrived', oldest
           state → 'called', called_at=NOW(), assigned_barber_id=barber
           staff.status → 'cutting'
           INSERT outbox_event: bb_you_are_next
           queue_version++
         COMMIT → SSE broadcast
         Sends: "🔔 It's Your Turn! Token #18 [I'm Here — Check In]"
 
Step 14: Customer's magic link page updates: "🔔 It's Your Turn!"
 
Step 15: Barber taps "Direct Start" (customer already seated)
         POST /v1/staff/queue/entries/{id}/start (StaffJWT)
         Guard: presence MUST be 'arrived'
         called_at=NOW(), started_at=NOW(), state → 'in_progress' (atomic)
 
Step 16: Barber taps "Complete" after service, enters payment
         POST /v1/staff/queue/entries/{id}/complete
         14-step CompleteVisitAndCheckout transaction
         (see 05_queue_locking_transactions.md)
 
Step 17: 30 minutes later: feedback outbox fires
         Bhejna sends bb_service_feedback:
         "⭐ How Was Your Experience? [⭐⭐⭐⭐⭐ Excellent] [⭐⭐⭐ Average] [⭐ Poor]"
         Customer taps → webhook worker inserts feedback_response
```
 
---
 
## checkin_intent Lifecycle
 
The `token_code` (e.g. "JN8K4P") acts as an out-of-band handshake between the web page and the WhatsApp message.
 
```
Created:   POST /v1/public/locations/{id}/checkin-intents
           status='created', expires 23 hours (GENERATED ALWAYS AS)
 
Resolved:  Webhook worker processes the WhatsApp JOIN message
           status='resolved', resolved_queue_entry_id set
 
Expired:   expires_at passed and still status='created'
           Attempted resolution → 422 "Link expired. Please rejoin."
 
Rejected:  Shop closed at resolution time
           status='rejected', no queue_entry created
```
 
---
 
## In-App Browser (WhatsApp Webview) — Critical Constraints
 
### What Works
- Standard HTML, CSS, JavaScript
- SvelteKit SSR-rendered pages
- `fetch()` API
- EventSource (SSE) — how magic link page stays live
- Geolocation API (with permission prompt)
- HTTPS only (Cloudflare provides)
### What Does NOT Work
- Camera / microphone (blocked)
- Service Workers (unreliable — do not use)
- Web Push Notifications (blocked)
- Clipboard write API (unreliable)
- Bluetooth / NFC Web APIs
**Scope of these constraints:** This list applies ONLY to the WhatsApp in-app browser webview, which loads all customer-facing pages (`/q/status`, `/q/appointment`, public shop pages). It does NOT apply to `/dashboard`, which staff open in a native browser (Chrome, Samsung Internet, Safari). Service Workers and Web Push are valid and supported on `/dashboard` with StaffJWT auth. See `16_web_push_service_worker.md` and Law 17.
 
### Isolated Cookie Jar
**WhatsApp in-app browser has its own isolated cookie store.** Cookies from the main browser (Chrome/Safari) are NOT present. This is why magic link tokens are in the URL query param (`?t=token`) and never in cookies.
 
### Design for Resumability
Every page must be fully functional from the URL alone. No state that only lives in JavaScript memory. If the webview closes and the customer taps the button again, they get the exact same page.
 
### Performance Constraint
Magic link page: under 50KB of JavaScript. Must load in under 3 seconds on 3G. Use SvelteKit SSR for initial render.
 
---
 
## Magic Link Page States (`barberbase.in/q/status?t=...`)
 
Rendered based on `queue_entry.state` + `queue_entry.presence_state`:
 
| State | What Customer Sees |
|---|---|
| `presence=remote` or `presence=notified` | Token #18 \| 5 ahead \| ~40 min \| Services + price \| [I'm On My Way →] |
| `presence=on_the_way` | Token #18 \| 3 ahead \| ~25 min \| 4-digit PIN input \| [Confirm Arrival] \| [Use My Location] |
| `presence=arrived` | Token #18 \| 2 ahead \| ~15 min \| ✅ You're confirmed! Wait inside. |
| `state=called` | 🔔 It's Your Turn! — Token #18 \| Please go to the barber chair now. |
| `state=in_progress` | ✂️ Service In Progress — Token #18 \| Enjoy your service! |
| `state=completed` | 🎉 All Done! Thanks for visiting Star Salon. \| [Book Again →] |
| `presence=snoozed` | ⏸ Spot Paused — Token #18 \| Turn passed. Ask staff to reactivate. \| [Call Shop] (tel: link) |
 
SSE keeps page live. On `queue_changed` event: compare version → debounce 500ms → `GET /v1/queue/my-status`.
