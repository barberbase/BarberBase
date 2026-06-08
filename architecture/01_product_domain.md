# Purpose
Defines what BarberBase is, why it exists, the company structure, all canonical URLs, and the core domain problem. Establishes context for every other file.
 
# Use This File When
- Need to understand the product mission or core problem
- Need canonical URLs for code, templates, or configuration
- Need to understand BarberBase's relationship to Bhejna
- Onboarding a new implementation agent
# Do Not Use This File For
- Stack or infrastructure decisions (→ `02_architecture_constraints.md`)
- API contracts (→ `openapi.yaml`)
- Schema details (→ `001_complete_schema.sql`)
# Related Files
- `02_architecture_constraints.md`
- `06_bhejna_whatsapp.md`
- `13_infra_env_deployment.md`
# Source Of Truth Priority
Briefing for domain intent. `001_complete_schema.sql` for all DB facts.
 
---
 
## What BarberBase Is
 
Real-time operations SaaS for Indian barbershops and salons. Not a CRUD dashboard — a live operating system.
 
**Core capabilities:**
- Unified queue for walk-ins and remote joiners
- Customers join queue from home via WhatsApp; arrive only when their turn is near
- Barbers get a one-tap dashboard to move the queue forward
- Records daily revenue and customer history
- Weekly performance summaries to shop owners (Phase 1 — not deferred)
**The core problem it solves:** On Sundays, customers walk into a barbershop, see 10 people waiting, and silently leave. The owner never knows the loss. BarberBase makes the wait visible and manageable.
 
**Target market:** Mid-tier air-conditioned salons and barbershops in India, 3–5 chairs. Initial focus: Mira Bhayandar, Mumbai. Pricing: ₹299–₹999/month per location.
 
---
 
## Company Structure
 
**CodenXT Lab** — Parent sole proprietorship. Owns both products.
 
**BarberBase** — This system. Queue + appointment + operations SaaS.
Stack: Go + PostgreSQL on DigitalOcean. SvelteKit on Cloudflare Pages.
 
**Bhejna** — Sibling product. Standalone WhatsApp Business API gateway.
Stack: SvelteKit on Appwrite. Go + SQLite on DigitalOcean. Supabase as source of truth.
Any developer registers on Bhejna, connects their WABA, gets an `api_key`, sends/receives WhatsApp without touching Meta directly.
 
**Relationship:** BarberBase is one registered Bhejna tenant — same as any other developer.
- BarberBase has its own Bhejna `api_key`, its own WABA, its own `webhook_url`.
- Each barbershop on BarberBase is NOT a separate Bhejna tenant in Phase 1.
- BarberBase is one Bhejna tenant. All barbershops route through BarberBase's single WABA.
- Shop-level routing happens by message content (`JOIN STAR-SALON JN8K4P`), not phone number.
- BarberBase never calls Meta APIs directly. Never manages WABA access tokens. Never processes Meta webhooks. Bhejna owns all of that.
---
 
## Canonical URLs
 
All URLs in code, templates, and configuration MUST use these exact values.
 
```
Frontend (SvelteKit on Cloudflare Pages):
  https://barberbase.in
 
  Shop landing page:   https://barberbase.in/{tenant_slug}/{location_slug}
  Magic link page:     https://barberbase.in/q/status?t={signed_token}
  Appointment page:    https://barberbase.in/q/appointment?t={signed_token}
  Staff dashboard:     https://barberbase.in/dashboard
  Owner admin:         https://barberbase.in/admin
  Staff login:         https://barberbase.in/login
 
API (Go on DigitalOcean via Caddy):
  https://api.barberbase.in
 
  All API routes:      https://api.barberbase.in/v1/...
  Bhejna webhook:      https://api.barberbase.in/v1/webhooks/bhejna
  Mode B webhook:      https://api.barberbase.in/v1/webhooks/bhejna/loc/{location_id}
 
Bhejna API (sibling, separate service):
  https://bhejna-api.codenxtlab.tech
```
 
---
 
## Domain Model Glossary
 
| Term | Meaning |
|---|---|
| tenant | One shop owner account (one row in `tenants`) |
| location | One physical shop (multiple locations per tenant possible) |
| queue_session | One day's queue for one location |
| queue_entry | One customer's slot in a queue session ("the pawn") |
| visit | One customer's service interaction (parent of queue_entry) |
| checkin_intent | Pending intent to join queue, holds a token_code, expires 23h |
| magic link | Signed HMAC URL sent to customer via WhatsApp, scoped to one visit |
| is_dispatchable | Boolean gate — only `true` entries appear in "Call Next" |
| paise | Monetary unit. All money is paise. 100 paise = ₹1. Never floats. |
| BSUID | Bhejna's identifier for a WhatsApp user. Supplementary to phone number. |
| Mode A | Shop uses BarberBase shared WhatsApp number (default) |
| Mode B | Shop uses its own WhatsApp number (premium, Phase 1) |
 
---
 
## WhatsApp Is Enhancement, Not Dependency
 
If Bhejna is unavailable:
- Staff dashboard and SSE still function
- Customers can still join via the web page
- Customers can confirm arrival via PIN on the magic link page
- The queue works — WhatsApp notifications are async enhancement, not correctness path
