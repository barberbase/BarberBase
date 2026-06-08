# Purpose
Defines all valid state transitions for queue_entries and presence_state. The authoritative reference for what transitions are legal, when, and by whom.
 
# Use This File When
- Implementing any queue state transition
- Checking whether a transition is valid
- Implementing watchdog logic (auto-snooze, end-of-day expiry)
- Understanding is_dispatchable rules
# Do Not Use This File For
- How transitions are locked and committed (→ `05_queue_locking_transactions.md`)
- The outbox events fired on transitions (→ `07_webhooks_outbox_workers.md`)
- API endpoint shapes (→ `openapi.yaml`)
# Related Files
- `001_complete_schema.sql` — `queue_entries` table, CHECK constraints
- `05_queue_locking_transactions.md` — how transitions execute
- `07_webhooks_outbox_workers.md` — outbox events triggered by transitions
- `15_critical_laws.md` — Law 1 (lock first), Law 6 (arrived cannot be self-declared)
# Source Of Truth Priority
`001_complete_schema.sql` CHECK constraints are the ground truth for valid state values. This file defines the valid transition graph.
 
---
 
## queue_entry State Machine
 
### Valid States
 
`waiting` | `called` | `in_progress` | `completed` | `skipped` | `no_show` | `cancelled` | `expired`
 
Terminal states (no further transitions): `completed`, `no_show`, `cancelled`, `expired`
 
### Transition Table
 
| From | To | Trigger | Actor |
|---|---|---|---|
| `waiting` | `called` | Call Next | Staff tap |
| `waiting` | `in_progress` | Direct Start | Staff tap — ONLY if `presence=arrived` |
| `waiting` | `cancelled` | Cancel | Customer or Staff |
| `waiting` | `skipped` | Skip | Staff |
| `waiting` | `expired` | End-of-day job | System only |
| `called` | `in_progress` | Start | Staff tap |
| `called` | `no_show` | Mark No-Show | Staff tap (terminal) |
| `called` | `skipped` | Skip back | Staff |
| `called` | `expired` | End-of-day job | System only |
| `in_progress` | `completed` | CompleteVisitAndCheckout | Staff tap |
| `in_progress` | `needs_review` | End-of-day flag | System (not auto-expired) |
| `skipped` | `waiting` | Reactivate | Staff |
| `skipped` | `expired` | End-of-day job | System only |
 
### Direct Start Rule
 
`waiting → in_progress` is only allowed when `presence_state = arrived`. This is a hard guard, not a soft suggestion. The handler MUST validate presence before executing the transition.
 
---
 
## presence_state Machine
 
Tracks physical location, separate from queue position.
 
### Valid States
 
`remote` | `notified` | `on_the_way` | `arrived` | `snoozed` | `unknown`
 
- `unknown`: anonymous walk-in, physical presence unclear
### Transition Table
 
| From | To | Trigger | Notes |
|---|---|---|---|
| `remote` | `notified` | Watchdog sends near-turn WhatsApp | Sets `near_turn_notified_at` |
| `notified` | `on_the_way` | Customer taps button, Bhejna webhook fires | Self-declared — OK |
| `on_the_way` | `arrived` | PIN + rate limit OR GPS ≤100m OR staff tap | Physical verification required |
| `remote` | `snoozed` | Auto-snooze (see below) | System |
| `notified` | `snoozed` | Auto-snooze (see below) | System |
| `snoozed` | `arrived` | Staff reactivates | Staff action |
 
### Arrived Cannot Be Self-Declared
 
`arrived` requires one of:
- PIN: customer enters static location PIN (bcrypt verified, rate-limited)
- GPS: browser geolocation ≤100m of shop (accuracy must be ≤150m)
- Staff: staff taps "Mark Arrived" (StaffJWT, bypasses PIN)
**Law 6:** `arrived` cannot be self-declared. Physical verification always.
 
---
 
## is_dispatchable
 
Boolean flag on `queue_entries`. Controls "Call Next" eligibility.
 
```
is_dispatchable = FALSE when:
  presence_state = 'snoozed'
  OR state IN ('skipped', 'no_show', 'cancelled', 'expired', 'completed', 'needs_review')
 
is_dispatchable = TRUE:
  On creation (walk-in or WhatsApp join)
  When staff reactivates a skipped or snoozed entry
 
"Call Next" query filter:
  WHERE is_dispatchable = true AND state = 'waiting'
  (plus barber routing filter from JWT)
```
 
**Law 12:** `is_dispatchable` is the dispatch gate. Never bypass it.
 
---
 
## Auto-Snooze Logic
 
Executed by watchdog (60-second ticker):
 
When a `remote` or `notified` entry is next in dispatch order AND no `arrived` customer is ahead:
```
UPDATE queue_entry SET presence_state='snoozed', is_dispatchable=false
INSERT outbox_event: bb_queue_snoozed
```
 
The queue does not pause. The next `arrived` customer is called instead.
 
---
 
## Shop Status Machine
 
Governs whether new joins and dispatches are permitted.
 
| From | To | Trigger |
|---|---|---|
| `open` | `closing_soon` | Watchdog (configurable threshold before closing_time) |
| `closing_soon` | `closed` | Watchdog or manual |
| `open` | `temporarily_closed` | Staff manual, with `expires_at` |
| `temporarily_closed` | `open` | `expires_at` reached or staff re-opens |
| any | `closed` | Manual staff override |
 
**No-show timers PAUSE during `temporarily_closed`.**
 
---
 
## Watchdog Stale Warnings (60-second ticker)
 
Stale `called`:
- >5 min → `stale_warning='called_warning'`
- >10 min → `stale_warning='called_critical'`
Stale `in_progress`:
- >estimated_duration+10 min → `stale_warning='in_progress_warning'`
- >estimated_duration+15 min → INSERT outbox_event (customer WhatsApp confirmation)
- >estimated_duration+25 min → `stale_warning='in_progress_critical'`
End-of-day (2h after closing):
- `UPDATE state='expired' WHERE state IN ('waiting', 'called', 'skipped')`
- `in_progress` entries are never auto-expired. Transition them to the `needs_review` state
  (a valid `queue_entries.state` value) with `is_dispatchable=false`. They await manual staff
  reconciliation and do not appear in dispatch or stale-watchdog queries.
