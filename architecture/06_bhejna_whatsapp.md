# Purpose
Complete specification of BarberBase's integration with Bhejna — what Bhejna is, Mode A vs Mode B, one-time setup, the sending API, inbound webhook payloads, credential selection per location, and the 24-hour session window.
 
# Use This File When
- Implementing the Bhejna client (sending)
- Implementing webhook ingress handlers
- Implementing Mode B (own-number) onboarding
- Checking how credentials are selected per location
- Understanding the webhook routing problem and solution
# Do Not Use This File For
- Outbox worker logic (→ `07_webhooks_outbox_workers.md`)
- Template parameters (→ `09_notifications_templates.md`)
- Auth flows (→ `03_auth_identity.md`)
# Related Files
- `07_webhooks_outbox_workers.md` — webhook worker classification
- `09_notifications_templates.md` — template specs
- `13_infra_env_deployment.md` — env vars for Bhejna credentials
- `15_critical_laws.md` — Laws 9, 15, 16
# Source Of Truth Priority
This file for Bhejna integration rules. `001_complete_schema.sql` for schema. `openapi.yaml` for webhook endpoint shape.
 
---
 
## What Bhejna Is
 
Multi-tenant WhatsApp Business API gateway. Bhejna:
- Holds Meta WABA access tokens per tenant
- Normalizes Meta webhooks → forwards to client's `webhook_url`
- Tracks 24-hour free conversation sessions per recipient
- Manages send tiers and quality ratings
BarberBase is one registered Bhejna tenant (Mode A). All barbershops are sub-tenants of BarberBase.
 
**BarberBase never calls Meta APIs directly. Never manages WABA tokens. Never processes Meta webhooks.**
 
---
 
## Mode A vs Mode B
 
### Mode A: Shared Platform Number (Default for all shops)
- All shops use BarberBase's registered WhatsApp number
- Customers message that number; shop routing is by message content (`JOIN STAR-SALON JN8K4P`)
- BarberBase routes internally by slug
- Credentials: `BHEJNA_API_KEY` + `BHEJNA_FROM_PHONE` env vars
- Webhook: `POST https://api.barberbase.in/v1/webhooks/bhejna`
- Zero per-shop configuration
### Mode B: Shop's Own Number (Phase 1, Premium)
- Shop registers their own WABA on Bhejna as a separate Bhejna tenant
- Customers see the shop's name as the sender
- Credentials stored encrypted in `locations` table
- Webhook: `POST https://api.barberbase.in/v1/webhooks/bhejna/loc/{location_id}`
- The `location_id` in the URL is used to look up the shop's `webhook_secret` before payload parsing
**Why per-location webhook URL:** Bhejna has one `webhook_url` per tenant. All Mode B shops point to BarberBase. BarberBase must know which shop's `webhook_secret` to use for HMAC verification before reading the payload. `location_id` in the path resolves this cleanly.
 
---
 
## One-Time BarberBase Platform Setup (Mode A)
 
```
Step 1: Register on Bhejna portal (CodenXT Lab account)
Step 2: Connect WABA via embedded signup:
  https://business.facebook.com/messaging/whatsapp/onboard/
    ?app_id=1594349876023947
    &config_id=1298428131650108
Step 3: In Bhejna tenant settings set:
  webhook_url    = https://api.barberbase.in/v1/webhooks/bhejna
  webhook_secret = {32+ char random string}
Step 4: Copy to BarberBase env:
  BHEJNA_API_KEY        = {from Bhejna portal}
  BHEJNA_WEBHOOK_SECRET = {same as step 3}
  BHEJNA_FROM_PHONE     = {platform phone, e.g. +912212345678}
```
 
Nothing in Bhejna needs to change for Phase 1. BarberBase uses Bhejna as any other client.
 
---
 
## Credential Selection Per Location
 
```
func BhejnaClient.Send(ctx, locationID, req):
    loc = GetLocationWhatsAppConfig(ctx, locationID)
 
    if loc.WhatsAppMode == "own_number":
        apiKey    = AES_Decrypt(loc.BhejnaAPIKeyEncrypted, AES_ENCRYPTION_KEY)
        fromPhone = loc.BusinessWhatsAppNumber
    else:
        apiKey    = env.BHEJNA_API_KEY
        fromPhone = env.BHEJNA_FROM_PHONE
 
    POST https://bhejna-api.codenxtlab.tech/v1/messages
      Authorization: Bearer {apiKey}
      Body: { ..., from_business_phone: fromPhone }
```
 
Identical sending API contract for both modes. Only auth header and `from_business_phone` differ.
 
---
 
## Bhejna Sending API
 
`POST https://bhejna-api.codenxtlab.tech/v1/messages`
 
**Text message (OTP — sent synchronously, not via outbox):**
```json
{
  "to": "+919876543210",
  "from_business_phone": "+912212345678",
  "idempotency_key": "barberbase:otp:{staff_otp_id}",
  "type": "text",
  "text": { "body": "Your BarberBase OTP: 847291. Valid 5 min." }
}
```
 
`staff_otp_id` = `staff_otps.id` for this issuance. Stable per-issuance; a retry of
the same OTP send reuses the same id and is deduped by Bhejna.
 
**Template message (via outbox worker):**
```json
{
  "to": "+919876543210",
  "from_business_phone": "+912212345678",
  "idempotency_key": "barberbase:outbox:{outbox_event_id}",
  "type": "template",
  "template": {
    "template_code": "bb_queue_joined",
    "language": "en",
    "components": [
      {
        "type": "body",
        "parameters": [
          { "type": "text", "text": "Star Salon" },
          { "type": "text", "text": "18" },
          { "type": "text", "text": "6" },
          { "type": "text", "text": "45" }
        ]
      },
      {
        "type": "button", "sub_type": "url", "index": 0,
        "parameters": [{ "type": "text", "text": "eyJhbGci..." }]
      }
    ]
  }
}
```
 
`outbox_event_id` = `outbox_events.id` for the row being dispatched. This key MUST be
used for every Bhejna template send dispatched by the outbox worker. One outbox row =
one intended send. Retries reclaim the same row and reuse the same id; Bhejna dedups
them. Two legitimate sends of the same template (e.g., `bb_you_are_next` after a
re-queue) produce different outbox rows with different ids and are both delivered.
Never key on entity ids (visit_id, customer_id) or timestamps — those suppress
legitimate repeat notifications or allow duplicates on retry.
 
**Success response 202:** `{ "job_id": "01KTBWFPS...", "status": "queued" }`
Store `job_id` in `notification_events.provider_message_id`.
 
**Error response:** `{ "success": false, "error": { "code": "...", "retryable": false }, "request_id": "..." }`
- `retryable: false` → mark `notification_events.status = 'failed'`
- `retryable: true` → backoff and retry, max 3 attempts
---
 
## Inbound Webhook Payloads
 
### A. `message.received`
```json
{
  "bhejna_event_id": "019001b3-4f9c-70e1-8000-017f8a9b2c3d",
  "event_type": "message.received",
  "channel": "whatsapp",
  "received_at": "2026-06-05T12:00:00Z",
  "business_phone_number": "912212345678",
  "sender": {
    "whatsapp_identifier": "919876543210",
    "bsuid": "IN.123456",
    "phone_number": "919876543210",
    "is_phone_masked": false,
    "display_name": "Rahul"
  },
  "message": {
    "meta_message_id": "wamid.HBgLMjMz...",
    "type": "text",
    "body": "JOIN STAR-SALON JN8K4P",
    "button_payload": null,
    "reply_to_meta_message_id": null
  }
}
```
 
### B. `message.status_updated`
```json
{
  "bhejna_event_id": "019001b3-4f9e-7110-9000-...",
  "event_type": "message.status_updated",
  "status_update": {
    "meta_message_id": "wamid.HBgLMjMz...",
    "status": "delivered",
    "recipient_bsuid": "919876543210",
    "pricing_category": "utility",
    "errors": []
  }
}
```
 
**⚠ Law 16 — Verify before implementing status correlation.**
The send path stores Bhejna's `job_id` in `notification_events.provider_message_id`.
The status webhook references `meta_message_id`. These are different identifiers; there
is no documented mapping between them in the example payload above.
 
Before implementing the `message.status_updated` handler, confirm with the live Bhejna
portal documentation:
1. Does the send response (`202`) also return `meta_message_id`? If yes: store it at
   send time alongside `job_id` and correlate on `meta_message_id`.
2. Does the status webhook also include `job_id`? If yes: correlate on `job_id`.
3. If neither namespace co-occurs: this requires a Bhejna-side field (similar to the
   Mode B `phone_number` column). Do not invent a mapping that does not exist.
Until verified, the `message.status_updated` handler should be implemented as a
no-op that logs the raw payload. Do not assume correlation works.
 
---
 
## Two Webhook Handlers (Go)
 
```
POST /v1/webhooks/bhejna                     ← Mode A only
  1. Read full body
  2. HMAC-SHA256(body, BHEJNA_WEBHOOK_SECRET) == X-Bhejna-Signature header → else 401
  3. INSERT webhook_events ON CONFLICT (source, external_event_id) DO NOTHING
  4. Return 200 immediately
 
POST /v1/webhooks/bhejna/loc/{location_id}   ← Mode B only
  1. Read full body
  2. Load location by location_id path param
  3. Validate whatsapp_mode = 'own_number' → else 404
  4. Decrypt bhejna_webhook_secret_encrypted (AES-256-GCM)
  5. HMAC-SHA256(body, decrypted_secret) == X-Bhejna-Signature header → else 401
  6. INSERT webhook_events (location_id pre-filled) ON CONFLICT DO NOTHING
  7. Return 200 immediately
```
 
**Law 9:** Always return 200 to Bhejna (after auth passes). Never return 5xx. 5xx = retry storm. Process asynchronously.
 
**Law 16:** Verify the actual Bhejna webhook signature header before implementing. Assumed: `X-Bhejna-Signature: sha256={hmac}`. Check live Bhejna portal docs to confirm header name.
 
---
 
## 24-Hour Session Window
 
Bhejna tracks `active_sessions(tenant_id, recipient_bsuid, expires_at)`.
When a customer messages BarberBase's WhatsApp, a 24-hour free window opens.
All utility templates sent within 24h of that message are free (₹0).
 
BarberBase enforces `magic_link_expires_at = created_at + 23 hours`. Hard-coded buffer.
 
After 24h window: appointment reminders ~₹0.14, weekly summary ~₹0.14, marketing ~₹0.90.
 
---
 
## Mode B Per-Location Config (in `001_complete_schema.sql`)
 
These columns live directly in `001_complete_schema.sql` on `locations` — there is no separate
`002` migration (no deployed DB / migration history exists yet). Only fields operationally
required to send and receive on a shop's own number are kept:
 
| Column | Type | Purpose |
|---|---|---|
| `whatsapp_mode` | VARCHAR(20) DEFAULT 'shared' | `'shared'` (Mode A, env creds) or `'own_number'` (Mode B) |
| `business_whatsapp_number` | VARCHAR(20) | Mode B: `from_business_phone` for sends (E.164) |
| `bhejna_api_key_encrypted` | TEXT | Mode B: AES-256-GCM encrypted api_key |
| `bhejna_webhook_secret_encrypted` | TEXT | Mode B: AES-256-GCM encrypted webhook_secret |
 
Removed as non-operational (validated at connect time, never read at send/receive):
`waba_id`, `whatsapp_display_name`, `whatsapp_connected_at`, `whatsapp_disconnected_at`,
and `tenants.owner_email` / `owner_email_verified` (no Phase 1 email channel exists).
Sender display name is controlled by Bhejna/Meta, not by BarberBase at send time.
 
---
 
## Mode B One-Paste Onboarding Flow
 
Owner pastes a JSON blob copied from Bhejna portal ("Copy BarberBase Integration Config" button):
 
```json
{
  "bhejna_config_version": "1",
  "waba_id": "1015610274155840",
  "phone_number": "+912212345678",
  "phone_number_id": "1127337617128308",
  "api_key": "nxt_live_f2d12bb55b234b92b659d0e767e...",
  "webhook_secret": "the_current_webhook_secret_value",
  "whatsapp_status": "ACTIVE",
  "display_name": "Star Salon",
  "quality_rating": "GREEN",
  "messaging_limit": 250
}
```
 
Go backend validates from the pasted blob: `whatsapp_status == "ACTIVE"`, `quality_rating != "RED"`, phone is E.164, then test-sends via Bhejna API. On success it stores ONLY `business_whatsapp_number`, the encrypted `api_key`, the encrypted `webhook_secret`, and sets `whatsapp_mode='own_number'`. `waba_id`, `display_name`, `quality_rating`, and `messaging_limit` are validation/diagnostic inputs only — not persisted. Returns the webhook URL for the owner to paste into the Bhejna portal.
 
Disconnect (`POST .../whatsapp/disconnect`) sets `whatsapp_mode='shared'` and clears the encrypted credentials; the location falls back to the platform number.
 
**Endpoint:** `POST /v1/admin/locations/{location_id}/whatsapp/connect`
 
Owner's final step: paste `https://api.barberbase.in/v1/webhooks/bhejna/loc/{location_id}` into Bhejna portal webhook URL field. Cannot be automated (no Bhejna API for this).
 
---
 
## Bhejna Changes Required (for Mode B Support)
 
Bhejna needs `phone_number` column added to its tenant table (not in current Supabase schema):
 
```sql
-- Bhejna Supabase
ALTER TABLE public.tenants ADD COLUMN phone_number TEXT;
 
-- Bhejna Go SQLite (local cache)
ALTER TABLE tenants ADD COLUMN phone_number TEXT;
```
 
Populate during `phone_number_name_update` Meta webhook. Also: add "Copy BarberBase Integration Config" button to Bhejna Developer Settings page (frontend-only change). No other Bhejna changes needed.
