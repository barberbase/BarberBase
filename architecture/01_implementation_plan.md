# 01_implementation_plan.md — BarberBase Phase 1 Execution Plan

Architecture is frozen. This plan does not redesign anything. It sequences the 45 approved
build items (`14_build_order_deferred.md`) into executable phases, splits **barberbase-core**
(Go) and **barberbase-frontend** (SvelteKit) into separate tracks, and assigns each unit to
the right tool with the minimal file set required to implement it.

---

## §0 Conventions, ownership, blockers

**Owner legend**
- `[CL]` — Claude. Produces the artifact directly (SQL, contract edits, correctness-critical algorithm bodies, race review).
- `[AG]` — Antigravity. Autonomous multi-file implementation against a frozen spec; runs codegen, wiring, repos, UI, tests, device runs.
- `[CL→AG]` — Claude writes the spec / lock-order / SQL body; Antigravity writes the surrounding code and tests against it.

**Ownership policy (one rule):** Claude owns anything where a hallucination causes silent data
corruption or a security hole — every queue transaction's lock order and SQL, idempotency,
identity/tenant-resolution SQL, PAT verification, outbox frequency gate, OpenAPI/SQL edits,
and review of Antigravity output against the 21 laws. Antigravity owns everything mechanical
where the contract is fixed.

**Per-phase context discipline:** the `Read` line of each unit is the *complete* context for
that unit. Load those files only. Do not load the rest of the knowledge base. This is the
mechanism for minimum context + minimum hallucination — every unit is independently executable
from its `Read` set + `001_complete_schema.sql` + `openapi.yaml`.

**Testing baseline (applies to all core units):** integration tests run against a **real
PostgreSQL 16** (docker-compose), never mocks — correctness depends on `FOR UPDATE`,
constraints, and `lock_timeout`. Queue-mutation units additionally require a **concurrency
test** (N parallel goroutines) proving Law 1 serialization and monotonic `queue_version`.
Frontend units: Vitest (logic) + Playwright (flows). Device units: physical Android first.

**Repo naming:** backend is `barberbase-core`, frontend is `barberbase-frontend` — now
consistent across `13_infra_env_deployment.md` and `16_web_push_service_worker.md`. Paths
below are taken verbatim from those trees.

**Open blockers: none.** Both conflicts flagged in the prior draft are resolved in the
current files: (1) `createCheckinIntent` (`POST /v1/public/locations/{location_id}/checkin-intents`)
now exists in `openapi.yaml` returning `{intent_id, token_code, deep_link, expires_at}` — F3's
WhatsApp deep link is no longer blocked (now handled in C3.3 + F3); (2) the directory tree is
named `barberbase-core` consistently. No assumed-contract risk remains for the public join path.

**Verify-only units (already present in frozen files — confirm on apply, never edit):**
#29 VAPID env vars, #30 `webpush-go` in go.mod, #31 push columns + channel CHECK in `001`,
#33 `subscribePush`/`pushCallNext`/`PushActionToken` scheme in `openapi.yaml`.

---

## §1 Track map

| Track | Phase | Scope | Build # |
|---|---|---|---|
| CORE | C0 | Codegen, pool/tx, schema, Bhejna client | 1–4 |
| CORE | C1 | Auth + webhook ingress/worker | 5–7 |
| CORE | C2 | Queue session + mutations + SSE | 8–13 |
| CORE | C3 | Outbox + checkout + public reads | 14, 15, (public reads) |
| CORE | C4 | Operational backend (status, appts, jobs, feedback, analytics, quota) | 19–24 |
| CORE | C5 | Admin/onboarding backend | 25–28 |
| CORE | C6 | Push backend (VAPID, PAT, dispatch, step 12.5) | 29–38 |
| FRONTEND | F0 | SvelteKit scaffold + BFF (prereq, **not one of the 45**) | — |
| FRONTEND | F1 | Staff dashboard | 16 |
| FRONTEND | F2 | Magic link page | 17 |
| FRONTEND | F3 | Public shop page | 18 |
| FRONTEND | F4 | Admin/onboarding UI (full-stack halves of 19/23/25–28) | 19, 23, 25–28 (UI) |
| FRONTEND | F5 | Staff PWA: manifest, SW, permission prompt | 39–41 |
| JOINT | V1 | Push E2E + battery soak + device compat + Law 21 regression | 42–45 |

**Ordering:** the approved build order is preserved by the build numbers; within each track,
units run in ascending build #. Cross-track gates (frontend → core endpoints) are stated per
unit. The two tracks proceed in parallel once their gating core phase commits.

Items 19, 23, 25–28 are full-stack features: a **backend half** (C-track) and a **UI half**
(F4). Each number appears in both, annotated `(backend)` / `(UI)`. Every number 1–45 appears
exactly once per layer.

---

## §2 CORE track — `barberbase-core` (Go modular monolith)

### Phase C0 — Foundation

**C0.1 — oapi-codegen + generated.go · build #1 · [AG]**
Depends: —
Read: `13_infra_env_deployment.md` (deps, dir tree), `02_architecture_constraints.md`, `openapi.yaml`
Write: `api/openapi.yaml` (copy of frozen contract), `internal/api/generated.go` (codegen output), codegen config, `cmd/server/main.go` (skeleton)
Accept: `generated.go` builds; every `operationId` in `openapi.yaml` has a server interface method; `generated.go` is committed and marked never-edit.
Test: `go build ./...` green; CI guard fails the build if `generated.go` is hand-edited (diff against fresh codegen).

**C0.2 — pgxpool + transaction wrapper · build #2 · [CL→AG]**
Depends: C0.1
Read: `02_architecture_constraints.md` (memory budget, PG config), `05_queue_locking_transactions.md` (tx wrapper expectations), `13_infra_env_deployment.md`
Write: `internal/repository/` (pool init + `WithTx` wrapper), `cmd/server/main.go`
Accept: `MaxConns=20`; `GOMEMLIMIT=250MiB` honored; per-session `lock_timeout=1s`, `statement_timeout=5s`, `idle_in_transaction_session_timeout=10s` applied; `WithTx` rolls back on error/panic and never leaks a connection.
Test: integration — forced panic inside `WithTx` leaves zero open transactions; lock held >1s by a second tx returns a retriable lock_timeout error.

**C0.3 — Apply `001_complete_schema.sql` · build #3 · [AG]**
Depends: C0.2
Read: `001_complete_schema.sql`
Write: `migrations/001_complete_schema.sql` (apply only; no `002`)
Accept: all 35 tables, indexes, CHECKs, and partial indexes created on a clean PG16; UUID v7 defaults present; push columns + `notification_events.channel` CHECK include `'web_push'`; `needs_review` present in the relevant state CHECK.
Test: apply on empty DB → `\d+` inventory matches file; re-apply is a clean no-op via guard; verify no `NOW()` in any partial-index predicate.

**C0.4 — Bhejna client (text + template send) · build #4 · [CL→AG]**
Depends: C0.2
Read: `06_bhejna_whatsapp.md`, `13_infra_env_deployment.md` (env), `15_critical_laws.md` (15, 16)
Write: `internal/bhejna/client.go`
Accept: outbound uses Bearer (`BHEJNA_API_KEY` Mode A; decrypted per-location key Mode B); text + template send implemented; credential selection is Mode A/B aware; one platform key for Mode A (Law 15).
Test: unit with stubbed HTTP — Mode A vs Mode B selects correct key; template payload shape matches `09_notifications_templates.md`; 5xx from Bhejna surfaces a retriable error to the outbox caller (no panic).

### Phase C1 — Auth + webhook ingress

**C1.1 — Staff OTP + JWT · build #5 · [CL→AG]**
Depends: C0.3, C0.4
Read: `03_auth_identity.md`, `openapi.yaml` (`requestStaffOTP`,`verifyStaffOTP`,`refreshStaffToken`), `001` (`staff_otps`,`staff_members`)
Write: `internal/auth/otp.go`, `internal/auth/jwt.go`, `internal/auth/middleware.go`, `internal/api/handlers_staff.go` (auth ops), `pkg/middleware/tenant.go`
Accept: 6-digit `crypto/rand` OTP, bcrypt-hashed in `staff_otps`, 5-min TTL; request-otp rate-limited 3/phone/10min via `x/time/rate`; verify is a single `FOR UPDATE` tx — lockout at ≥5 attempts, `consumed_at` set on success; JWT 15-min, payload `{tenant_id,location_id,staff_member_id,role}`; refresh 30-day; unknown phone → 401 (no self-registration); `tenant_id` injected from JWT into context (Law 11), never from body.
Test: integration — replayed OTP after `consumed_at` → 401; 6th attempt → 401; expired code → 401; tenant middleware rejects any body `tenant_id`.

**C1.2 — Bhejna webhook ingress · build #6 · [CL→AG]**
Depends: C0.4, C0.3
Read: `07_webhooks_outbox_workers.md`, `06_bhejna_whatsapp.md`, `15_critical_laws.md` (9, 16), `001` (`webhook_events`)
Write: `internal/api/handlers_webhook.go`, `internal/repository/webhook.go`
Accept: **verify the live Bhejna signature header before coding (Law 16)** — implement what Bhejna actually sends; HMAC-verify → idempotent `INSERT webhook_events` (dedupe on provider event id) → return **200 immediately**, never 5xx (Law 9); zero synchronous processing in the handler.
Test: integration — duplicate delivery inserts one row; bad signature → reject without enqueue; handler returns 200 in <50ms with no downstream processing on the request path.

**C1.3 — Webhook worker + classifier · build #7 · [CL→AG]**
Depends: C1.2
Read: `07_webhooks_outbox_workers.md`, `06_bhejna_whatsapp.md`, `03_auth_identity.md` (identity + tenant resolution), `04_queue_state_machine.md`, `15_critical_laws.md` (2, 3)
Write: `internal/webhook/processor.go`, `internal/webhook/message_classifier.go`, `internal/webhook/intent_resolver.go`, `internal/repository/customer.go`, `internal/domain/identity/resolver.go`, `internal/domain/identity/merge.go`
Accept: `SKIP LOCKED` claim loop (Law 2, workers only); routes JOIN / button-payload / rating; **tenant resolved from the message's unique entity, never slug-`LIKE`** (JOIN → `checkin_intents.token_code`; button → entity UUID chain); phone normalized to E.164 (Law 3); shadow-profile path on masked number; lease recovery + terminal-failure re-poll.
Test: integration — JOIN with `%`/`_` in the slug text routes by `token_code` to the correct tenant, not a wildcard match; two workers never process the same event; crashed worker's leased event is re-claimed.

### Phase C2 — Queue session + mutations + SSE

**C2.1 — Queue session auto-create · build #8 · [CL]**
Depends: C0.3
Read: `05_queue_locking_transactions.md` (auto-create), `001` (`queue_sessions`)
Write: `internal/repository/queue.go`, `internal/domain/queue/commands.go`
Accept: upsert-then-lock ordering — `INSERT … ON CONFLICT (location_id,business_date) DO NOTHING` **before** `SELECT … FOR UPDATE`; concurrent first-joiners converge on one row.
Test: concurrency — 50 parallel first-joins on a fresh date create exactly one session, `last_token_number` has no gap/dup.

**C2.2 — `POST /v1/queue/join` · build #9 · [CL→AG]**
Depends: C2.1
Read: `05_queue_locking_transactions.md` (mutation template, idempotency), `04_queue_state_machine.md`, `15_critical_laws.md` (1,7,8,12), `001` (`queue_entries`,`visits`,`idempotency_keys`,`visit_services`), `openapi.yaml` (`joinQueue`)
Write: `internal/domain/queue/commands.go`, `internal/repository/queue.go` + `visit.go`, `internal/api/handlers_public.go`
Accept: full atomic tx — lock session FOR UPDATE, all 8 steps, `queue_version++`, outbox insert inside tx (Law 7); idempotency = `INSERT idempotency_keys … ON CONFLICT DO NOTHING RETURNING id` first, replay stored response on conflict; `visits.idempotency_key` UNIQUE is the second guard; `visit_services` written once (Law 10); SSE broadcast after commit (Law 8).
Test: idempotency — same `idempotency_key` twice (interleaved) → one visit, one queue_entry, identical response; concurrency — 100 parallel distinct joins → monotonic `queue_version`, contiguous tokens.

**C2.3 — `POST /v1/staff/queue/call-next` · build #10 · [CL→AG]**
Depends: C2.2
Read: `05_queue_locking_transactions.md` (call-next + routing filters), `04_queue_state_machine.md` (`is_dispatchable`), `15_critical_laws.md` (1,12), `openapi.yaml` (`callNextCustomer`)
Write: `internal/domain/queue/commands.go`, `internal/repository/queue.go`, `internal/api/handlers_staff.go`
Accept: dispatch gate `state='waiting' AND is_dispatchable=true AND presence_state='arrived'` (Laws 12, 6) + `queue_routing_mode` filter (pooled/hybrid/barber_specific); order `priority_group, sort_key, token_number`; on no match → 404 with `waiting_remote_count`; sets `called`, `assigned_barber_id`, barber `cutting`, outbox `bb_you_are_next`, `queue_version++`; plain `FOR UPDATE`, never `SKIP LOCKED` (Law 2).
Test: concurrency — two barbers call-next simultaneously on a 1-arrived queue → exactly one gets the customer, the other 404s, no double-call; routing — `barber_specific` never dispatches another barber's requested customer.

**C2.4 — Start / Direct Start · build #11 · [CL→AG]**
Depends: C2.3
Read: `05_queue_locking_transactions.md` (direct start), `04_queue_state_machine.md`, `15_critical_laws.md` (1), `openapi.yaml` (`startService`)
Write: `internal/domain/queue/commands.go`, `internal/domain/queue/state_machine.go`, `internal/api/handlers_staff.go`
Accept: `called→in_progress` normal path; `waiting+arrived→in_progress` sets `called_at` and `started_at` atomically; `waiting` with `presence≠arrived` → 422; barber `cutting`; `queue_version++`.
Test: integration — direct start on `waiting/remote` → 422; on `waiting/arrived` → both timestamps set in one tx.

**C2.5 — Confirm-arrival (PIN/GPS/staff) · build #12 · [CL→AG]**
Depends: C2.2
Read: `05_queue_locking_transactions.md` (arrival PIN), `04_queue_state_machine.md` (presence), `15_critical_laws.md` (6), `001` (`arrival_attempts`), `openapi.yaml` (`confirmArrival`,`staffConfirmArrival`,`confirmOnTheWay`,`cancelMyEntry`)
Write: `internal/domain/presence/arrival.go`, `internal/api/handlers_public.go` + `handlers_staff.go`
Accept: arrived is never self-declared (Law 6); PIN bcrypt-verified, rate-limited 5/entry + 10/IP/hr; GPS `accuracy≤150m` and haversine `≤arrival_radius_metres`; staff method requires StaffJWT and bypasses PIN; every attempt logged in `arrival_attempts`; success → `presence='arrived'`, `is_dispatchable=true`, SSE ping.
Test: integration — wrong PIN 6× → rate-limited; `accuracy=200` → 422; GPS just outside radius → 422; staff tap with CustomerSession → 401.

**C2.6 — SSE manager + stream · build #13 · [CL→AG]**
Depends: C2.3
Read: `08_sse_realtime.md`, `02_architecture_constraints.md` (sync.Map fanout), `15_critical_laws.md` (8, 21), `openapi.yaml` (`subscribeToQueueStream`,`getQueueSnapshot`)
Write: `internal/realtime/manager.go`, `internal/api/handlers_staff.go` (stream + snapshot)
Accept: in-memory `sync.Map` broadcast, heartbeat 30s; broadcast only after COMMIT (Law 8); SSE carries `queue_version` for stale comparison; **queue is correct with SSE down** (Law 21) — `getQueueSnapshot` is the canonical recovery read.
Test: integration — kill the SSE connection mid-session, mutate via REST, reconnect → snapshot reflects truth; broadcast fires post-commit only (no stale push on rollback).

### Phase C3 — Outbox + checkout + public reads

**C3.1 — Outbox worker (12 templates) · build #14 · [CL→AG]**
Depends: C1.3, C0.4
Read: `07_webhooks_outbox_workers.md`, `09_notifications_templates.md`, `06_bhejna_whatsapp.md`, `15_critical_laws.md` (2,7,9), `001` (`outbox_events`,`notification_events`,`tenant_quota_periods`,`quota_usage_ledger`)
Write: `internal/outbox/worker.go`, `internal/outbox/handlers/notification.go`
Accept: `SKIP LOCKED` claim; quota checked **before every Bhejna call**; all 12 templates wired with exact params/registration order; `notification_events` recorded; lease recovery + terminal re-poll; marketing hard-blocked at quota, transactional **never** blocked.
Test: integration — quota-exhausted marketing event is held while a transactional event still sends; two workers never double-send; failed send re-polls and is not lost.

**C3.2 — CompleteVisitAndCheckout (14 steps) · build #15 · [CL]**
Depends: C2.4, C3.1
Read: `05_queue_locking_transactions.md` (14-step), `04_queue_state_machine.md`, `15_critical_laws.md` (1,4,7,8,10), `001` (`visit_charges`,`visit_charge_line_items`,`visit_payments`), `openapi.yaml` (`completeService`)
Write: `internal/domain/queue/commands.go`, `internal/repository/visit.go`, `internal/api/handlers_staff.go`
Accept: all-or-nothing tx; payment validation `SUM(payment_lines)=subtotal−discount`; money in paise (Law 4); line items + payments + charges immutable once written (Law 10); entry `completed`+`is_dispatchable=false`, visit/customer/staff updated; outbox `feedback_request.schedule` (+30min) inside tx (Law 7); **step 12.5 deferred to C6.6**; `queue_version++`; SSE after commit.
Test: integration — payment mismatch → full rollback, zero rows written; completed entry never re-dispatched; historical line items unchanged after a later price edit.

**C3.3 — Public read + booking-options + checkin-intent endpoints · implied (gates #18, #20) · [CL→AG]**
Depends: C0.3, C1.3
Read: `12_staff_dashboard_frontend.md` (public flow), `10_customer_journey.md` (checkin_intent lifecycle), `05_queue_locking_transactions.md` (booking resolver), `11_appointments_booking.md`, `openapi.yaml` (`getLocationStatus`,`getServiceCatalog`,`searchServiceVariants`,`resolveBookingOptions`,`createCheckinIntent`,`getAppointmentSlots`), `001` (`locations`,`service_*`,`queue_sessions`,`checkin_intents`)
Write: `internal/api/handlers_public.go`, `internal/repository/location.go` + `service.go`, `internal/domain/queue/booking_resolver.go`, `internal/webhook/intent_resolver.go`
Accept: reads are no-auth (Cloudflare WAF + `x/time/rate`); booking resolver computes `allowed_modes`, totals, `estimated_wait`, `blocked_reason` per the documented rules (`requires_appointment` removes `walk_in`; capacity/closing limits enforced). `createCheckinIntent` is **public + Turnstile + rate-limited**, inserts a `checkin_intents` row (`status='created'`, 6-char `token_code`, `shop_status_at_creation` snapshot), returns `{intent_id, token_code, deep_link, expires_at}`, and **does NOT create a queue_entry** (the JOIN webhook worker does, C1.3); `deep_link` targets the Mode B business number else the Mode A platform number, pre-filled `JOIN {SLUG} {TOKEN}`; **no idempotency key** — each tap mints a throwaway intent, unresolved intents expire in 23h; closing-time rejection happens at JOIN resolution, not at creation.
Test: integration — all-variants walk-in → `walk_in` present; any `requires_appointment` → `appointment` only; queue at `max_total_queue_size` → `walk_in` removed; `createCheckinIntent` writes one `checkin_intents` row and zero `queue_entries`; token resolves later via the C1.3 JOIN path to the correct tenant; intent past 23h is not resolvable.

### Phase C4 — Operational backend

**C4.1 — Shop status (backend) · build #19 (backend) · [CL→AG]**
Depends: C0.3
Read: `04_queue_state_machine.md` (shop status), `openapi.yaml` (`setShopStatus`), `001` (`location_status_overrides`)
Write: `internal/api/handlers_staff.go`, `internal/repository/location.go`
Accept: manual `open`/`closed`/`temporarily_closed` with expiry; override interacts correctly with the booking resolver's mode narrowing.
Test: integration — `temporarily_closed` with future expiry blocks `walk_in`; expiry passes → walk-in re-allowed.

**C4.2 — `POST /v1/appointments/book` (staff-only) · build #20 · [CL→AG]**
Depends: C3.3, C2.2
Read: `11_appointments_booking.md`, `05_queue_locking_transactions.md` (resolver), `15_critical_laws.md` (7,10), `001` (`appointments`,`visits`,`visit_services`), `openapi.yaml` (`bookAppointment`)
Write: `internal/api/handlers_public.go` (staff-gated), `internal/repository/visit.go`, `internal/domain/queue/booking_resolver.go`
Accept: Phase-1 staff-created only; `appointments.idempotency_key` UNIQUE guard; `visit_services` snapshot at booking time (Law 10); outbox confirmation inside tx (Law 7); appointment entries seed `priority_group=50`.
Test: integration — duplicate book key → one appointment; appointment outranks walk-ins in dashboard order.

**C4.3 — Watchdog + end-of-day + weekly summary · build #21 · [CL→AG]**
Depends: C3.1
Read: `07_webhooks_outbox_workers.md` (workers), `09_notifications_templates.md` (EOD/weekly), `15_critical_laws.md` (14), `13_infra_env_deployment.md` (advisory-lock singletons, `statement_timeout` exemption)
Write: `internal/jobs/watchdog.go`, `internal/jobs/end_of_day.go`, `internal/jobs/weekly_summary.go`
Accept: time-driven singletons guarded by `pg_try_advisory_lock`; **weekly summary ships (Law 14)** — Sunday 22:00 cron + `bb_weekly_summary` outbox; heavy reporting queries run with `statement_timeout=0` exemption; watchdog flags stale `called`/`in_progress` per the stale-warning thresholds.
Test: integration — two app instances → job runs once (advisory lock); weekly summary enqueues for active tenants only; long aggregation does not abort at 5s.

**C4.4 — Feedback system · build #22 · [CL→AG]**
Depends: C3.2, C3.1, C1.3
Read: `09_notifications_templates.md` (rating), `07_webhooks_outbox_workers.md` (feedback scheduler + rating webhook), `001` (`feedback_requests`,`feedback_responses`), `openapi.yaml` (`submitFeedback`)
Write: `internal/outbox/handlers/feedback_scheduler.go`, `internal/webhook/message_classifier.go` (rating route), `internal/api/handlers_public.go`
Accept: `feedback_request.schedule` (+30min) → outbox → Bhejna rating template → inbound rating reply classified → `feedback_responses`; idempotent on duplicate rating reply.
Test: integration — completed visit produces exactly one feedback request after 30min; duplicate rating reply stored once.

**C4.5 — Daily analytics (backend) · build #23 (backend) · [CL→AG]**
Depends: C3.2
Read: `12_staff_dashboard_frontend.md` (analytics), `openapi.yaml` (`getDailyAnalytics`), `001` (`visits`,`visit_payments`)
Write: `internal/api/handlers_staff.go`, `internal/repository/visit.go`
Accept: SQL aggregation only (no analytics store); revenue, customer count, avg wait, no-show rate; owner/manager role only; reporting query uses the `statement_timeout` exemption.
Test: integration — totals reconcile against a seeded day; barber role → 403.

**C4.6 — Tenant quota enforcement · build #24 · [CL→AG]**
Depends: C3.1
Read: `07_webhooks_outbox_workers.md` (quota), `09_notifications_templates.md` (buckets), `15_critical_laws.md`, `001` (`tenant_quota_periods`,`quota_usage_ledger`)
Write: `internal/outbox/handlers/notification.go`, `internal/repository/outbox.go`
Accept: per-bucket quota checked before each marketing send; marketing hard-blocked when exhausted; transactional categories never blocked; ledger increments are tx-safe.
Test: integration — marketing blocked at limit while `bb_you_are_next` still sends; concurrent sends don't over-count the ledger.

### Phase C5 — Admin / onboarding backend

**C5.1 — Services CRUD (backend) · build #25 (backend) · [CL→AG]**
Depends: C0.3, C1.1
Read: `12_staff_dashboard_frontend.md` (admin services), `openapi.yaml` (`createServiceVariant`,`updateServiceVariant`), `001` (`service_categories`,`service_groups`,`service_variants`)
Write: `internal/api/handlers_admin.go`, `internal/repository/service.go`
Accept: 3-level hierarchy CRUD; `is_popular`, `price_paise`, `duration_minutes`, booking rules per variant; owner/manager only; prices in paise.
Test: integration — variant edits don't mutate historical `visit_services`; role gate enforced.

**C5.2 — Staff management (backend) · build #26 (backend) · [CL→AG]**
Depends: C1.1
Read: `03_auth_identity.md` (phone = identity, no self-reg), `openapi.yaml` (`createStaffMember`), `001` (`staff_members`)
Write: `internal/api/handlers_admin.go`, `internal/repository/` (staff)
Accept: owner adds staff by phone + role; phone is the login credential; no self-registration path exists.
Test: integration — login with a never-created phone → 401; created phone can request OTP.

**C5.3 — Tenant onboarding (backend) · build #27 (backend) · [CL→AG]**
Depends: C5.1, C5.2
Read: `12_staff_dashboard_frontend.md` (admin), `01_product_domain.md` (company structure), `001` (`tenants`,`locations`)
Write: `internal/api/handlers_admin.go`, `internal/repository/location.go`
Accept: first-time setup creates tenant + location + slugs; `locations.slug` globally unique; arrival PIN initialized.
Test: integration — duplicate slug rejected; new tenant fully isolated (Law 11 spot-check).

**C5.4 — Mode B own-number connect (backend) · build #28 (backend) · [CL→AG]**
Depends: C0.4, C5.3
Read: `06_bhejna_whatsapp.md` (Mode B), `13_infra_env_deployment.md` (AES key), `openapi.yaml` (`connectWhatsAppModeB`,`disconnectWhatsAppModeB`,`receiveBhejnaWebhookModeB`), `001` (`locations` whatsapp_* cols)
Write: `internal/api/handlers_admin.go`, `internal/api/handlers_webhook.go` (Mode B path), `internal/repository/location.go`
Accept: JSON paste → validate → AES-256-GCM encrypt → store `bhejna_api_key_encrypted`/`bhejna_webhook_secret_encrypted` → return per-location webhook URL; Mode B webhook path reads `location_id` from URL, `tenant_id` from it; no per-shop env vars (Law 15).
Test: integration — stored key round-trips via `AES_ENCRYPTION_KEY`; Mode B inbound resolves tenant from path, not message content.

### Phase C6 — Push backend

**C6.1 — VAPID + webpush-go + schema verify · build #29, #30, #31 · [AG] (verify-only)**
Depends: C0.3
Read: `16_web_push_service_worker.md` (VAPID), `13_infra_env_deployment.md` (env, deps), `001` (push columns)
Write: env config (`VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY`/`VAPID_SUBJECT`), `go.mod` confirm `SherClockHolmes/webpush-go`
Accept: VAPID keypair generated once via `webpush.GenerateVAPIDKeys()`; public key also set as `PUBLIC_VAPID_PUBLIC_KEY` in Cloudflare Pages; push columns + `'web_push'` channel CHECK already in `001` (confirm, **do not edit**).
Test: env presence check on boot; private key never serialized to any response.

**C6.2 — `internal/push/vapid.go` (VAPID sign + PAT gen/verify) · build #32 · [CL]**
Depends: C6.1, C1.1
Read: `16_web_push_service_worker.md` (PAT format + verify steps), `03_auth_identity.md` (PAT tier), `15_critical_laws.md` (18, 20)
Write: `internal/push/vapid.go`
Accept: PAT = `base64url(payload).base64url(HMAC-SHA256(payload, HMAC_SECRET))`, payload `{staff_member_id}:{location_id}:call_next:{unix_expires}`, 4h TTL; verify = exactly-two-segments → constant-time MAC compare → parse → command literal check → expiry check; uses existing `HMAC_SECRET`, no new secret/table (Laws 18, 20).
Test: unit — forged MAC → fail; tampered payload → fail; wrong command literal → command-reject; expired → fail; valid → parsed claims; constant-time path confirmed.

**C6.3 — OpenAPI PAT scheme + push endpoints → re-codegen · build #33, #34 · [CL] then [AG]**
Depends: C6.2
Read: `16_web_push_service_worker.md`, `openapi.yaml` (`subscribePush`,`pushCallNext`,`PushActionToken`)
Write: re-run codegen → `internal/api/generated.go`
Accept: `subscribePush`, `pushCallNext`, and `PushActionToken` scheme **already present in frozen `openapi.yaml`** — verify only, never edit (#33); re-run codegen so `generated.go` exposes both handler interfaces (#34).
Test: `go build` green; both interfaces present in `generated.go`.

**C6.4 — `handlers_push.go` · build #35 · [CL→AG]**
Depends: C6.3
Read: `16_web_push_service_worker.md` (verify sequence), `03_auth_identity.md` (PAT verify), `15_critical_laws.md` (18,20,21), `openapi.yaml`
Write: `internal/api/handlers_push.go`
Accept: `subscribe` = StaffJWT → upsert push columns on `staff_members` (latest wins, one per member); `call-next` = PAT verify (401/403 per `vapid.go`) → rate-limit 1/3s per staff → `SELECT staff_members … is_active` for `tenant_id` → call the **same** call-next domain fn as the dashboard; domain layer never sees the PAT.
Test: integration — `call-next` with a forged PAT → 401; with a non-`call_next` PAT → 403; valid PAT advances the queue via the shared domain fn; rapid replay is safe (queue lock).

**C6.5 — Push dispatch handler + worker routing · build #36, #37 · [CL→AG]**
Depends: C6.4, C3.1
Read: `16_web_push_service_worker.md` (dispatch flow), `07_webhooks_outbox_workers.md` (outbox worker), `15_critical_laws.md` (19), `001` (`notification_events` channel)
Write: `internal/outbox/handlers/push_notification.go`, `internal/outbox/worker.go` (route `web_push.send`)
Accept: `web_push.send` **bypasses Bhejna quota entirely**; frequency gate (Law 19) — if zero `waiting/dispatchable/arrived` entries, mark dispatched and send nothing; per enabled staff: generate PAT, encrypt with `push_p256dh`+`push_auth`, VAPID-sign `Urgency:high`, POST endpoint; 410 → null out push columns + disable; 2xx → `notification_events` (`channel='web_push'`, `source_type='staff_member'`, status `'sent'` only — no delivered).
Test: integration — 0 arrived-dispatchable → no send, event dispatched; 410 response cleans the subscription; quota path is never consulted for this type.

**C6.6 — CompleteVisitAndCheckout step 12.5 · build #38 · [CL]**
Depends: C3.2, C6.5
Read: `05_queue_locking_transactions.md` (step 12.5), `16_web_push_service_worker.md`, `15_critical_laws.md` (7, 19, 21)
Write: `internal/domain/queue/commands.go` (insert step into existing tx)
Accept: conditional `INSERT outbox_events(type='web_push.send', process_after=NOW())` **inside the existing checkout tx** (Law 7), gated on `EXISTS(staff_members … push_enabled AND is_active)`; rollback drops the event (no orphan push); checkout behaves identically with push disabled (Law 21); the actual send decision stays in the dispatch handler's frequency gate.
Test: integration — checkout rollback inserts no push event; with no push-enabled staff, no outbox row is written; with push-enabled staff + arrived queue, exactly one `web_push.send` row enqueues.

---

## §3 FRONTEND track — `barberbase-frontend` (SvelteKit / Svelte 5, Cloudflare Pages)

**F0 — SvelteKit scaffold + BFF · prerequisite (not one of the 45) · [AG]**
Depends: C0.1 (contract for the API client)
Read: `12_staff_dashboard_frontend.md` (BFF pattern), `02_architecture_constraints.md` (SvelteKit/Svelte 5), `13_infra_env_deployment.md` (domains)
Write: project scaffold, `src/hooks.server.ts` (401→`/auth/staff/refresh`→retry), typed API client against `api.barberbase.in`, `PUBLIC_VAPID_PUBLIC_KEY` env wiring
Accept: SSR builds for Cloudflare Pages; StaffJWT + refresh in httpOnly cookies set by BFF; customer tokens via URL query only (webview cookie isolation); auto-refresh hook works.
Test: Playwright — expired access token transparently refreshes and the original request succeeds.

**F1 — Staff dashboard · build #16 · [AG]**
Depends (cross-track): C2.2–C2.6, C3.2 (+ `addWalkIn`, `reassign`, `skip`, `no-show`, `reactivate`, `staffConfirmArrival` endpoints)
Read: `12_staff_dashboard_frontend.md` (dashboard UX), `04_queue_state_machine.md` (render states), `08_sse_realtime.md` (client), `openapi.yaml` (`getQueueSnapshot` + all `/staff/queue/*`)
Write: `src/routes/dashboard/+page.svelte` + `+page.server.ts`, queue store, checkout-modal component, SSE client
Accept: big single-tap targets; Call Next / Direct Start / Mark No-Show are one tap, no confirm modal; checkout modal is the only multi-step flow with `SUM(payments)=subtotal−discount` client validation; queue ordering `in_progress → called → waiting(priority_group, sort_key)`; presence + stale-warning (yellow/red) indicators; SSE-driven with `queue_version` compare → `getQueueSnapshot` on stale/reconnect; **fully usable with SSE down** (load = snapshot, then SSE).
Test: Playwright — kill SSE, mutate via another client, dashboard recovers on reconnect via snapshot; checkout total mismatch blocks submit; one-tap actions issue exactly one request (debounced).

**F2 — Magic link page · build #17 · [AG]**
Depends (cross-track): C2.5, C2.6 (+ `getMyQueueStatus`, `confirmOnTheWay`, `cancelMyEntry`)
Read: `10_customer_journey.md` (7 page states, WhatsApp webview constraints), `08_sse_realtime.md`, `03_auth_identity.md` (magic-link token), `openapi.yaml` (`getMyQueueStatus`,`confirmArrival`,`on-the-way`,`cancel`,`feedback`)
Write: `src/routes/q/status/+page.svelte` + `+page.server.ts`, `src/routes/q/appointment/+page.svelte`
Accept: token from `?t=` only, carried in `X-Session-Token` (never cookies); all 7 states render; PIN + GPS arrival entry; live position via SSE with snapshot fallback; **no JS-memory state that isn't recoverable from the URL**; renders inside WhatsApp webview; **no Service Worker registration on this route** (Law 17).
Test: Playwright — reload mid-session restores from URL; PIN success flips to arrived state; SSE drop falls back to `getMyQueueStatus` poll.

**F3 — Public shop page · build #18 · [AG]**
Depends (cross-track): C3.3 (public reads + booking-options + `createCheckinIntent`)
Read: `12_staff_dashboard_frontend.md` (public flow), `10_customer_journey.md` (deep link + intent lifecycle, webview), `openapi.yaml` (`getLocationStatus`,`getServiceCatalog`,`resolveBookingOptions`,`createCheckinIntent`)
Write: `src/routes/[tenant_slug]/[location_slug]/+page.svelte` + `+page.server.ts`
Accept: SSR for fast paint; service selector (Hair/Beard/Skin tabs, popular highlighted); `booking-options` drives totals + `estimated_wait`; walk-in allowed → [Join via WhatsApp] CTA that calls `createCheckinIntent`, then shows "WhatsApp will open. Press Send." + the returned `deep_link` button; walk-in blocked → `blocked_reason`; appointment booking UI is Phase 1.5 (show appointment-blocked message in Phase 1); Cloudflare Turnstile on the join form; no JS-memory state that isn't recoverable from the URL; **no Service Worker registration on this route** (Law 17).
Test: Playwright — selecting variants returns correct modes/totals; tapping Join calls `createCheckinIntent` and surfaces a `wa.me` deep link with `JOIN {SLUG} {TOKEN}`; closed shop shows `blocked_reason`; no SW registers on this route (Law 17).

**F4 — Admin / onboarding UI · build #19, #23, #25–#28 (UI halves) · [AG]**
Depends (cross-track): C4.1, C4.5, C5.1–C5.4
Read: `12_staff_dashboard_frontend.md` (admin sections), `06_bhejna_whatsapp.md` (Mode B paste flow), `openapi.yaml` (admin + `setShopStatus` + `getDailyAnalytics`)
Write: `src/routes/admin/+page.svelte` + sub-pages, `src/routes/admin/analytics/+page.svelte`, `+page.server.ts` (role guard)
Accept: owner/manager role guard in BFF load; Services CRUD over the 3-level hierarchy (#25 UI); staff add by phone + role, no self-register (#26 UI); first-time onboarding (#27 UI); shop open/close/temp-close toggle (#19 UI); Mode B JSON-paste → connect → shows returned webhook URL (#28 UI); analytics read-only daily view (#23 UI); money displayed `/100`.
Test: Playwright — barber role can't load `/admin`; Mode B paste surfaces the webhook URL; price entered in rupees stored as paise via the API.

**F5 — Staff PWA: manifest, SW, permission prompt · build #39, #40, #41 · [CL→AG]**
Depends (cross-track): C6.4 (subscribe endpoint), C6.1 (`PUBLIC_VAPID_PUBLIC_KEY`); F1
Read: `16_web_push_service_worker.md` (SW scope, payload schema, error contract), `12_staff_dashboard_frontend.md` (PWA + prompt), `15_critical_laws.md` (17, 21), `10_customer_journey.md` (webview SW prohibition)
Write: `static/dashboard/manifest.json` (#39), `src/service-worker.js` (#40), dashboard permission-prompt component (#41)
Accept: manifest `scope:"/dashboard/"`, `start_url:"/dashboard"`, `display:"standalone"` (#39); SW handles `push` + `notificationclick`, registration gated on `pathname.startsWith('/dashboard')` AND confirmed StaffJWT, **never root scope, never on customer routes** (Law 17) (#40); NEXT CLIENT fires background `POST /v1/staff/push/call-next` with the PAT from the payload (never StaffJWT, Law 18); notifications `silent:true`, single shared `tag`, no MediaSession, no audio; **401 on tap MUST update the notification** (stale-success is forbidden); prompt shown second session post-StaffJWT, deny → silent SSE-only fallback (Law 21) (#41); VAPID public key from `import.meta.env.PUBLIC_VAPID_PUBLIC_KEY`, not an API call.
Test: Playwright/Vitest — SW does not register on `/q/status` or public routes; deny path produces no error state and dashboard still works; simulated 401 from the action updates the notification text.

---

## §4 JOINT verification — push E2E + device + Law 21

Cross-layer; requires CORE C6 + FRONTEND F5 deployed together. Owner `[AG]` for execution, `[CL]` for the Law-21 regression spec and failure triage.

**V1.1 — Android E2E · build #42 · [AG]**
Read: `16_web_push_service_worker.md` (dispatch + matrix), `12_staff_dashboard_frontend.md`
Accept: on Chrome **and** Samsung Internet — subscribe → complete a visit → push arrives → tap NEXT CLIENT from lock screen → queue advances (verified in PostgreSQL) without unlocking or opening the browser.
Test: physical Galaxy A-series; assert `queue_version` increment + correct next-customer dispatch after the lock-screen tap.

**V1.2 — 12-hour battery soak · build #43 · [AG]**
Read: `16_web_push_service_worker.md` (battery rules, FCM high-priority)
Accept: over a 12h shift simulation, ≤20 push events/shift; FCM `Urgency:high` delivers in Doze; no battery-pathological wake pattern.
Test: physical mid-tier Android, Doze enabled; count push wakeups; confirm delivery latency under Doze is operationally acceptable.

**V1.3 — Samsung Internet compat · build #44 · [AG]**
Accept: SW registration, subscription, and `notificationclick` behave identically to Chrome on Galaxy A-series.
Test: physical device matrix run of V1.1 on Samsung Internet specifically.

**V1.4 — iOS best-effort · build #45 · [AG]**
Read: `16_web_push_service_worker.md` (Android/iOS matrix, iOS support policy)
Accept: after Add-to-Home-Screen, push delivers and the action can advance the queue from Notification Center; degraded persistence documented; **no iOS-specific code**. Unacceptable (investigate): push never delivers after correct install, or a 401 tap gives no visual feedback.
Test: physical iPhone (Safari 16.4+ PWA); confirm delivery + functional NEXT CLIENT; record degraded persistence as expected.

**V1.5 — Law 21 regression (gate for ship) · [CL spec, AG run]**
Read: `15_critical_laws.md` (21), `16_web_push_service_worker.md`
Accept: with **push infrastructure disabled entirely** (no VAPID, no subscriptions, no dispatch), every core workflow — join, call-next, start, complete, confirm-arrival, skip/no-show/reactivate, SSE delivery, snapshot recovery — passes identically. No entry stuck, lost, or double-called.
Test: full core integration + Playwright suite run twice (push enabled / push disabled); identical functional outcomes required to ship.

---

## §5 Coverage check (every build # mapped once per layer)

Core: 1·C0.1 2·C0.2 3·C0.3 4·C0.4 | 5·C1.1 6·C1.2 7·C1.3 | 8·C2.1 9·C2.2 10·C2.3 11·C2.4
12·C2.5 13·C2.6 | 14·C3.1 15·C3.2 | 19·C4.1 20·C4.2 21·C4.3 22·C4.4 23·C4.5 24·C4.6 |
25·C5.1 26·C5.2 27·C5.3 28·C5.4 | 29-31·C6.1 32·C6.2 33-34·C6.3 35·C6.4 36-37·C6.5 38·C6.6.
Frontend: 16·F1 17·F2 18·F3 | 19/23/25-28 (UI)·F4 | 39-41·F5.
Joint: 42·V1.1 43·V1.2 44·V1.3 45·V1.4 (+ Law-21 gate V1.5).
Implied/unnumbered (contracted in `openapi.yaml`, grouped under their phase): public reads + booking resolver + `createCheckinIntent` (C3.3); SvelteKit scaffold (F0).
