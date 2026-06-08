
raw
# Purpose
Documents all SvelteKit frontend pages, staff dashboard UX constraints, and the public shop page flow. Covers what each page does, what APIs it calls, and critical design constraints.
 
# Use This File When
- Implementing any SvelteKit page
- Designing staff dashboard interactions
- Implementing the public shop landing page
- Checking what a frontend page should display and when
# Do Not Use This File For
- Backend API implementation (→ `openapi.yaml`)
- In-app browser constraints (→ `10_customer_journey.md`)
- Magic link page states (→ `10_customer_journey.md`)
- Auth token mechanics (→ `03_auth_identity.md`)
# Related Files
- `10_customer_journey.md` — magic link page states, in-app browser
- `04_queue_state_machine.md` — what states to render
- `08_sse_realtime.md` — SSE client behavior
- `openapi.yaml` — all API contracts
# Source Of Truth Priority
`openapi.yaml` for API shapes. This file for frontend UX rules.
 
---
 
## Pages Overview
 
| Route | Page | Auth | Purpose |
|---|---|---|---|
| `/{tenant_slug}/{location_slug}` | Public shop landing | None | Service selector, queue join |
| `/q/status?t={token}` | Magic link queue status | CustomerSession | Live queue position, arrival PIN |
| `/q/appointment?t={token}` | Appointment details | CustomerSession | View/cancel appointment |
| `/login` | Staff login | None | WhatsApp OTP login |
| `/dashboard` | Staff queue dashboard | StaffJWT | Live queue management |
| `/admin` | Owner admin | StaffJWT (owner/manager) | Services, staff, settings |
| `/admin/analytics` | Analytics | StaffJWT (owner/manager) | Daily/weekly reports |
 
---
 
## Public Shop Landing Page
 
URL: `https://barberbase.in/{tenant_slug}/{location_slug}`
 
**Stateless.** No token. Customer starts fresh.
 
### What it shows
- Shop name, operating hours, current status (open/closed/closing_soon)
- Current queue length and estimated wait
- Service catalog (tabs: Hair / Beard / Skin; popular variants highlighted)
- "Join via WhatsApp" CTA (primary) when walk-in is allowed
### Flow
```
1. Load: GET /v1/public/locations/{id}/info (queue length, status, operating hours)
2. Customer selects services from catalog
3. POST /v1/public/locations/{id}/booking-options
   Returns: allowed_modes, total duration, total price, estimated wait
4. If walk_in allowed: show [Join via WhatsApp] button
   Tap → POST /v1/public/locations/{id}/checkin-intents
          Returns: token_code, deep_link
   Page shows: "WhatsApp will open. Press Send." + deep_link button
5. If walk_in blocked but appointment allowed: show appointment booking UI (Phase 1.5)
6. If all blocked: show blocked_reason message
```
 
### Design Constraints
- Must render server-side (SvelteKit SSR) for fast initial paint
- Must work on 3G (Cloudflare edge delivery)
- No state in JavaScript memory that isn't recoverable from the URL
---
 
## Staff Dashboard
 
URL: `https://barberbase.in/dashboard`
Auth: StaffJWT (httpOnly cookie, managed by SvelteKit BFF)
 
### Critical UX Constraint: Low-Staff-Training Design
- **Big tap targets.** Primary actions must be reachable with one thumb.
- **Single-tap actions.** Call Next, Direct Start, Mark No-Show — one tap, no confirmation modal for primary flow.
- **Checkout modal is the exception** — requires payment entry, unavoidably multi-step.
### Queue Display Order
```
in_progress entries first (currently being served)
called entries (customer was called, not yet seated)
waiting entries — by priority_group ASC, sort_key ASC
  priority_group 50 = appointments (higher priority)
  priority_group 100 = walk-ins/WhatsApp joins (default)
```
 
### Staff Actions Per Entry
 
| State | Available Actions |
|---|---|
| `waiting`, `presence=arrived` | Direct Start, Skip, Cancel, (Call Next finds this automatically) |
| `waiting`, `presence=remote/notified/on_the_way` | Skip, Cancel, Mark Arrived (staff verification) |
| `called` | Start Service, Mark No-Show, Skip Back |
| `in_progress` | Complete (opens checkout modal) |
| `skipped` | Reactivate |
 
### Presence Indicator (Visual)
 
| presence_state | Indicator |
|---|---|
| `remote` | 🌐 Remote |
| `notified` | 📨 Notified |
| `on_the_way` | 🏃 On the Way |
| `arrived` | ✅ Arrived |
| `snoozed` | ⏸ Snoozed |
| `unknown` | — Walk-in |
 
### SSE on Dashboard
- Persistent connection. Reconnect immediately on disconnect.
- On `queue_changed`: compare version → if stale → `GET /v1/staff/queue/snapshot`
- On reconnect: `GET /v1/staff/queue/snapshot` immediately
- Dashboard load: `GET /v1/staff/queue/snapshot` once, then SSE-driven
### Stale Warning Display
`stale_warning` field on `QueueEntryStaff`:
- `'called_warning'` → yellow highlight on entry
- `'called_critical'` → red highlight, visual urgency
- `'in_progress_warning'` → yellow
- `'in_progress_critical'` → red
### Checkout Modal
Opened by: staff taps "Complete" on an `in_progress` entry.
 
Contains:
- Service line items (from `visit_services` — immutable snapshot)
- Product add-ons (optional, ad-hoc at checkout)
- Discount amount + reason (optional)
- Payment methods: Cash / Card / UPI (one or multiple)
- Total validation: SUM(payment_lines) == subtotal - discount
On submit: `POST /v1/staff/queue/entries/{id}/complete` with `CheckoutRequest` body.
 
---
 
## Staff PWA — Web Push Notification Console
 
The dashboard is a scoped Progressive Web App. Push notifications give the barber a
lock-screen NEXT CLIENT control so they can advance the queue without unlocking the phone
or opening the browser. Full design: `16_web_push_service_worker.md`.
 
### PWA Manifest
- File: `static/dashboard/manifest.json`
- `scope: "/dashboard/"`, `start_url: "/dashboard"`, `display: "standalone"`
- Android Chrome shows "Add to Home Screen" automatically when manifest + HTTPS are present.
### Service Worker Registration
- File: `src/service-worker.js`, registered with `scope: '/dashboard/'`
- Registration is gated: only runs when `window.location.pathname.startsWith('/dashboard')`
  AND StaffJWT is confirmed (Law 17).
- Never registered on customer-facing routes (WhatsApp webview — see `10_customer_journey.md`).
### Push Permission Prompt
- Shown on the second or later dashboard session, after StaffJWT is confirmed — never on
  first load.
- VAPID public key for `pushManager.subscribe()` comes from `import.meta.env.PUBLIC_VAPID_PUBLIC_KEY`
  (Cloudflare Pages env var), not from an API call.
- On grant: `POST /v1/staff/push/subscribe` with the W3C PushSubscription JSON.
- On deny: silent fallback to SSE-only. No error state. Push is a convenience layer (Law 21).
### Notification Action Button
- The lock-screen notification carries `[ NEXT CLIENT ]` and `[ Open Dashboard ]`.
- NEXT CLIENT fires a background `POST /v1/staff/push/call-next` from the Service Worker
  using a PushActionToken (not StaffJWT — see `03_auth_identity.md`).
- Notifications are `silent: true` and use a single shared `tag` for inline replacement —
  no audio, no Bluetooth interruption, no stacking.
### Platform Support
- Android Chrome / Samsung Internet: full support (lock-screen persistent console).
- iOS Safari: best-effort, requires Add to Home Screen; notification persistence degrades.
  No iOS-specific code. See `16_web_push_service_worker.md` for the support matrix.
---
 
## Owner Admin Panel
 
URL: `https://barberbase.in/admin`
Auth: StaffJWT with role `owner` or `manager`
 
### Sub-sections
 
**Services CRUD:** Manage service_categories → service_groups → service_variants.
- Set `is_popular`, `price_paise`, `duration_minutes`, booking rules per variant.
**Staff Management:** Add staff with phone number and role. Phone number is the login credential. No self-registration.
 
**Shop Status:** Manual open/close override, temporary closure with expiry.
 
**WhatsApp Settings:** Mode A (default) or Mode B (own number via JSON paste flow). See `06_bhejna_whatsapp.md`.
 
**Analytics:** Daily revenue, customer counts, avg wait, no-show rate. Powered by SQL aggregation only (no separate analytics DB).
 
---
 
## SvelteKit BFF Pattern
 
SvelteKit runs as SSR on Cloudflare Pages (or Node adapter for full BFF).
 
- Staff JWT access token: httpOnly cookie, set by BFF on login
- Refresh token: httpOnly cookie, set by BFF on login
- Auto-refresh: SvelteKit hooks intercept 401 → `POST /v1/auth/staff/refresh` → retry
- Customer tokens: URL query param only, not cookies (WhatsApp webview constraint)
---
 
## Cloudflare Turnstile
 
Used on public-facing forms (shop landing page join flow) to prevent bot abuse. Implemented at Cloudflare edge. Not in the Go backend.
