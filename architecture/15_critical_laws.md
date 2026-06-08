# Purpose
The 21 immutable laws of BarberBase. These rules must never be violated under any circumstances, in any component, in any phase.
 
# Use This File When
- Reviewing any implementation for correctness
- Designing a new feature
- Debugging a race condition or data integrity issue
# Do Not Use This File For
- Implementation details of how laws are enforced (→ relevant domain files)
# Related Files
- `05_queue_locking_transactions.md` — enforces Laws 1, 2, 7, 8, 10
- `06_bhejna_whatsapp.md` — enforces Laws 9, 15, 16
- `03_auth_identity.md` — enforces Laws 3, 11
- `02_architecture_constraints.md` — enforces all infrastructure constraints
- `16_web_push_service_worker.md` — enforces Laws 17, 18, 19, 20, 21
# Source Of Truth Priority
These laws take precedence over any single file. If any file suggests behavior that violates a law, the law wins.
 
---
 
<laws>
LAW 1:
Every queue mutation MUST lock `queue_sessions` FOR UPDATE first.
 
```sql
SELECT id, queue_version, last_token_number, status
FROM queue_sessions
WHERE location_id = $1 AND business_date = $2
FOR UPDATE;
```
 
No exceptions. Every `waiting→called`, `called→in_progress`, `in_progress→completed`, join, skip, cancel, reactivate — all start by acquiring this lock.
 
---
 
LAW 2:
`SKIP LOCKED` is ONLY for `webhook_events` and `outbox_events` workers.
 
Never use `SKIP LOCKED` for queue mutations. Queue mutations use plain `FOR UPDATE` (blocking). `lock_timeout=1s` is the circuit breaker.
 
---
 
LAW 3:
Phone number is the canonical customer identity.
 
All Bhejna sends use phone number. `bsuid` is supplementary — stored in `customer_identities` — but never the primary key. Always normalize to E.164 format before using.
 
---
 
LAW 4:
All money is in PAISE (integers).
 
100 paise = ₹1. Never use FLOAT for monetary values. Never store or return money as decimal. Use `INT` in SQL. Divide by 100 only at display time.
 
---
 
LAW 5:
All IDs are UUID v7.
 
No ULID. No auto-increment integer. No UUID v4 for primary keys. UUID v7 is timestamp-sortable and used everywhere.
 
---
 
LAW 6:
`arrived` presence cannot be self-declared.
 
Physical verification is always required. Accepted methods: PIN (bcrypt-verified, rate-limited), GPS (≤100m, accuracy ≤150m), or Staff tap (StaffJWT). A customer cannot set their own presence to `arrived` through any other mechanism.
 
---
 
LAW 7:
Outbox events are inserted inside transactions.
 
`INSERT outbox_events` must happen inside the same database transaction as the state change that triggers it. If the transaction rolls back, the outbox event is never inserted. This guarantees no orphan notifications.
 
---
 
LAW 8:
SSE broadcast happens AFTER COMMIT.
 
`manager.Broadcast(...)` is called after the transaction successfully commits. Never inside a transaction. Never before `COMMIT`. Broadcasting before commit can push stale state to clients.
 
---
 
LAW 9:
Return 200 to Bhejna before processing.
 
The webhook ingress handler must INSERT into `webhook_events` and return 200 immediately. Never process the webhook synchronously inside the HTTP handler. Never return 5xx to Bhejna. A 5xx response causes Bhejna to retry — creating a retry storm.
 
---
 
LAW 10:
Snapshots are immutable.
 
`visit_services` rows are written once at booking time and never modified. `visit_charge_line_items` rows are written once at checkout and never modified. Historical visit data must not change when shop prices change.
 
---
 
LAW 11:
`tenant_id` comes from JWT context only. Never from request body.
 
Go middleware extracts `tenant_id` from the verified JWT and injects it into `context.Context`. All repository queries use this context value. If a request body contains a `tenant_id` field, it is ignored for authorization purposes.
 
---
 
LAW 12:
`is_dispatchable` is the dispatch gate.
 
"Call Next" ONLY considers entries where `is_dispatchable = true AND state = 'waiting'`. Never bypass this filter. `is_dispatchable = false` when `presence = 'snoozed'` OR `state IN ('skipped', 'no_show', 'cancelled', 'expired', 'completed')`.
 
---
 
LAW 13:
`magic_link_expires_at = 23 hours`. Hard-coded.
 
The magic link token expires 23 hours after creation. This is within Bhejna's 24-hour free conversation window, with a 1-hour buffer. Do not make this configurable. Do not extend it beyond 23 hours.
 
---
 
LAW 14:
Weekly summary ships in Phase 1.
 
`bb_weekly_summary` is NOT a deferred feature. It is a core owner retention mechanism. The Sunday 10 PM cron and weekly summary outbox must be implemented in Phase 1.
 
---
 
LAW 15:
BarberBase is one Bhejna tenant for Mode A.
 
One platform API key (`BHEJNA_API_KEY`). One platform phone number (`BHEJNA_FROM_PHONE`). One webhook URL. Mode B shops have their own Bhejna api_keys stored encrypted in `locations.bhejna_api_key_encrypted`. There are no per-shop environment variables.
 
---
 
LAW 16:
Verify Bhejna's actual webhook signature header before implementing.
 
The assumed header is `X-Bhejna-Signature: sha256={hmac}`. This may differ from what Bhejna actually sends. Check the live Bhejna portal documentation before implementing HMAC verification. Implement what Bhejna actually sends. Do not assume either format.
 
---
 
LAW 17:
Service Worker scope is `/dashboard/` only. Never register on customer-facing pages.
 
`navigator.serviceWorker.register()` and `pushManager.subscribe()` must only execute from routes under `/dashboard` with a confirmed StaffJWT session. They must never be called from `/q/status`, `/q/appointment`, `/{tenant_slug}/{location_slug}`, `/login`, or any other non-dashboard route.
 
The WhatsApp in-app browser, which loads all customer-facing pages, has unreliable Service Worker support (documented in `10_customer_journey.md`). A Service Worker registered at root scope (`/`) intercepts fetch events from every page on the domain, breaking customer pages in the WhatsApp webview unpredictably.
 
The PWA manifest `scope` must be `/dashboard/`. The SvelteKit registration call must be gated on `window.location.pathname.startsWith('/dashboard')`.
 
See: `16_web_push_service_worker.md`
 
---
 
LAW 18:
Push notification actions must never carry a StaffJWT or refresh token in the payload.
 
The StaffJWT access token TTL is 15 minutes. A haircut runs 20–45 minutes. When the phone is locked mid-haircut, the browser tab is backgrounded and JavaScript is suspended. No token refresh can occur. A notification tapped 25 minutes later would produce a guaranteed 401.
 
The refresh token is a 30-day httpOnly credential that must never leave the BFF cookie context.
 
Push action authentication uses exclusively a PushActionToken (PAT):
```
payload = base64url("{staff_member_id}:{location_id}:call_next:{unix_expires}")
mac     = base64url(HMAC-SHA256(payload, HMAC_SECRET))
PAT     = payload + "." + mac
```
The PAT is two base64url-encoded segments joined by ".": the plaintext payload and its
HMAC-SHA256 signature. The verifier splits on ".", recomputes the MAC over the first
segment with constant-time comparison, then parses the claims from the verified payload.
4-hour TTL. Stateless. Uses existing `HMAC_SECRET`. No new table. No new secret.
 
See: `16_web_push_service_worker.md`, `03_auth_identity.md`
 
---
 
LAW 19:
Push notifications fire only when arrived dispatchable queue entries exist at dispatch time.
 
The outbox `web_push.send` handler MUST check:
```sql
SELECT COUNT(*) FROM queue_entries
WHERE queue_session_id = {today's session}
  AND state = 'waiting'
  AND is_dispatchable = true
  AND presence_state = 'arrived'
```
If count = 0: skip all push sends for this event, mark outbox dispatched, return.
 
`queue_version` increments on every mutation. Push must NOT fire on every `queue_version` increment. Only queue state that is actionable by the barber from a locked phone justifies an FCM wakeup on a device that must run for 12+ hours.
 
See: `16_web_push_service_worker.md`
 
---
 
LAW 20:
Push Action Tokens are command-scoped. A PAT for one command cannot authorize another.
 
The literal string `"call_next"` is embedded in the HMAC input at PAT generation time. The handler for `POST /v1/staff/push/call-next` verifies this literal before executing. Any future push command endpoint must generate its own PAT with its own command literal. Presenting a `call_next` PAT to a different push endpoint returns 403, not a generic auth failure.
 
The domain layer never sees or validates the PAT. It receives pre-verified `staff_member_id` and `location_id` from the handler, identical to how StaffJWT middleware works.
 
See: `16_web_push_service_worker.md`
 
---
 
LAW 21:
Push notifications are a convenience layer. Queue correctness is independent of push.
 
The BarberBase queue operates correctly with zero push infrastructure. If every staff member denies push permission, revokes it, or receives no push notifications for an entire shift:
- The queue state machine is unaffected
- SSE delivers all queue changes to the open dashboard tab
- All queue mutations succeed via existing REST endpoints
- No queue entry is stuck, lost, or double-called because push failed
Push delivery, FCM availability, Service Worker execution, and browser notification permissions are NEVER in the critical path for queue correctness. They exist only for barber convenience when the phone is locked.
 
Verification test: if push infrastructure is disabled entirely, the product must work identically for every core barbershop workflow.
 
See: `16_web_push_service_worker.md`
 
</laws>
