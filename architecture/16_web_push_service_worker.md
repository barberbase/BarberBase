# Purpose
Owns the complete Staff-only Web Push and Service Worker architecture for BarberBase.
Documents VAPID key lifecycle, Push Action Token (PAT) design and rationale, push
subscription lifecycle, Service Worker scope rules, notification payload schema, outbox
dispatch flow, battery optimization rules, Android/iOS behavioral matrix, and queue
integrity invariants for background push actions.
 
# Use This File When
- Implementing POST /v1/staff/push/subscribe
- Implementing POST /v1/staff/push/call-next
- Implementing the Service Worker (src/service-worker.js)
- Implementing the outbox push dispatch handler (internal/outbox/handlers/push_notification.go)
- Implementing VAPID signing and PAT generation/verification (internal/push/vapid.go)
- Debugging a push notification that did not appear or did not advance the queue
- Reviewing security of the PushActionToken
- Adding any future push action endpoint
# Do Not Use This File For
- Queue mutation logic (→ 05_queue_locking_transactions.md)
- WhatsApp notification templates (→ 09_notifications_templates.md)
- SSE manager or SSE client behavior (→ 08_sse_realtime.md)
- Staff JWT or BFF cookie mechanics (→ 03_auth_identity.md)
- Customer-facing pages or flows (→ 10_customer_journey.md)
- Flutter, native apps, or non-web delivery channels
# Related Files
- `001_complete_schema.sql` — staff_members push columns; notification_events channel CHECK
- `openapi.yaml` — POST /v1/staff/push/subscribe; POST /v1/staff/push/call-next; PushActionToken security scheme
- `03_auth_identity.md` — PushActionToken auth tier; HMAC_SECRET usage pattern
- `05_queue_locking_transactions.md` — CompleteVisitAndCheckout step 12.5 (push trigger)
- `07_webhooks_outbox_workers.md` — web_push.send outbox event type; dispatch handler
- `12_staff_dashboard_frontend.md` — PWA manifest; Service Worker registration; push UX
- `13_infra_env_deployment.md` — VAPID env vars; webpush-go dependency; directory structure
- `15_critical_laws.md` — Laws 17, 18, 19, 20, 21
# Source Of Truth Priority
`001_complete_schema.sql` for schema. `openapi.yaml` for endpoint contracts.
This file for push infrastructure design, PAT rationale, and Service Worker rules.
 
---
 
## Business Goal
 
Haircut finished → barber pulls notification shade → presses NEXT CLIENT → queue advances → continues working.
 
The notification is a lightweight operational control surface for authenticated staff only.
This feature is not for customers, not for WhatsApp flows, not for public pages.
 
---
 
## Architectural Position
 
Push is an enhancement layer on top of the existing architecture. The existing system:
- SSE delivers queue changes to the open dashboard tab (foreground)
- REST endpoints handle all queue mutations
Push adds: queue state delivery to the locked phone (background). Nothing else changes.
 
If push is removed tomorrow, every queue operation continues to work via SSE + REST.
This is Law 21. Verify it before shipping.
 
---
 
## Why PushActionToken Is Required (Not StaffJWT)
 
The StaffJWT is an httpOnly cookie managed by the SvelteKit BFF. The BFF reads it
server-side in the `+page.server.ts` load function and passes the JWT string to the
dashboard as page data. This is why `?token={StaffJWT}` appears in SSE connections —
the JWT string IS accessible to dashboard JavaScript via the Svelte store, not via
`document.cookie`.
 
Despite this, the StaffJWT cannot be used from the Service Worker background context
for one deterministic reason:
 
**TTL:** StaffJWT access token TTL is 15 minutes. A haircut runs 20–45 minutes. The
barber starts a haircut, locks their phone, and puts it in their pocket. When the phone
is locked, the browser tab is backgrounded. On mobile browsers, backgrounded tab
JavaScript is suspended — SvelteKit's auto-refresh hooks do not run. When the barber
finishes and taps the notification 25 minutes later, the JWT in the Svelte store is
expired. The call returns 401 silently. Queue does not advance. The barber sees nothing.
This is a guaranteed failure for the primary use case.
 
The Service Worker cannot refresh the JWT because:
1. The refresh token is httpOnly — inaccessible to SW context
2. The refresh endpoint is on `api.barberbase.in` (cross-origin from the `barberbase.in`
   frontend served by Cloudflare Pages)
3. The Go refresh endpoint issues the new token as a cookie response, not a JSON body
Adding a BFF proxy endpoint to return the raw JWT would change the auth model, expose
a 30-day credential through a new surface, and add a new endpoint that does not exist
in the documented architecture. The PAT is simpler, more secure, and uses existing
infrastructure.
 
---
 
## Push Action Token (PAT)
 
### Format
 
```
payload = base64url("{staff_member_id}:{location_id}:call_next:{unix_expires}")
mac     = base64url(HMAC-SHA256(payload, HMAC_SECRET))
PAT     = payload + "." + mac
```
 
Two base64url segments joined by ".": the plaintext claims and their HMAC-SHA256
signature. Uses the existing `HMAC_SECRET` — same secret as CustomerSession magic
links (`03_auth_identity.md`). No new secret. No table. Stateless.
 
### Generation
 
Generated by the outbox push dispatch handler (`internal/outbox/handlers/push_notification.go`)
at the moment the push is sent, not at transaction time. One PAT is generated per
staff member per push dispatch. TTL: 4 hours from generation time.
 
The PAT is embedded in the encrypted push notification payload alongside queue state data.
 
### Verification at POST /v1/staff/push/call-next
 
```
1. Extract raw token from X-Push-Action-Token header
2. Split on "." → segments[0] = payload_b64, segments[1] = mac_b64
   Reject 401 if not exactly two segments
3. Recompute HMAC-SHA256(segments[0], HMAC_SECRET)
   constant-time compare against base64url-decode(segments[1])
   Reject 401 if mismatch
4. base64url-decode segments[0] → parse "{staff_member_id}:{location_id}:call_next:{unix_expires}"
5. Validate command == "call_next" — reject 403 if not (Law 20)
6. Validate unix_expires > now() — reject 401 if expired
7. Rate limit: golang.org/x/time/rate, max 1/3s per staff_member_id
8. SELECT staff_members WHERE id=$1 AND is_active=true → get tenant_id (1 DB read)
9. Proceed to call-next domain function (same as POST /v1/staff/queue/call-next)
```
 
### Why No Single-Use Table
 
Single-use enforcement requires one table write per barber action tap. The queue's own
`FOR UPDATE` lock on `queue_sessions` (Law 1) already prevents double-advance:
- Two concurrent calls are serialized by the lock
- The second call finds either no dispatchable entry (404) or calls the next customer
  in line (correct queue behavior)
The rate limit (1/3s per `staff_member_id`) prevents rapid-fire replay from a spotty
double-tap. Adding a table write for a property already guaranteed by the queue model
violates the resource efficiency principle. No table.
 
### PAT TTL
 
4 hours. A barber who does not respond to a notification for 4+ hours has either ended
their shift or the queue state is too stale to be useful. On 401, the Service Worker
MUST update the notification — see Error Handling Contract.
 
### Command Scoping (Law 20)
 
The literal `"call_next"` is in the HMAC input. Future push commands must use their
own literals. Cross-command use is rejected with 403. The domain layer never validates
the PAT — it only receives pre-verified `staff_member_id` + `location_id`.
 
---
 
## VAPID Key Management
 
### Keys
 
```
VAPID_PUBLIC_KEY   # base64url-encoded EC P-256 public key
                   # Delivered to frontend via PUBLIC_VAPID_PUBLIC_KEY SvelteKit env var
                   # Passed to: pushManager.subscribe({applicationServerKey: ...})
 
VAPID_PRIVATE_KEY  # base64url-encoded EC P-256 private key
                   # Never sent to client. Used by Go (webpush-go) to sign push requests.
 
VAPID_SUBJECT      # Contact URI — e.g. mailto:ops@barberbase.in
                   # Required by Web Push spec for VAPID identification
```
 
### Frontend Delivery
 
The VAPID public key is exposed as `PUBLIC_VAPID_PUBLIC_KEY` in Cloudflare Pages
environment settings. Accessible in SvelteKit as `import.meta.env.PUBLIC_VAPID_PUBLIC_KEY`.
No API endpoint required. Baked into the Cloudflare Pages deployment.
 
Rotation: VAPID keys are long-lived. Rotate only if the private key is compromised.
Rotation requires a Cloudflare Pages redeployment to update the public key, and all
existing push subscriptions become stale (staff must re-subscribe). Document a rotation
runbook before going to production.
 
### Key Generation (one-time)
 
Use `webpush.GenerateVAPIDKeys()` from `github.com/SherClockHolmes/webpush-go`.
Store both in env immediately. Never commit to source control or logs.
 
---
 
## Push Subscription Lifecycle
 
### One Subscription Per Staff Member (Latest Wins)
 
**Rule:** One active push subscription per `staff_members` row. When a barber registers
or re-registers a push subscription, the new subscription unconditionally overwrites the
existing columns. There is no multi-device support. There is no subscription accumulation.
 
This is correct for the target market: 3–5 chair AC salons where each `barber` account
is operated from one personal phone. Shared shop iPads log in as `owner` or `manager`
accounts, not individual barber accounts.
 
If multi-device support becomes a genuine product requirement, migrate the push columns
to a separate `staff_push_subscriptions` table at that time with field evidence.
 
### Registration Flow
 
```
1. Dashboard loads, StaffJWT confirmed
2. On second or later session (not on first load — premature prompts are rejected)
3. Check Notification.permission
   → 'denied': skip entirely, fall back to SSE-only (no error, no retry)
   → 'default': show permission prompt
   → 'granted': proceed to subscribe
4. pushManager.subscribe({
     userVisibleOnly: true,
     applicationServerKey: urlBase64ToUint8Array(import.meta.env.PUBLIC_VAPID_PUBLIC_KEY)
   })
5. POST /v1/staff/push/subscribe with W3C PushSubscription JSON body
6. Backend: UPDATE staff_members SET
     push_endpoint=$1, push_p256dh=$2, push_auth=$3, push_enabled=true
     WHERE id={from_jwt_staff_member_id}
7. Response: 204 No Content
```
 
### Re-subscription
 
Same flow. Same endpoint. The UPSERT (UPDATE staff_members SET ...) overwrites previous
columns. Old FCM endpoint becomes stale. On next push send attempt to the old endpoint,
FCM returns 410 Gone — cleaned up automatically by the outbox dispatch handler.
 
### 410 Gone Handling
 
When FCM returns 410 Gone for a push endpoint, the subscription is expired. The outbox
push dispatch handler MUST:
 
```
1. UPDATE staff_members
   SET push_enabled=false, push_endpoint=NULL, push_p256dh=NULL, push_auth=NULL
   WHERE id=$1
2. INSERT notification_events (channel='web_push', status='failed', error='410_gone')
3. Continue to next staff member in the dispatch loop
```
 
**Order matters:** Disable the subscription BEFORE logging. If the process crashes
between these steps, disabling first means no wasted FCM calls on retry. Logging first
means stale sends continue until the next 410.
 
---
 
## Notification Payload Schema
 
JSON payload embedded in the encrypted push message (encrypted using p256dh + auth keys):
 
```json
{
  "waiting_arrived_count": 3,
  "next_token_number": 19,
  "estimated_wait_minutes": 25,
  "pat": "<base64url-signed-PAT>",
  "api_url": "https://api.barberbase.in/v1"
}
```
 
`api_url` is hardcoded by Go at dispatch time. The Service Worker cannot read env vars.
 
### Notification Display Contract
 
```
title:              "BarberBase Queue"
body:               "Waiting: {waiting_arrived_count} | Est. {estimated_wait_minutes} min"
tag:                "barberbase-desk-queue"     ← MUST be identical on every push
requireInteraction: true                        ← MUST be true — persists until acted on
silent:             true                        ← MUST be true — no audio, no Bluetooth
badge:              "/favicon.png"
icon:               "/favicon.png"
actions:
  - action: "dispatch_next"
    title:  "NEXT CLIENT"
  - action: "open_dashboard"
    title:  "Open Dashboard"
data:
  pat:     <PAT string>
  api_url: <api_url string>
```
 
**MUST NOT** use MediaSession API. **MUST NOT** play audio. **MUST NOT** register
background audio. This would pause or interrupt music on Bluetooth speakers.
 
### Inline Notification Update
 
All push events use the same `tag: "barberbase-desk-queue"`. The OS replaces the
existing notification with the new content rather than stacking a new one. This is
intentional: no notification stacking, no multiple push sounds, no cluttered shade.
 
---
 
## Service Worker Scope Rules (Law 17)
 
```javascript
// Registration must be gated — both conditions required
if (window.location.pathname.startsWith('/dashboard') && staffAuthenticated) {
  navigator.serviceWorker.register('/service-worker.js', { scope: '/dashboard/' })
}
```
 
### Pages where registration is FORBIDDEN
 
| Route | Reason |
|---|---|
| `/q/status` | WhatsApp in-app browser — SW unreliable (Law 17, `10_customer_journey.md`) |
| `/q/appointment` | Same webview constraint |
| `/{tenant_slug}/{location_slug}` | Public, unauthenticated, customer context |
| `/login` | No StaffJWT yet |
| `/admin`, `/admin/analytics` | Owner/manager pages — push not needed |
 
### PWA Manifest
 
`/static/dashboard/manifest.json`
 
```json
{
  "name": "BarberBase Queue",
  "short_name": "BarberBase",
  "start_url": "/dashboard",
  "scope": "/dashboard/",
  "display": "standalone",
  "background_color": "#ffffff",
  "theme_color": "#000000",
  "icons": [{ "src": "/favicon.png", "sizes": "192x192", "type": "image/png" }]
}
```
 
`scope: "/dashboard/"` ensures the Service Worker only intercepts requests within
`/dashboard/*`. Customer page requests are never intercepted.
 
---
 
## Service Worker Error Handling Contract
 
On every notification action tap, the Service Worker MUST update the notification
regardless of the response. Never leave a stale notification unchanged after a tap.
 
| Response | Notification update |
|---|---|
| 200 OK | Re-show with updated `{next_token_number, waiting_arrived_count, est_wait}` |
| 404 Not Found | Re-show: title "Queue Clear", body "No customers ready", remove action buttons |
| 401 Unauthorized | Re-show: body "Session expired — open dashboard", replace actions with "Open Dashboard" only |
| 429 Too Many Requests | Do not update notification. Rate limit is 3s. Barber double-tapped. Ignore. |
| Network failure | Re-show notification unchanged. Add "Tap to retry" if possible. |
 
The 401 case is critical. A stale notification after a tap implies the action succeeded
when it did not. The barber moves on to the next customer unaware the queue did not advance.
 
---
 
## Outbox Dispatch Flow (web_push.send)
 
Triggered by `INSERT outbox_events (type='web_push.send')` in `CompleteVisitAndCheckout`
step 12.5 (see `05_queue_locking_transactions.md`).
 
Handler in `internal/outbox/handlers/push_notification.go`:
 
```
1. Extract location_id, tenant_id from outbox_event.payload
 
2. FREQUENCY GATE (Law 19):
   SELECT COUNT(*) FROM queue_entries
     WHERE queue_session_id = (today's session for location_id)
     AND state = 'waiting'
     AND is_dispatchable = true
     AND presence_state = 'arrived'
   → If 0: UPDATE outbox_event.status='dispatched'. Return. No push sent.
 
3. SELECT * FROM staff_members
   WHERE location_id=$1 AND push_enabled=true AND is_active=true
 
4. For each staff member:
   a. Generate PAT (4h TTL)
   b. Build payload JSON (queue state + PAT + api_url)
   c. Encrypt using staff_member.push_p256dh + staff_member.push_auth (webpush-go)
   d. VAPID sign with VAPID_PRIVATE_KEY, Urgency: high
   e. POST to staff_member.push_endpoint
   f. On 410 Gone:
      UPDATE staff_members SET push_enabled=false, push_endpoint=NULL,
        push_p256dh=NULL, push_auth=NULL WHERE id=$1
   g. On 2xx:
      INSERT notification_events (
        channel='web_push',
        notification_type='push_call_next',
        tenant_id, location_id,
        customer_id=NULL,           ← push is for staff, not customer
        recipient_phone=NULL,
        source_type='staff_member',
        source_id=staff_member.id,
        status='sent'
      )
 
5. UPDATE outbox_event.status='dispatched'
```
 
### FCM High-Priority Requirement
 
The `webpush-go` library must be configured with `Urgency: high` (maps to FCM
`priority: high`). Without this, Android Doze mode batches delivery and may delay
it by minutes. A delayed NEXT CLIENT notification is useless in the operational flow.
 
### Quota Bypass
 
`web_push.send` events BYPASS the Bhejna quota system entirely. There is no Bhejna
call. There are no WhatsApp quota buckets. Skip the quota check (outbox worker step 4–5
from `07_webhooks_outbox_workers.md`) entirely for this event type. Route directly
to the push dispatch handler.
 
### Delivery Receipt Limitation
 
FCM does not provide delivery receipts to the sender. `notification_events.status` for
`web_push` rows will only ever reach `'sent'`. It will never reach `'delivered'`. This
is a known, permanent limitation. Do not add polling or callback infrastructure.
 
---
 
## Android vs iOS Behavioral Matrix
 
| Factor | Android Chrome / Samsung Internet | iOS Safari (PWA only) |
|---|---|---|
| Push support | Full. FCM. No install required. | Requires Add to Home Screen (Safari 16.4+) |
| `requireInteraction: true` | Honored — notification persists until dismissed | Not fully honored — slides to Notification Center within minutes |
| Action buttons | Full support | Limited support in 16.4+ |
| Background fetch on tap | Reliable (FCM high-priority wakeup) | May be throttled in Low Power Mode |
| Platform coverage in target market | ~95% | ~5% |
| V1 engineering required | Full feature | Zero iOS-specific code |
 
### iOS Support Policy
 
**iOS is supported with graceful degradation.** No iOS-specific code is written.
Standard Web Push API code produces Android Tier 1 behavior automatically. iOS produces
a degraded-but-functional experience.
 
**Tier 1 — Android Chrome / Samsung Internet (Full Support):**
Lock screen → pull notification shade → NEXT CLIENT persists → tap → background fetch →
queue advances → phone stays locked. This is the target experience.
 
**Tier 2 — iOS Safari PWA (Best-Effort):**
Requires "Add to Home Screen" installation. Notification appears but fades from lock
screen into Notification Center within minutes. Action button tap may briefly surface
Safari before executing. The barber can still tap from Notification Center and the
queue advances. Not the target experience, but functional.
 
**Acceptable iOS failures:** notification fades quickly; background fetch opens Safari
briefly; Add to Home Screen required.
 
**Unacceptable iOS failures (requires investigation, not acceptance):** push never
delivers after correct PWA installation; action button produces 401 with no visual
feedback (the SW error handling contract must apply on iOS identically to Android).
 
---
 
## Queue Integrity Invariants for Background Push Actions
 
### Concurrent calls from multiple devices are safe
 
If a barber has the dashboard open on a tablet AND taps the notification on their phone
simultaneously, two `POST /v1/staff/push/call-next` calls are in-flight. Both hit
`SELECT queue_sessions FOR UPDATE` (Law 1). The first acquires the lock, advances the
queue, commits. The second acquires the lock and finds either no more arrived entries
(returns 404) or calls the next customer (correct behavior). No corruption. This is
not a special case — it is the same concurrency behavior as two dashboard taps.
 
### Stale notification payload is safe
 
The notification shows queue state at dispatch time ("Waiting: 3"). By the time the
barber taps 10 minutes later, the actual state may differ. The push call-next handler
executes against live PostgreSQL state, not the notification payload. It calls whoever
is actually next at execution time. The payload is display context only.
 
### What must not change
 
- The call-next domain function is the single authoritative path for advancing the queue.
  `POST /v1/staff/push/call-next` is a new auth gate in front of the same function.
  No queue mutation logic lives in the push handler.
- Law 1 (`FOR UPDATE` on `queue_sessions`) applies to every call-next invocation
  regardless of trigger source — dashboard tap, push action, or any future caller.
- Law 7 (outbox inside transaction) applies: the `web_push.send` outbox event is
  inserted inside the `CompleteVisitAndCheckout` transaction.
- Law 8 (SSE after COMMIT) applies: SSE broadcast still fires after checkout commit.
  Push is additive — it does not replace SSE.
---
 
## Project Directory Additions
 
New files following structure from `13_infra_env_deployment.md`:
 
```
barberbase-core/
└── internal/
    ├── api/
    │   └── handlers_push.go          ← POST /subscribe and POST /call-next handlers
    ├── push/
    │   └── vapid.go                  ← VAPID signing; PAT generation; PAT verification
    └── outbox/handlers/
        └── push_notification.go      ← web_push.send dispatch handler
 
barberbase-frontend/ (SvelteKit)
├── src/
│   └── service-worker.js             ← push + notificationclick handlers
└── static/dashboard/
    └── manifest.json                 ← PWA manifest, scope: /dashboard/
```
 
---
 
## What Must Not Change
 
1. Service Worker scope MUST remain `/dashboard/`. Never extend to root scope.
2. PAT MUST use `HMAC_SECRET`. Never add a push-specific secret.
3. Push notifications MUST be `silent: true`. Never add audio.
4. MediaSession API MUST NOT be used. Never.
5. The push handler MUST call the same call-next domain function as the dashboard.
   Never bypass the queue lock for push actions.
6. Push subscription columns live on `staff_members`. One per staff member (latest wins).
   Do not create a subscriptions table without field evidence of multi-device need.
7. The confirm-arrival push trigger is explicitly DEFERRED to Phase 1.5.
   Do not add without field evidence of idle barbers missing customers with phones locked.
8. Tab-active suppression (SSE connection tracking) is explicitly DEFERRED to Phase 1.5.
   Do not add without field evidence of double-notification complaints from barbers.
