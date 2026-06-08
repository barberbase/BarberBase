# Purpose
Covers all authentication mechanisms (customer session, staff JWT/OTP, PushActionToken,
AI agent), customer identity resolution, shadow profiles, and tenant isolation rules.
 
# Use This File When
- Implementing or changing login, session, or token flows
- Implementing identity resolution from Bhejna webhooks
- Checking tenant_id scoping rules
- Working with magic link token generation or validation
- Implementing PushActionToken generation or verification
# Do Not Use This File For
- WhatsApp sending (→ `06_bhejna_whatsapp.md`)
- Queue mutations (→ `05_queue_locking_transactions.md`)
- API contracts (→ `openapi.yaml`)
- Push subscription lifecycle or Service Worker scope (→ `16_web_push_service_worker.md`)
# Related Files
- `openapi.yaml` — security schemes, `/auth/*` endpoints, PushActionToken scheme
- `001_complete_schema.sql` — `customers`, `customer_identities`, `staff_members` tables
- `10_customer_journey.md` — magic link page usage
- `15_critical_laws.md` — Law 3 (phone as canonical identity), Law 11 (tenant from JWT), Law 18 (PAT not JWT)
- `16_web_push_service_worker.md` — full PAT design rationale, dispatch flow, verification steps
# Source Of Truth Priority
`openapi.yaml` for API shape. `001_complete_schema.sql` for schema. Briefing for flow logic.
 
---
 
## Authentication Tiers
 
| Tier | Who | Mechanism | Scope |
|---|---|---|---|
| Public | Customers browsing | No auth. Cloudflare WAF + `x/time/rate` | Read-only public data |
| CustomerSession | Customer on magic link page | Signed HMAC token in `X-Session-Token` header | One queue_entry + location, 23h |
| StaffJWT | Staff on dashboard | Bearer JWT in `Authorization` header | tenant_id + location_id + role, 15 min |
| PushActionToken | Staff from notification action button | HMAC token in `X-Push-Action-Token` header | One staff member + location + command, 4h |
| BhejnaKey | Bhejna webhook ingress | `X-Bhejna-Key` or HMAC in `X-Bhejna-Signature` | Webhook endpoint only |
| AIAgent | Future AI agent | Bearer token, owner-issued | Deferred |
 
---
 
## Customer: No Account System
 
- Identity = phone number from Bhejna webhook (E.164 format)
- Session = signed magic link token in URL query param `?t=`
- Token is in the URL, never in cookies (WhatsApp in-app browser has isolated cookie jar)
- `X-Session-Token` header carries token in API requests from magic link page
### Magic Link Token
 
```
token = HMAC-SHA256(customer_id:location_id:visit_id:expires_at, HMAC_SECRET)
Base64url encoded. Expires 23 hours. Hard-coded.
```
 
- For queue status page: scoped to `visit_id`
- For appointment page: scoped to `appointment_id`
**23-hour expiry is hard-coded.** Within Bhejna's 24-hour free conversation window minus 1h buffer. See Law 13.
 
Token generation: Go generates full signed token. The portion after `?t=` is passed as the dynamic button suffix in Bhejna send payload.
 
---
 
## Staff: WhatsApp OTP → JWT
 
```
1. Staff opens barberbase.in/login, enters phone number
2. POST /v1/auth/staff/request-otp { phone_number }
   Rate limit: max 3 requests per phone per 10 minutes
3. Go calls Bhejna text API → sends bb_staff_otp template to staff's WhatsApp
4. Staff enters 6-digit OTP
5. POST /v1/auth/staff/verify-otp { phone_number, otp }
6. Go issues:
     Access JWT (15 min TTL)  — httpOnly cookie, managed by SvelteKit BFF
     Refresh token (30 days)  — httpOnly cookie
   JWT payload: { tenant_id, location_id, staff_member_id, role }
```
 
Auto-refresh via `POST /v1/auth/staff/refresh`. Staff re-authenticates ~monthly.
OTP: 6 digits from `crypto/rand`. Stored as bcrypt hash in `staff_otps`. Expires 5 min.
Cost: ~₹0.15/staff/month.
 
**Phone number IS the staff identifier.** Staff members are pre-created by owner with their phone number. Login with unknown phone → rejected. No self-registration.
 
### OTP Store and Verification
 
OTPs live in PostgreSQL (`staff_otps`), not in memory or SQLite. A 5-minute, monthly-frequency
code must survive process restarts and be shared across nodes — an OTP requested on one node
must verify on another without sticky sessions. Volume is trivial; marginal cost on the primary
DB is negligible. Schema: `001_complete_schema.sql`.
 
```
request-otp:
  1. Rate limit: max 3 per phone / 10 min (x/time/rate)
  2. DELETE FROM staff_otps WHERE phone_number = $1        -- self-cleaning; no sweep job
  3. INSERT staff_otps (phone_number, otp_hash=bcrypt(code), expires_at=NOW()+5min)
 
verify-otp (single transaction):
  1. SELECT * FROM staff_otps
       WHERE phone_number=$1 AND consumed_at IS NULL AND expires_at > NOW()
       ORDER BY created_at DESC LIMIT 1 FOR UPDATE
  2. not found / expired       → 401
  3. attempts >= 5             → 401 (locked out; require a new code)
  4. UPDATE attempts = attempts + 1
  5. bcrypt mismatch           → COMMIT, 401
  6. match                     → SET consumed_at = NOW(); COMMIT; issue JWT + refresh token
```
 
### Staff JWT in Dashboard JavaScript
 
The SvelteKit BFF reads the httpOnly JWT cookie in the `+page.server.ts` load function
server-side and passes the JWT string to the dashboard page as page data. The dashboard
JavaScript holds the JWT string in a Svelte store — not via `document.cookie`, but via
the SSR page data. This is how `?token={StaffJWT}` is constructed for SSE connections.
 
The JWT string is available to dashboard JavaScript. It is NOT available to the Service
Worker background context (see PushActionToken section below).
 
### Staff Roles
 
`owner` | `manager` | `barber` — encoded in JWT `role` claim.
 
---
 
## PushActionToken (PAT)
 
Used exclusively by `POST /v1/staff/push/call-next` — the endpoint called by the
Service Worker background thread when the barber taps the NEXT CLIENT notification action.
 
### Why StaffJWT Cannot Be Used Here
 
The StaffJWT is available to dashboard JavaScript via the Svelte store (see above).
However, it cannot be used from the Service Worker background context for one
deterministic reason:
 
**TTL is 15 minutes. A haircut runs 20–45 minutes.** When the barber locks their phone
mid-haircut, the browser tab is backgrounded and JavaScript execution is suspended on
mobile browsers. The Svelte store auto-refresh cannot run. When the barber taps the
notification from the lock screen 25 minutes later, the JWT is expired. The call returns
401 silently. This is a guaranteed failure for the primary use case, not a race condition.
 
The Service Worker cannot refresh the JWT because:
- The refresh token is httpOnly — inaccessible from SW `fetch()` context
- The refresh endpoint (`/auth/staff/refresh`) issues the new token as a cookie, not
  in the response body — the SW receives no usable credential from this call
### PAT Format
 
```
payload = base64url("{staff_member_id}:{location_id}:{command}:{unix_expires}")
mac     = base64url(HMAC-SHA256(payload, HMAC_SECRET))
PAT     = payload + "." + mac
```
 
Two base64url segments joined by ".": the plaintext claims and their signature.
Uses the same `HMAC_SECRET` as `CustomerSession` magic links. No new secret. No new
table. Stateless — the verifier never touches the database to authenticate the token.
 
### PAT Generation
 
Generated by the outbox push dispatch handler (`internal/outbox/handlers/push_notification.go`)
at push-send time, one per staff member per push event. TTL: 4 hours.
Embedded in the encrypted push notification payload.
 
**Generated by Go outbox worker — never by a client.**
 
### PAT Verification (at POST /v1/staff/push/call-next handler)
 
```
1. Extract raw token from X-Push-Action-Token header
2. Split on "." → segments[0] = payload_b64, segments[1] = mac_b64
   Reject 401 if not exactly two segments
3. Recompute HMAC-SHA256(segments[0], HMAC_SECRET)
   constant-time compare against base64url-decode(segments[1])
   Reject 401 if mismatch
4. base64url-decode segments[0] → parse "{staff_member_id}:{location_id}:{command}:{unix_expires}"
5. Validate command == "call_next" — reject 403 if not (Law 20)
6. Validate unix_expires > now() — reject 401 if expired
7. Rate limit: golang.org/x/time/rate, max 1/3s per staff_member_id
8. SELECT staff_members WHERE id=$1 AND is_active=true → get tenant_id
9. Proceed to call-next domain function
```
 
For full rationale (why no single-use table, TTL choice, command scoping) see:
`16_web_push_service_worker.md` — "Push Action Token (PAT)" section.
 
### PAT Security Properties
 
- **Forgery resistance:** HMAC-SHA256 with HMAC_SECRET. Computationally infeasible to
  forge without the secret.
- **Replay window:** 4 hours. Rate limited at 1/3s per staff_member_id. Queue's own
  FOR UPDATE lock prevents double-advance even on rapid replay.
- **Cross-command misuse prevention:** command literal in HMAC input. Law 20.
- **Credential isolation:** PAT never contains StaffJWT or refresh token. Law 18.
---
 
## Customer Identity Resolution
 
Executed on every `message.received` webhook from Bhejna:
 
```
1. Normalize: sender.phone_number → E.164 (+91XXXXXXXXXX)
2. SELECT * FROM customers WHERE tenant_id=$1 AND phone_number=$2
   (tenant_id is resolved from the message's referenced entity — see Tenant ID
   Resolution below — never from a slug prefix match)
3. If found → use existing customer_id
4. If not found:
   a. INSERT INTO customers (tenant_id, phone_number, name=display_name)
   b. IF sender.bsuid present AND non-null:
      INSERT INTO customer_identities
        (customer_id, provider='whatsapp', provider_id=sender.bsuid)
      ON CONFLICT DO NOTHING
5. Proceed with customer_id
```
 
### Shadow Profile Edge Case
 
When `phone_number` is null from Bhejna (masked number):
```
a. INSERT INTO customers (tenant_id, is_shadow_profile=true)
b. INSERT INTO customer_identities (provider='whatsapp', provider_id=bsuid)
c. Merge when phone confirmed within same checkin_intent session
```
 
### Tenant ID Resolution for Inbound Messages (Mode A)
 
Mode A shares one platform number, so tenant/location is resolved from message content —
but never by slug prefix. `locations.slug` is globally UNIQUE; a `slug LIKE 'x%'` matches
multiple branches and treats `%`/`_` in user text as wildcards, routing customers to the
wrong tenant. Resolution keys off the globally-unique identifier in the message:
 
**JOIN messages** (`"JOIN STAR-SALON JN8K4P"`):
1. `token_code` = last token in the body (`"JN8K4P"`)
2. `SELECT tenant_id, location_id FROM checkin_intents WHERE token_code=$1 AND status='created'`
3. `tenant_id` / `location_id` come from that intent row — authoritative
4. The slug in the body is display context only; if validated, use EXACT equality, never LIKE
**Button-payload messages** (`"ON_THE_WAY:{entry_id}"`, `"CANCEL:{entry_id}"`, `"RATING:..:{visit_id}"`):
Resolve `tenant_id` from the referenced entity (queue_entry → visit → location → tenant,
or `visit_id` directly). The entity UUID is the authoritative tenant anchor.
 
**Mode B:** `location_id` is already known from the webhook URL path; `tenant_id` is read from it.
 
Note: `tenant_id` is resolved from message content, not from `business_phone_number`. Multiple shops share the same BarberBase platform phone (Mode A).
 
---
 
## Tenant Isolation Rule
 
**Law 11: `tenant_id` comes from JWT context only. Never from request body.**
 
Go middleware extracts `tenant_id` from JWT into `context.Context`. All repository queries include `WHERE tenant_id=$1` sourced from context.
 
For PushActionToken: `tenant_id` is obtained by `SELECT staff_members WHERE id=$staff_member_id` after PAT verification. It is never embedded in the PAT itself and never taken from the request body.
 
See: `02_architecture_constraints.md`
 
---
 
## AI Agent Auth (Deferred)
 
Bearer token issued by owner from dashboard. `Authorization: Bearer {agent_token}`.
Scoped to read+write public operations. Not implemented in Phase 1.
