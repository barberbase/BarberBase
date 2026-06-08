# Purpose
Master navigation guide for the BarberBase knowledge base. Start here. Route every question to the right file before reading anything else.
 
# Use This File When
- Starting any feature or change
- Unsure which file owns a domain
- Looking for source-of-truth hierarchy
# Do Not Use This File For
- Implementation details (go to the relevant file)
- Schema specifics (go to 001_complete_schema.sql)
# Related Files
All files in this knowledge base.
 
# Source Of Truth Priority
1. `001_complete_schema.sql` — database schema, constraints, indexes
2. `openapi.yaml` — API contracts, request/response shapes, security schemes
3. Split briefing files (this set) — domain rules, flows, constraints, rationale
4. `market_strategy.md` — pricing, positioning, prioritization
When files conflict: SQL wins for DB truth. OpenAPI wins for API truth. Briefing files win for domain intent.
 
---
 
## File Navigation Guide
 
| File | Owns |
|---|---|
| `00_project_map.md` | Navigation, dependency map, question routing |
| `01_product_domain.md` | What BarberBase is, company structure, URLs, core problem |
| `02_architecture_constraints.md` | Stack, infra, hard technology constraints |
| `03_auth_identity.md` | Customer session, Staff JWT/OTP, PushActionToken, identity resolution, tenant isolation |
| `04_queue_state_machine.md` | All state/presence transitions, is_dispatchable, shop status |
| `05_queue_locking_transactions.md` | FOR UPDATE pattern, mutation template, CompleteVisitAndCheckout (with push trigger), booking resolver, arrival PIN |
| `06_bhejna_whatsapp.md` | Bhejna architecture, Mode A/B, registration, sending API, webhook payloads, credential selection |
| `07_webhooks_outbox_workers.md` | Webhook ingress handler, webhook worker classifier, outbox worker (including web_push.send), quota enforcement |
| `08_sse_realtime.md` | SSE manager, broadcast pattern, client reconnect, version comparison |
| `09_notifications_templates.md` | All 12 WhatsApp templates with exact parameters, registration order, quota buckets |
| `10_customer_journey.md` | Full 17-step customer flow, checkin_intent lifecycle (creation → JOIN handshake), magic link page states, in-app browser constraints (WhatsApp webview only) |
| `11_appointments_booking.md` | Appointment lifecycle, booking resolver, appointment-day check-in |
| `12_staff_dashboard_frontend.md` | SvelteKit frontend pages, staff dashboard UX, Staff PWA manifest and push registration |
| `13_infra_env_deployment.md` | DigitalOcean droplet, env vars (including VAPID), Docker, Caddy, Go module deps (including webpush-go), OS tuning |
| `14_build_order_deferred.md` | Phase 1 build sequence (45 steps), deferred features table |
| `15_critical_laws.md` | The 21 immutable laws. Never violate. |
| `16_web_push_service_worker.md` | VAPID key lifecycle, PAT design and rationale, push subscription rules (latest wins), Service Worker scope policy, outbox dispatch flow, battery rules, Android/iOS matrix, queue integrity invariants for push |
 
---
 
## Question-to-File Routing
 
| Question / Change | Read First | Also Read |
|---|---|---|
| Modifying queue state transitions | `04_queue_state_machine.md` | `001_complete_schema.sql` |
| Modifying queue mutations (locking, TX) | `05_queue_locking_transactions.md` | `04_queue_state_machine.md` |
| CompleteVisitAndCheckout changes | `05_queue_locking_transactions.md` | `001_complete_schema.sql` |
| Adding a WhatsApp notification | `09_notifications_templates.md` | `07_webhooks_outbox_workers.md` |
| Modifying Bhejna integration | `06_bhejna_whatsapp.md` | `07_webhooks_outbox_workers.md` |
| Webhook handler changes | `07_webhooks_outbox_workers.md` | `06_bhejna_whatsapp.md` |
| Outbox worker changes | `07_webhooks_outbox_workers.md` | `09_notifications_templates.md` |
| SSE stream changes | `08_sse_realtime.md` | `openapi.yaml` |
| Auth changes (staff login) | `03_auth_identity.md` | `openapi.yaml` |
| PushActionToken auth | `16_web_push_service_worker.md` | `03_auth_identity.md` |
| Customer identity / phone resolution | `03_auth_identity.md` | `001_complete_schema.sql` |
| Magic link token changes | `03_auth_identity.md` | `10_customer_journey.md` |
| Customer journey / in-app browser | `10_customer_journey.md` | `08_sse_realtime.md` |
| Check-in intent / WhatsApp JOIN handshake | `10_customer_journey.md` | `openapi.yaml`, `07_webhooks_outbox_workers.md` |
| Appointment booking logic | `11_appointments_booking.md` | `001_complete_schema.sql` |
| Staff dashboard UI | `12_staff_dashboard_frontend.md` | `04_queue_state_machine.md` |
| Staff PWA / push permission prompt | `12_staff_dashboard_frontend.md` | `16_web_push_service_worker.md` |
| Public shop page | `12_staff_dashboard_frontend.md` | `11_appointments_booking.md` |
| Mode B own-number onboarding | `06_bhejna_whatsapp.md` | `13_infra_env_deployment.md` |
| Infrastructure / deployment | `13_infra_env_deployment.md` | — |
| Environment variables | `13_infra_env_deployment.md` | — |
| Adding a new database column | `001_complete_schema.sql` | Relevant domain file |
| Adding a new API endpoint | `openapi.yaml` | Relevant domain file |
| Understanding deferred features | `14_build_order_deferred.md` | — |
| Any correctness question | `15_critical_laws.md` | `05_queue_locking_transactions.md` |
| Push subscription registration | `16_web_push_service_worker.md` | `03_auth_identity.md` |
| Service Worker scope / page guards | `16_web_push_service_worker.md` | `10_customer_journey.md` |
| VAPID keys and rotation | `16_web_push_service_worker.md` | `13_infra_env_deployment.md` |
| Push outbox dispatch logic | `07_webhooks_outbox_workers.md` | `16_web_push_service_worker.md` |
| Push notification payload schema | `16_web_push_service_worker.md` | `openapi.yaml` |
| PWA manifest and install behavior | `12_staff_dashboard_frontend.md` | `16_web_push_service_worker.md` |
| Android vs iOS push support | `16_web_push_service_worker.md` | — |
| Queue correctness under push | `15_critical_laws.md` (Law 21) | `16_web_push_service_worker.md` |
 
---
 
## Dependency Map
 
```
15_critical_laws
    └── enforced by:
        ├── 05_queue_locking_transactions  (laws 1, 2, 7, 8, 10)
        ├── 06_bhejna_whatsapp             (laws 9, 15, 16)
        ├── 07_webhooks_outbox_workers     (laws 2, 7, 9, 12)
        ├── 03_auth_identity               (laws 3, 11)
        ├── 09_notifications_templates     (laws 4, 13, 14)
        └── 16_web_push_service_worker     (laws 17, 18, 19, 20, 21)
 
04_queue_state_machine
    └── implemented by:
        ├── 05_queue_locking_transactions
        └── 07_webhooks_outbox_workers     (webhook worker executes transitions)
 
06_bhejna_whatsapp
    └── consumed by:
        ├── 07_webhooks_outbox_workers     (sending + receiving)
        └── 09_notifications_templates     (template codes + parameters)
 
10_customer_journey
    └── depends on:
        ├── 04_queue_state_machine
        ├── 06_bhejna_whatsapp
        ├── 08_sse_realtime
        └── 03_auth_identity               (magic link token)
 
16_web_push_service_worker
    └── depends on:
        ├── 03_auth_identity               (HMAC_SECRET for PAT; JWT TTL proof)
        ├── 05_queue_locking_transactions  (CompleteVisitAndCheckout step 12.5)
        ├── 07_webhooks_outbox_workers     (web_push.send outbox event type)
        ├── 001_complete_schema.sql        (staff_members push columns)
        ├── 13_infra_env_deployment.md     (VAPID env vars; webpush-go)
        └── 10_customer_journey.md         (WhatsApp webview SW prohibition)
    └── referenced by:
        ├── 15_critical_laws.md            (Laws 17–21)
        ├── 12_staff_dashboard_frontend.md (PWA manifest; push permission UX)
        └── 03_auth_identity.md            (PushActionToken auth tier)
```
 
---
 
## Schema Notes
 
All Phase 1 columns live in `001_complete_schema.sql`. There is no `002` migration (no
deployed DB / migration history exists yet).
 
Mode B per-location WhatsApp config in `001` (trimmed to operationally-required fields):
`whatsapp_mode`, `business_whatsapp_number`, `bhejna_api_key_encrypted`,
`bhejna_webhook_secret_encrypted`. Removed as non-operational: `waba_id`,
`whatsapp_display_name`, `whatsapp_connected_at`, `whatsapp_disconnected_at`,
`tenants.owner_email`, `tenants.owner_email_verified`.
 
Staff OTP store: `staff_otps` table in `001` (PostgreSQL — durable + horizontal-safe).
 
Staff PWA push columns (`push_endpoint`, `push_p256dh`, `push_auth`, `push_enabled` on
`staff_members`; `'web_push'` in `notification_events.channel`) are in `001`. See
`16_web_push_service_worker.md`.
