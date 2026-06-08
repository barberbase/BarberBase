# Purpose
Covers the webhook ingress worker (classifies and routes inbound Bhejna events), the outbox worker (dispatches WhatsApp notifications and web push), and the quota enforcement flow.
 
# Use This File When
- Implementing the webhook_events worker (SKIP LOCKED processor)
- Implementing the outbox_events worker
- Adding a new message classification or button payload handler
- Implementing quota checking before Bhejna sends
- Debugging a missed notification or failed webhook
- Adding a new outbox event type
# Do Not Use This File For
- Bhejna sending API shapes (→ `06_bhejna_whatsapp.md`)
- Template parameters (→ `09_notifications_templates.md`)
- Queue locking (→ `05_queue_locking_transactions.md`)
- Webhook HTTP handler ingress (→ `06_bhejna_whatsapp.md`)
- Push dispatch implementation details (→ `16_web_push_service_worker.md`)
# Related Files
- `001_complete_schema.sql` — `webhook_events`, `outbox_events`, `notification_events`, `tenant_quota_periods`, `staff_members` (push columns) tables
- `06_bhejna_whatsapp.md` — inbound payload shapes, sending API
- `09_notifications_templates.md` — template codes and parameters
- `15_critical_laws.md` — Laws 2, 7, 9, 12, 19
- `16_web_push_service_worker.md` — web_push.send dispatch handler; PAT generation; FCM urgency; 410 cleanup
# Source Of Truth Priority
`001_complete_schema.sql` for table structure. This file for worker logic.
 
---
 
## Webhook Worker (Inbound Classifier)
 
Claim-or-reclaim in one statement: recovers rows whose worker died mid-processing via the
`locked_until` lease, and stops retrying terminal-failed events. `webhook_events` has no
`max_attempts` column; the cap is a code constant (10).
 
### Worker Loop
 
```sql
UPDATE webhook_events
SET status='processing', locked_until = NOW() + INTERVAL '30 seconds', attempts = attempts + 1
WHERE id = (
  SELECT id FROM webhook_events
  WHERE ( status='pending'
       OR (status='failed'     AND attempts < 10)
       OR (status='processing' AND locked_until < NOW()) )
  ORDER BY created_at
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING *;
```
 
Then (outside the claim transaction):
1. Classify and execute domain action
2. Success: `UPDATE status='processed', processed_at=NOW()`
3. Retryable failure, attempts < 10: `UPDATE status='failed', last_error=...`
4. attempts >= 10: `UPDATE status='failed'` (terminal; no longer claimed by the WHERE above)
### Classification Rules (Mode A — location_id is NULL)
 
First validate: `normalizeE164(business_phone_number) == normalizeE164(BHEJNA_FROM_PHONE)` (env var), else discard. Webhook payloads omit the leading `+`; the env var includes it — compare normalized E.164, not raw strings.
 
| Message pattern | Action |
|---|---|
| `"JOIN {slug} {token_code}"` | Resolve location by slug → resolve checkin_intent → create queue_entry |
| `"BOOK {slug} {token_code}"` (future) | Resolve location → create appointment intent |
| `button_payload = "ON_THE_WAY:{entry_id}"` | presence_state → on_the_way, SSE ping |
| `button_payload = "CANCEL:{entry_id}"` | Cancel queue_entry (if waiting/called) |
| `button_payload = "CANCEL_APT:{appointment_id}"` | Cancel appointment |
| `button_payload = "RATING:{1-5}:{visit_id}"` | INSERT feedback_response |
| `button_payload = "OPT_OUT_MARKETING"` | UPDATE customer_consents opt-out |
| `body matches "^[1-5]$"` (plain text reply) | INSERT feedback_response (typed rating) |
| `"STOP"` or `"UNSUBSCRIBE"` | Marketing opt-out |
| anything else | Send help text via Bhejna (plain text reply) |
 
### Classification Rules (Mode B — location_id pre-filled)
 
- location_id already known — skip slug resolution
- Resolve tenant_id from location
- Same classification table as above, minus slug resolution step
### message.status_updated handling (both modes)
 
**⚠ Law 16 dependency — verify before implementing.**
The send path stores `job_id` in `notification_events.provider_message_id`. The status
webhook references `meta_message_id`. These are different namespaces with no documented
mapping. See `06_bhejna_whatsapp.md` — "Bhejna Sending API" for the required verification
steps. Until the shared identifier is confirmed with the live Bhejna portal, implement
this handler as a no-op that logs the raw payload and marks the webhook_event processed.
 
---
 
## JOIN Intent Resolution Flow
 
Triggered when webhook worker classifies `"JOIN {slug} {token_code}"`:
 
```
1. SELECT checkin_intent WHERE token_code = {token from body} AND status='created'
     → location_id and tenant_id come from this row (authoritative; no slug match needed)
     → not found → reply "Link expired or invalid"
2. (Optional) validate the body slug against the resolved location by EXACT equality
3. Normalize sender.phone_number → E.164 → resolve/create customer_id under that tenant_id
4. Validate: checkin_intent.expires_at > NOW()        (else: reply "Link expired")
5. Validate: location.status IN ('open','closing_soon') (else: reply "Shop closed")
6. BEGIN
     Lock queue_session FOR UPDATE (auto-create if needed)
     -- C1: one active entry per customer per session
     -- The DB constraint (idx_queue_entries_one_active_per_customer) is the hard
     -- backstop; this in-txn check provides a clean reply before the INSERT fails.
     IF customer_id IS NOT NULL AND EXISTS (
       SELECT 1 FROM queue_entries qe
       JOIN visits v ON v.id = qe.visit_id
       WHERE qe.queue_session_id = $session_id
         AND v.customer_id = $customer_id
         AND qe.state IN ('waiting', 'called', 'in_progress')
     ):
       → ROLLBACK, reply "You already have an active spot in this queue"
     Create visit (customer_id = $customer_id)
     Insert visit_services (snapshot variant data)
     Create queue_entry (state='waiting', presence='remote',
                         customer_id=$customer_id)   ← must match visits.customer_id
     last_token_number++, queue_version++
     UPDATE checkin_intent.status='resolved'
     INSERT outbox_event: type='notification.send', template='bb_queue_joined'
   COMMIT
7. SSE broadcast
```
 
---
 
## Outbox Worker
 
Claim-or-reclaim in one statement: recovers rows whose worker died mid-dispatch, stops
retrying terminal-failed events, and backs off retries instead of hot-looping. No separate
reaper goroutine — reclaim is folded into the claim.
 
### Worker Loop
 
```sql
UPDATE outbox_events
SET status='processing', locked_until = NOW() + INTERVAL '30 seconds', attempts = attempts + 1
WHERE id = (
  SELECT id FROM outbox_events
  WHERE process_after <= NOW()
    AND ( status='pending'
       OR (status='failed'     AND attempts < max_attempts)
       OR (status='processing' AND locked_until < NOW()) )
  ORDER BY process_after
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING *;
```
 
Then (outside the claim transaction):
 
4. Route by event type:
   IF type == 'web_push.send':
     → Go to Web Push Dispatch Handler (see below)
     → SKIP steps 5–8 entirely (no Bhejna call, no quota check)
   ELSE:
     → Continue with Bhejna dispatch (steps 5–8)
5. Determine quota_type:
     template in (bb_marketing_broadcast) → 'whatsapp_marketing'
     all others → 'whatsapp_transactional'
6. Quota check transaction:
     BEGIN
       -- V5: auto-create the period row on first send of the month.
       -- Mirrors the queue_session auto-create pattern (05_queue_locking_transactions.md).
       -- included_limit is read from tenants.monthly_*_quota at creation time.
       -- ON CONFLICT DO NOTHING is a no-op on every subsequent send of the month.
       INSERT INTO tenant_quota_periods
         (tenant_id, quota_type, period_start, period_end, included_limit)
       SELECT $tenant_id, $quota_type,
              date_trunc('month', NOW())::DATE,
              (date_trunc('month', NOW()) + INTERVAL '1 month - 1 day')::DATE,
              CASE $quota_type
                WHEN 'whatsapp_marketing'     THEN t.monthly_marketing_quota
                WHEN 'whatsapp_transactional' THEN t.monthly_transactional_quota
              END
       FROM tenants t WHERE t.id = $tenant_id
       ON CONFLICT (tenant_id, quota_type, period_start) DO NOTHING;
       SELECT tenant_quota_periods FOR UPDATE
         WHERE tenant_id=$1 AND quota_type=$2 AND period_start=$current_month
       IF quota_type='whatsapp_marketing' AND used_count >= included_limit:
         → ABORT, UPDATE outbox_event.status='failed', notification.status='blocked_quota'
         RETURN (do not send)
       INSERT quota_usage_ledger (idempotency_key=outbox_event.id)
         ON CONFLICT DO NOTHING
       UPDATE tenant_quota_periods SET used_count=used_count+1
         WHERE quota_usage_ledger INSERT above returned a row  ← only increment if new row
     COMMIT
7. POST to Bhejna /v1/messages (→ see 06_bhejna_whatsapp.md for payload shape)
   MUST set `idempotency_key = "barberbase:outbox:{outbox_event.id}"`.
   This is the at-least-once dedup key: retries reuse the same outbox row id and
   are deduped by Bhejna. Never key on visit_id, customer_id, or timestamps.
8. INSERT notification_events with provider_message_id=job_id, status='sent'
9. On success: UPDATE outbox_event.status='dispatched', dispatched_at=NOW()
   On retryable failure, attempts < max_attempts:
     UPDATE status='failed', last_error=..., process_after=NOW()+backoff(attempts)  -- 30s, 2m, 10m
   On attempts >= max_attempts:
     UPDATE status='failed', last_error=...  -- terminal; no longer claimed
### Quota Rules
 
| Bucket | Templates | Behavior |
|---|---|---|
| `whatsapp_transactional` | All templates except bb_marketing_broadcast | Soft limit 1000/month. Warning at 800. Never hard-blocked. Queue notifications always go through. |
| `whatsapp_marketing` | `bb_marketing_broadcast` only | Hard limit 100/month (₹299 plan). Hard block at limit. Returns status='blocked_quota'. Never affects transactional bucket. |
| `web_push` | Not applicable | No quota. Bypasses steps 5–8 entirely. No Bhejna call. |
 
**Marketing quota exhaustion NEVER blocks queue notifications.** The two buckets are completely independent.
 
---
 
## Outbox Event Types
 
| `type` field | Trigger | `process_after` | Quota |
|---|---|---|---|
| `notification.send` | Any queue transition needing WhatsApp | NOW() | whatsapp_transactional or whatsapp_marketing |
| `feedback_request.schedule` | CompleteVisitAndCheckout step 13 | NOW() + 30 min | whatsapp_transactional |
| `appointment.reminder` | Appointment created | 6 PM day before appointment | whatsapp_transactional |
| `weekly_summary.send` | Sunday 10 PM cron | NOW() | whatsapp_transactional |
| `web_push.send` | CompleteVisitAndCheckout step 12.5 | NOW() | None — bypasses Bhejna quota entirely |
 
For `notification.send`, `feedback_request.schedule`, `appointment.reminder`, `weekly_summary.send`:
The `payload` JSONB contains all parameters needed to call Bhejna: template_code, to, from_business_phone, components array.
 
For `web_push.send`:
The `payload` JSONB contains: `{ "location_id": "...", "tenant_id": "..." }`
The dispatch handler queries current queue state and staff subscriptions at execution time.
 
---
 
## Web Push Dispatch Handler
 
Handles `type='web_push.send'` events. Lives in `internal/outbox/handlers/push_notification.go`.
Bypasses all Bhejna quota logic. See `16_web_push_service_worker.md` for full details.
 
```
1. Extract location_id, tenant_id from outbox_event.payload
 
2. FREQUENCY GATE (Law 19):
   SELECT COUNT(*) FROM queue_entries
     WHERE queue_session_id = (today's active session for location_id)
     AND state = 'waiting'
     AND is_dispatchable = true
     AND presence_state = 'arrived'
   If 0: UPDATE outbox_event.status='dispatched'. Return.
         (No arrived customers = push would be meaningless action. Skip entirely.)
 
3. SELECT * FROM staff_members
   WHERE location_id=$1 AND push_enabled=true AND is_active=true
 
4. For each staff member:
   a. Generate PAT (4h TTL) using HMAC_SECRET
      See: 03_auth_identity.md — PushActionToken section
   b. Build push payload JSON
   c. Encrypt with webpush-go (p256dh + auth keys)
   d. POST to push_endpoint with VAPID signature, Urgency: high
      (Urgency: high is mandatory for Android FCM Doze mode bypass)
   e. On FCM 410 Gone:
      UPDATE staff_members SET push_enabled=false, push_endpoint=NULL,
        push_p256dh=NULL, push_auth=NULL WHERE id=$1
      INSERT notification_events (channel='web_push', status='failed', error='410_gone')
   f. On 2xx:
      INSERT notification_events (
        channel='web_push', notification_type='push_call_next',
        tenant_id, location_id, customer_id=NULL, recipient_phone=NULL,
        source_type='staff_member', source_id=staff_member.id, status='sent'
      )
 
5. UPDATE outbox_event.status='dispatched'
```
 
**410 Gone cleanup order:** Disable subscription first, log second. If process crashes
between these steps, disabling first prevents wasted FCM calls on retry. Never log
before disabling.
 
**Delivery receipts:** FCM provides no delivery receipt. `notification_events.status`
for `web_push` rows only ever reaches `'sent'`. Never `'delivered'`. Do not add
delivery receipt polling.
 
---
 
## Feedback Request Scheduler
 
`type='feedback_request.schedule'` in outbox is processed by `feedback_scheduler.go`:
 
```
1. Find visit_id from payload
2. Find customer phone number
3. Check: does feedback_request already exist for this visit? If yes, skip.
4. INSERT feedback_requests (visit_id, expires_at=NOW()+1h)
5. INSERT outbox_event: type='notification.send', template='bb_service_feedback'
   process_after=NOW() (send immediately from the feedback worker perspective)
6. COMMIT
```
 
---
 
## Background Jobs (Go goroutines)
 
| Job | File | Trigger | Key behavior |
|---|---|---|---|
| Watchdog | `jobs/watchdog.go` | 60-second ticker | Near-turn, auto-snooze, stale warnings |
| End-of-day | `jobs/end_of_day.go` | 2h after each location's closing_time | Expire waiting/called/skipped, transition in_progress to needs_review |
| Weekly summary | `jobs/weekly_summary.go` | Sunday 10 PM cron | Aggregate per active tenant → outbox |
 
### Multi-Node Safety (future horizontal scaling)
 
Outbox and webhook workers are safe to run on every node — `SKIP LOCKED` guarantees each row
is claimed by exactly one worker. The singleton, time-driven jobs (watchdog tick, end-of-day,
weekly summary) would duplicate work across nodes; guard each with a PostgreSQL advisory lock
so only one node runs a given tick:
 
```go
if acquired, _ := db.Exec("SELECT pg_try_advisory_lock($1)", jobKey); acquired {
    runJob()
}
```
 
PostgreSQL-native. No Redis, no broker. On a single node the lock always succeeds — behavior
is unchanged today.
