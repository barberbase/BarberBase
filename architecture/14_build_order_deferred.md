# Purpose
Documents the Phase 1 build sequence (45 components in dependency order) and the table of deferred features with their planned phases.
 
# Use This File When
- Starting implementation (start here to pick the right component)
- Checking if a feature is in Phase 1 or deferred
- Determining what must be built before something else
# Do Not Use This File For
- How each component works (go to the relevant domain file)
- Infrastructure setup (→ `13_infra_env_deployment.md`)
# Related Files
All domain files — this file references them all.
 
# Source Of Truth Priority
Briefing for build priority. Domain files for implementation details.
 
---
 
## Phase 1 Build Order
 
Build in this sequence. Each item has a blocking dependency on prior items.
 
| # | Component | Key Rule / Constraint |
|---|---|---|
| 1 | oapi-codegen setup + generate handlers | Run BEFORE writing any handler. `generated.go` is never edited. |
| 2 | pgxpool + transaction wrapper | `MaxConns=20`, `GOMEMLIMIT=250MiB`, `lock_timeout=1s` |
| 3 | DB migration: `001_complete_schema.sql` | Apply and verify all tables, indexes, constraints |
| 4 | Bhejna client (text + template send) | HMAC verify on inbound webhook, Bearer on outbound |
| 5 | Auth: OTP + JWT | OTP via Bhejna text message. bcrypt OTP hash. 6-digit, 5min. |
| 6 | Bhejna webhook ingress receiver | HMAC sig verify → idempotent inbox → 200 immediately |
| 7 | Webhook worker + message classifier | SKIP LOCKED, JOIN/BOOK/button/rating routing |
| 8 | Queue session auto-create | Create today's session on first join if not exists |
| 9 | `POST /v1/queue/join` | Atomic FOR UPDATE transaction with all 8 steps |
| 10 | `POST /v1/staff/queue/call-next` | FOR UPDATE dispatch, find arrived-first |
| 11 | `POST /v1/staff/queue/entries/{id}/start` | Direct Start with presence=arrived guard |
| 12 | `POST /v1/queue/confirm-arrival` | PIN + GPS + staff methods. Rate limit. bcrypt verify. |
| 13 | SSE manager + stream endpoint | `sync.Map` broadcast, heartbeat every 30s |
| 14 | Outbox worker — all 12 templates | Quota check before every Bhejna call |
| 15 | `POST /v1/staff/queue/entries/{id}/complete` | Full 14-step CompleteVisitAndCheckout |
| 16 | Staff dashboard (SvelteKit) | Big tap targets, Direct Start, checkout modal |
| 17 | Magic link page (SvelteKit) | SSE + PIN entry + all 7 state views |
| 18 | Public shop page (SvelteKit) | Service selector + booking resolver + WhatsApp join deep link |
| 19 | Shop status management | Manual open/close/temporarily_closed |
| 20 | `POST /v1/appointments/book` | Staff-created only in Phase 1 |
| 21 | Watchdog + end-of-day + weekly summary | All background goroutines |
| 22 | Feedback system | outbox → Bhejna → rating reply webhook |
| 23 | Daily analytics | SQL aggregation only (no separate analytics store) |
| 24 | Tenant quota enforcement | Hard block on marketing; never block transactional |
| 25 | Admin: Services CRUD | Categories → groups → variants, 3-level hierarchy |
| 26 | Admin: Staff management | Phone number is login credential, no self-registration |
| 27 | Tenant onboarding | First-time setup admin page |
| 28 | Mode B own-number connect flow | JSON paste → validate → encrypt → store → return webhook URL |
| 29 | VAPID keypair generation + env vars | `webpush.GenerateVAPIDKeys()`. Set VAPID_*. PUBLIC_VAPID_PUBLIC_KEY in Cloudflare Pages. |
| 30 | Add `webpush-go` to go.mod | VAPID signing + payload encryption |
| 31 | Apply Staff PWA schema in `001_complete_schema.sql` | staff_members push columns + notification_events channel CHECK (already in schema; verify on apply) |
| 32 | `internal/push/vapid.go` | VAPID signing, PAT generation, PAT verification (HMAC_SECRET) |
| 33 | `openapi.yaml`: PushActionToken scheme + 2 endpoints | POST /staff/push/subscribe, POST /staff/push/call-next |
| 34 | Re-run oapi-codegen → generated.go | Never edit generated.go manually |
| 35 | `handlers_push.go` | subscribe (StaffJWT upsert) + call-next (PAT auth → call-next domain fn) |
| 36 | `internal/outbox/handlers/push_notification.go` | web_push.send dispatch: frequency gate, PAT, FCM urgency:high, 410 cleanup |
| 37 | Outbox worker `web_push.send` routing branch | Skip Bhejna quota logic for this type |
| 38 | CompleteVisitAndCheckout step 12.5 | Conditional INSERT outbox web_push.send (Law 7) |
| 39 | `static/dashboard/manifest.json` | PWA manifest, scope `/dashboard/` |
| 40 | `src/service-worker.js` | push + notificationclick handlers, scope-gated (Law 17) |
| 41 | Dashboard push permission prompt | Second-session, post-StaffJWT. Deny → SSE-only fallback. |
| 42 | Android E2E test (Chrome + Samsung Internet) | Subscribe → complete visit → push → NEXT CLIENT → queue advances |
| 43 | 12-hour battery soak test on physical mid-tier Android | Verify ≤20 push events/shift; FCM high-priority delivery in Doze |
| 44 | Samsung Internet physical device compatibility | Galaxy A-series; confirm SW + push behavior |
| 45 | iOS best-effort verification (PWA install) | Confirm push delivers; document degraded persistence |
 
---
 
## Deferred Features
 
| Feature | Planned Phase |
|---|---|
| Public appointment booking UI (date picker) | Phase 1.5 |
| `plans` / `plan_quotas` / `tenant_subscriptions` tables | After 50 shops |
| Razorpay billing automation | After 50 shops |
| Marketing campaign builder UI | Phase 2 |
| Full staff commission settlement | Phase 2 |
| Retail POS + inventory | Phase 2 |
| SMS OTP fallback | When DLT registered |
| NFC tag arrival confirmation | Phase 1.5 (schema ready in 001_complete_schema.sql) |
| Bhejna AI agent integration | When Bhejna agent ships |
| Multi-region | Phase 3 |
| `owner_email_verified` enforcement | Phase 1.5 (column exists, verification deferred) |
| Confirm-arrival push trigger (idle barber) | Phase 1.5 (only CompleteVisitAndCheckout triggers push in Phase 1) |
| Tab-active push suppression (SSE presence) | Phase 1.5 (accept dual notification when tab open in Phase 1) |
 
**NOT deferred (Phase 1):**
- Mode B (own WhatsApp number for shops) — Phase 1 premium feature
- Weekly summary (`bb_weekly_summary`) — Phase 1 owner retention mechanism
---
 
## Critical Build Note
 
`generated.go` is the output of `oapi-codegen` from `openapi.yaml`. It defines all handler interfaces. Every handler file (`handlers_public.go`, `handlers_staff.go`, etc.) must implement these interfaces. Run codegen first, then write implementations. If `openapi.yaml` changes, re-run codegen before touching any handler.
