# Purpose
The single most important file for queue correctness. Documents the mandatory FOR UPDATE locking pattern, the mutation template all queue operations must follow, the 14-step CompleteVisitAndCheckout transaction (with push trigger at step 12.5), the booking resolver, and the arrival PIN system.
 
# Use This File When
- Implementing any queue mutation
- Implementing CompleteVisitAndCheckout
- Implementing call-next, start, skip, cancel, or reactivate
- Debugging a race condition
- Reviewing a transaction for correctness
# Do Not Use This File For
- Which transitions are valid (→ `04_queue_state_machine.md`)
- SSE broadcast mechanics (→ `08_sse_realtime.md`)
- Outbox worker internals (→ `07_webhooks_outbox_workers.md`)
- Push dispatch handler logic (→ `16_web_push_service_worker.md`)
# Related Files
- `001_complete_schema.sql` — `queue_sessions`, `queue_entries`, `visits` tables
- `04_queue_state_machine.md` — valid transitions
- `15_critical_laws.md` — Laws 1, 2, 7, 8, 10
- `16_web_push_service_worker.md` — web_push.send outbox event (step 12.5); dispatch handler
# Source Of Truth Priority
`001_complete_schema.sql` for schema constraints. `openapi.yaml` for API shapes. This file for transaction design.
 
---
 
## The Single Most Important Rule
 
**Every queue mutation MUST lock `queue_sessions` FOR UPDATE first.**
 
```sql
SELECT id, queue_version, last_token_number, status
FROM queue_sessions
WHERE location_id = $1 AND business_date = $2
FOR UPDATE;
```
 
This is the serialization point. Without it, two concurrent mutations can corrupt `queue_version`, `last_token_number`, or state.
 
`lock_timeout=1s` is set in PostgreSQL. Design mutations to complete fast.
 
---
 
## SKIP LOCKED Rule
 
`SKIP LOCKED` is ONLY for `webhook_events` and `outbox_events` workers. **Never for queue mutations.**
 
Queue mutations use plain `FOR UPDATE` (blocking). If the lock cannot be acquired within `lock_timeout`, the operation fails with a retriable error. The client retries.
 
---
 
## Mutation Template
 
Every queue mutation follows this exact structure:
 
```
BEGIN
  SELECT queue_session FOR UPDATE          ← always first, always blocking
  Domain validation                         ← check state machine validity
  Apply mutation                            ← state change on entry/visit/staff
  queue_version++                           ← always increment on any mutation
  INSERT outbox_event if needed             ← inside the transaction
COMMIT
After commit: SSE broadcast                 ← AFTER commit, never before
```
 
**Law 7:** Outbox events are inserted inside transactions.
**Law 8:** SSE broadcast happens AFTER COMMIT. Never inside the transaction.
 
> First-join caveat: if the session row may not exist yet (first join of the day), run the
> idempotent `INSERT ... ON CONFLICT DO NOTHING` immediately before `SELECT ... FOR UPDATE`.
> See "Queue Session Auto-Create". "Lock first" means lock first within the critical section,
> after ensuring the row exists — you cannot FOR UPDATE a row that doesn't exist yet.
 
---
 
## Call Next Transaction
 
```
POST /v1/staff/queue/call-next (StaffJWT)
 
Read location.queue_routing_mode (from JWT location_id).
 
BEGIN
  1. Lock queue_session FOR UPDATE
  2. SELECT queue_entry WHERE:
       state = 'waiting'
       AND is_dispatchable = true
       AND presence_state = 'arrived'      ← arrived customers first
       AND <routing_filter below>
       ORDER BY priority_group ASC, sort_key ASC, token_number ASC
       LIMIT 1
       FOR UPDATE
 
  Routing filter by queue_routing_mode:
    pooled:
      (no additional filter — any barber takes the globally-next arrived customer)
 
    hybrid:
      AND (requested_barber_id = $jwt_barber_id OR requested_barber_id IS NULL)
      -- Calling barber gets customers who requested them or had no preference.
      -- Customers who requested a different barber wait for that barber.
 
    barber_specific:
      AND requested_barber_id = $jwt_barber_id
      -- Only customers who explicitly chose this barber are dispatched to them.
      -- If the entry's requested barber is unavailable (offline/inactive), the
      -- entry will not be dispatched by anyone until the barber returns or a
      -- manager uses /reassign to redirect it.
 
  3. Validate entry found (else 404 with waiting_remote_count)
  4. SET state = 'called', called_at = NOW()
  5. SET assigned_barber_id = (barber from JWT)
  6. UPDATE staff_member: status = 'cutting'
  7. INSERT outbox_event: bb_you_are_next
  8. queue_version++
COMMIT
→ SSE broadcast
```
 
If no `arrived` customers match the routing filter: return 404 with
`waiting_remote_count` (count of `is_dispatchable=true AND state='waiting'` entries
not yet arrived, filtered by the same routing clause).
 
---
 
## Direct Start Transaction
 
```
POST /v1/staff/queue/entries/{id}/start (StaffJWT)
 
Normal path (state=called):
  BEGIN
    Lock queue_session FOR UPDATE
    Lock queue_entry FOR UPDATE → validate state = 'called'
    SET state = 'in_progress', started_at = NOW()
    UPDATE staff_member: status = 'cutting'
    queue_version++
  COMMIT → SSE broadcast
 
Direct start path (state=waiting, presence=arrived):
  BEGIN
    Lock queue_session FOR UPDATE
    Lock queue_entry FOR UPDATE → validate state = 'waiting' AND presence = 'arrived'
    SET state = 'in_progress', called_at = NOW(), started_at = NOW()  ← atomic
    UPDATE staff_member: status = 'cutting'
    queue_version++
  COMMIT → SSE broadcast
 
Error if state = 'waiting' AND presence != 'arrived': 422
```
 
---
 
## CompleteVisitAndCheckout (14 Steps + Push Trigger)
 
```
POST /v1/staff/queue/entries/{entry_id}/complete (StaffJWT)
Body: CheckoutRequest { queue_entry_id, payment_lines, discount_amount_paise, product_line_items }
 
All-or-nothing transaction:
BEGIN
  1.  Lock queue_session FOR UPDATE
  2.  Lock queue_entry FOR UPDATE → validate state = 'in_progress'
  3.  Lock visit FOR UPDATE
  4.  Validate SUM(payment_lines.amount_paise) == subtotal_paise - discount_amount_paise
  5.  INSERT visit_charges (total_paise, discount_paise, finalized=true)
  6.  INSERT visit_charge_line_items (services — from visit_services snapshot)
  7.  INSERT visit_charge_line_items (products — from product_line_items in request)
  8.  INSERT visit_payments (one row per payment_line: cash/card/upi)
  9.  UPDATE queue_entry: state='completed', completed_at=NOW(), is_dispatchable=false
  10. UPDATE visit: status='completed', completed_at=NOW()
  11. UPDATE customer: last_visit_at=NOW(), visit_count++, lifetime_value_paise+=total
  12. UPDATE staff_member: status='idle'
 
  12.5 [PUSH TRIGGER — conditional, Staff PWA]:
       IF EXISTS (
         SELECT 1 FROM staff_members
         WHERE location_id = $location_id
           AND push_enabled = true
           AND is_active = true
         LIMIT 1
       ):
         INSERT outbox_events (
           type = 'web_push.send',
           tenant_id = $tenant_id,
           payload = '{"location_id": "$location_id", "tenant_id": "$tenant_id"}',
           process_after = NOW()
         )
       -- The outbox dispatch handler (not this transaction) decides whether to
       -- actually send push — it applies the frequency gate (Law 19) at dispatch
       -- time by checking for arrived dispatchable entries.
       -- Inserting here satisfies Law 7: outbox inside transaction.
       -- The EXISTS check avoids inserting unnecessary outbox rows for locations
       -- where no staff member has push enabled (typical before PWA adoption).
 
  13. INSERT outbox_event: type='feedback_request.schedule',
        process_after=NOW()+30min
  14. queue_version++
COMMIT
→ SSE broadcast
```
 
**Snapshots are immutable (Law 10).** `visit_services` and `visit_charge_line_items` are written once and never modified.
 
### Push Trigger Notes
 
- Step 12.5 fires inside the same transaction as all other steps (Law 7 satisfied).
- The push trigger inserts an outbox event, not a direct FCM call. The outbox worker
  handles actual delivery asynchronously.
- If the transaction rolls back for any reason (payment validation failure, lock
  timeout, etc.), the outbox event is never inserted — no orphan push events.
- The frequency gate (Law 19) lives in the outbox dispatch handler, not here. The
  transaction stays fast. A push outbox event does not guarantee a push will be sent;
  it signals that the dispatch handler should evaluate current queue state.
- Push trigger does NOT affect queue correctness. If push infrastructure is disabled
  entirely, CompleteVisitAndCheckout behaves identically (Law 21).
See: `16_web_push_service_worker.md` — "Outbox Dispatch Flow (web_push.send)"
 
---
 
## Booking Resolver
 
```
POST /v1/public/locations/{id}/booking-options
Input: variant_ids[], party_size
 
1. total_duration = Σ(variant.duration_minutes) × party_size
2. allowed_modes determination:
   - ANY variant with requires_appointment=true → only 'appointment'
   - ALL variants allow_walk_in=true → include 'walk_in'
   - ALL variants allow_appointment=true → include 'appointment'
3. current_time + total_duration > closing_time + allow_overtime_minutes → remove 'walk_in'
4. queue_length >= max_total_queue_size → remove 'walk_in'
5. location.operation_mode narrows further
6. allowed = [] → return blocked_reason
```
 
Returns: `total_duration_minutes`, `total_price_paise`, `allowed_modes[]`, `queue_length`, `estimated_wait_minutes`, optionally `blocked_reason`.
 
---
 
## Arrival PIN System
 
Static 4-digit PIN per location. Printed on counter card/QR standee. Permanent until owner regenerates.
 
```
POST /v1/queue/confirm-arrival  (CustomerSession)
Body: { method: "pin"|"geolocation"|"staff", pin?, latitude?, longitude?, accuracy_metres? }
```
 
### method=pin
- Rate limit: 5 attempts per queue_entry, 10 per IP per hour (`x/time/rate`)
- `bcrypt.CompareHashAndPassword(location.arrival_pin_hash, input_pin)`
- Match → `presence='arrived'`, `arrived_at=NOW()`, `is_dispatchable=true`, SSE ping
### method=geolocation
- `accuracy_metres > 150` → 422 "Too inaccurate, use PIN"
- `haversine(lat, lng, shop_lat, shop_lng) ≤ arrival_radius_metres` → `presence='arrived'`
- Else → 422
### method=staff
- Requires StaffJWT (not CustomerSession)
- Bypasses PIN. Sets `presence='arrived'`.
- Logged in `arrival_attempts` with `method='staff'`
---
 
## Queue Session Auto-Create
 
On the first join of a business date no `queue_session` row exists yet, so `SELECT ... FOR UPDATE`
locks zero rows. The race-free ordering is upsert-then-lock inside the same transaction:
 
```sql
-- 1. Ensure the row exists (idempotent; concurrent first-joiners converge on one row)
INSERT INTO queue_sessions (tenant_id, location_id, business_date,
                            status, queue_version, last_token_number)
VALUES ($1, $2, $3, 'active', 0, 0)
ON CONFLICT (location_id, business_date) DO NOTHING;
 
-- 2. Row now guaranteed to exist — take the serialization lock
SELECT id, queue_version, last_token_number, status
FROM queue_sessions
WHERE location_id = $2 AND business_date = $3
FOR UPDATE;
```
 
Two concurrent first-joiners both attempt the INSERT; one succeeds, one hits the conflict and
does nothing. Both then `SELECT ... FOR UPDATE` and serialize from there. Locking before the
upsert would lock zero rows and leave `last_token_number` exposed to a race.
 
---
 
## Idempotency Keys
 
`POST /v1/queue/join` and `POST /v1/appointments/book` accept `idempotency_key` (UUIDv4 from client).
 
Insert the key first inside the business transaction and let the UNIQUE constraint serialize
duplicates — do not SELECT-then-act (two duplicate taps on flaky 4G both read "not found"
and both execute):
 
```sql
INSERT INTO idempotency_keys (tenant_id, key, endpoint, created_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (tenant_id, key, endpoint) DO NOTHING
RETURNING id;
```
- No row returned → key already in flight or complete. Read the stored `response_status`/
  `response_body` and return it. If response not yet written, the original request is still
  running → return 409 / retry-after.
- Row returned → first request. Execute, then UPDATE the same row with `response_status`/
  `response_body` before COMMIT.
`visits.idempotency_key` and `appointments.idempotency_key` (both UNIQUE) are the second line
of defense: even if the key table is bypassed, a duplicate visit/appointment insert fails.
 
Stored response expires after 24 hours.
