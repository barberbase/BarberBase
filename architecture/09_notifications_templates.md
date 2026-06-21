# Purpose
Complete specification of all 12 WhatsApp message templates registered on BarberBase's WABA via Bhejna. Includes exact body copy, button definitions, parameter mapping, trigger conditions, cost, and registration priority.
 
# Use This File When
- Registering templates in Bhejna portal
- Implementing outbox worker payload construction
- Checking which parameters a template expects
- Determining which quota bucket a template belongs to
# Do Not Use This File For
- How the outbox worker sends (→ `07_webhooks_outbox_workers.md`)
- Bhejna API payload shape (→ `06_bhejna_whatsapp.md`)
- Quota enforcement logic (→ `07_webhooks_outbox_workers.md`)
# Related Files
- `06_bhejna_whatsapp.md` — sending API shape
- `07_webhooks_outbox_workers.md` — outbox worker and quota
- `15_critical_laws.md` — Law 13 (23-hour expiry)
# Source Of Truth Priority
This file for template specifications. `001_complete_schema.sql` for `notification_events.template_code` values.
 
---
 
## Template Registration Priority
 
Register all templates in Bhejna's template manager. Submit for Meta approval (utility ~24h; marketing longer).
 
| Priority | template_code | Category | Must Be Live |
|---|---|---|---|
| 1 | `bb_queue_joined` | UTILITY | Go-live |
| 2 | `bb_near_turn` | UTILITY | Go-live |
| 3 | `bb_you_are_next` | UTILITY | Go-live |
| 4 | `bb_service_feedback` | UTILITY | Go-live |
| 5 | `bb_staff_otp` | AUTHENTICATION | Go-live |
| 6 | `bb_queue_cancelled` | UTILITY | Go-live |
| 7 | `bb_queue_snoozed` | UTILITY | Go-live |
| 8 | `bb_shop_closing_early` | UTILITY | Go-live |
| 9 | `bb_appointment_confirmed` | UTILITY | Appointment feature |
| 10 | `bb_appointment_reminder` | UTILITY | Appointment feature |
| 11 | `bb_weekly_summary` | UTILITY | Weekly summary cron |
| 12 | `bb_marketing_broadcast` | MARKETING | Phase 2 campaigns |
 
Templates 1–8 must be ACTIVE before BarberBase serves any shop.
 
---
 
## URL Button Pattern
 
Static base URL configured in template manager. Dynamic suffix passed at send time.
 
```
Template static URL:   https://barberbase.in/q/status?t=
Dynamic suffix (send): eyJhbGciOiJIUzI1NiJ9...
Customer sees:         https://barberbase.in/q/status?t=eyJhbGciOiJIUzI1NiJ9...
```
 
In Go: generate full signed token, pass everything after `?t=` as the button parameter.
 
---
 
## Template 1: `bb_queue_joined`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h customer-initiated session)
**Trigger:** Immediately after queue_entry created (outbox from webhook worker)
**Quota:** `whatsapp_transactional`
 
```
Header: ✂️ You're in the Queue!
 
Body:
You're in the queue at {{1}}!
🎫 Token: #{{2}}
👥 People ahead: {{3}}
⏱ Est. wait: ~{{4}} minutes
We'll notify you when it's almost your turn.
 
Footer: BarberBase
 
Button 1 — URL: "Check My Status"
  Static: https://barberbase.in/q/status?t=  Dynamic suffix: ENABLED
Button 2 — QUICK_REPLY: "Cancel My Spot"
  Payload: CANCEL:{{6}}
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | token_number | "18" |
| {{3}} | people_ahead | "6" |
| {{4}} | estimated_wait_minutes | "45" |
| Button 1 suffix | magic_link_token | "eyJhbGci..." |
| {{6}} | queue_entry_id (UUID) | "019..." |
 
---
 
## Template 2: `bb_near_turn`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** Watchdog — `people_ahead ≤ notify_when_people_ahead` OR `wait ≤ notify_when_wait_minutes`
**Side effect:** Sets `presence_state → notified`, `near_turn_notified_at = NOW()`
**Quota:** `whatsapp_transactional`
 
```
Header: ⚡ Almost Your Turn!
 
Body:
Your turn is coming soon at {{1}}!
👥 Only {{2}} people ahead
⏱ Est. wait: ~{{3}} minutes
Start heading over now. Don't lose your spot!
 
Footer: BarberBase
 
Button 1 — QUICK_REPLY: "I'm On My Way 🏃"
  Payload: ON_THE_WAY:{{4}}
Button 2 — URL: "Check Status"
  Static: https://barberbase.in/q/status?t=  Dynamic suffix: ENABLED
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | people_ahead | "2" |
| {{3}} | estimated_wait_minutes | "18" |
| {{4}} | queue_entry_id (UUID) | "019..." |
| Button 2 suffix | magic_link_token | "eyJhbGci..." |
 
---
 
## Template 3: `bb_you_are_next`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** After call-next transaction commits (customer has channel=whatsapp)
**Quota:** `whatsapp_transactional`
 
```
Header: 🔔 It's Your Turn!
 
Body:
You're next at {{1}}!
🎫 Token: #{{2}}
Please come to the counter now. Your barber is ready.
 
Footer: BarberBase
 
Button 1 — URL: "I'm Here — Check In"
  Static: https://barberbase.in/q/status?t=  Dynamic suffix: ENABLED
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | token_number | "18" |
| Button 1 suffix | magic_link_token | "eyJhbGci..." |
 
Note: URL opens magic link page. If presence is still `on_the_way`, page shows PIN entry form.
 
---
 
## Template 4: `bb_service_feedback`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** `feedback_request.schedule` outbox fires 30 min after visit completed
**Quota:** `whatsapp_transactional`
 
```
Header: ⭐ How Was Your Experience?
 
Body:
{{1}} just served you at {{2}}.
How would you rate your experience?
Tap a button or reply 1–5 (1 = Poor, 5 = Excellent).
 
Footer: BarberBase
 
Button 1 — QUICK_REPLY: "⭐⭐⭐⭐⭐ Excellent"  Payload: RATING:5:{{3}}
Button 2 — QUICK_REPLY: "⭐⭐⭐ Average"       Payload: RATING:3:{{3}}
Button 3 — QUICK_REPLY: "⭐ Poor"             Payload: RATING:1:{{3}}
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | staff_name (assigned barber) | "Rahul" |
| {{2}} | shop_name | "Star Salon" |
| {{3}} | visit_id (UUID) | "019..." |
 
Webhook worker also classifies a plain typed `"1"` through `"5"` reply as a rating.
 
---
 
## Template 5: `bb_staff_otp`
 
**Category:** AUTHENTICATION | **Cost:** ~₹0.15 per OTP
**Trigger:** Staff requests login OTP
**Quota:** `sms_otp` (tracked separately)
 
```
Body:
{{1}} is your BarberBase login code.
Valid for 5 minutes. Do not share this with anyone.
 
Footer: BarberBase
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | otp_code | "847291" |
 
OTP: 6 digits from `crypto/rand`. Stored as bcrypt hash. Expires 5 min.
Send via Bhejna `type: "text"` if accepted; otherwise use template.
 
---
 
## Template 6: `bb_appointment_confirmed`
 
**Category:** UTILITY | **Cost:** ₹0 if within 24h window, else ~₹0.14
**Trigger:** After appointment created
**Quota:** `whatsapp_transactional`
 
```
Header: ✅ Appointment Confirmed
 
Body:
Your appointment at {{1}} is confirmed!
📅 {{2}}
🕐 {{3}}
✂️ {{4}}
⏱ Duration: ~{{5}} minutes
We'll send you a reminder the day before.
 
Footer: BarberBase
 
Button 1 — URL: "View Appointment"
  Static: https://barberbase.in/q/appointment?t=  Dynamic suffix: ENABLED
Button 2 — QUICK_REPLY: "Cancel Appointment"
  Payload: CANCEL_APT:{{7}}
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | appointment_date | "Tuesday, June 10" |
| {{3}} | appointment_time | "3:00 PM" |
| {{4}} | services_summary | "Haircut + Beard Trim" |
| {{5}} | total_duration_minutes | "40" |
| Button 1 suffix | magic_link_token | "eyJhbGci..." |
| {{7}} | appointment_id (UUID) | "019..." |
 
---
 
## Template 7: `bb_appointment_reminder`
 
**Category:** UTILITY | **Cost:** ~₹0.14 (business-initiated, outside 24h window)
**Trigger:** outbox_event fires at 6 PM the day before the appointment
**Quota:** `whatsapp_transactional`
 
```
Header: 📅 Appointment Tomorrow
 
Body:
Reminder: Your appointment at {{1}} is tomorrow!
🕐 Time: {{2}}
✂️ {{3}}
📍 {{4}}
See you then!
 
Footer: BarberBase
 
Button 1 — URL: "View Details"
  Static: https://barberbase.in/q/appointment?t=  Dynamic suffix: ENABLED
Button 2 — QUICK_REPLY: "Cancel"
  Payload: CANCEL_APT:{{6}}
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | appointment_time | "3:00 PM" |
| {{3}} | services_summary | "Haircut + Beard Trim" |
| {{4}} | location_address | "12, MG Road, Koramangala" |
| Button 1 suffix | magic_link_token | "eyJhbGci..." |
| {{6}} | appointment_id (UUID) | "019..." |
 
---
 
## Template 8: `bb_weekly_summary`

**Category:** UTILITY | **Cost:** ~₹0.14 (business-initiated)
**Trigger:** Sunday 10 PM IST cron → sent to `tenants.owner_phone_number`
**Quota:** `whatsapp_transactional`
**Sender class:** PLATFORM — always sent from the BarberBase platform WABA regardless of location `whatsapp_mode` (see C7.1 / sender_class.go).

> **Variable numbering is per-component.** Header, body, and button each have their own `{{1}}` sequence; they are NOT one shared 1..n run. The send payload reflects this — separate `parameters[]` arrays per component block.

```
Header: 📊 Weekly Report — {{1}}          (header {{1}} = week_range)

Body:
Here's how *{{1}}* did this week:         (body {{1}} = shop_name)
💰 Revenue ₹*{{2}}* · ✂️ *{{3}}* customers
⭐ Avg rating *{{4}}*/5 · ⏱ Avg wait *{{5}}* min
❌ No-shows: *{{6}}*

{{7}} — see full report below

Footer: BarberBase

Button 1 — URL: "View Full Report"
  Static: https://barberbase.in/admin/analytics?t=  Dynamic suffix: ENABLED
```

**Header component** — 1 parameter:

| # | Field | Example |
|---|---|---|
| {{1}} | week_range | "May 26 – Jun 1" |

**Body component** — 7 parameters (week_range is NOT repeated here; it lives in the header only):

| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | total_revenue_formatted | "12,450" |
| {{3}} | total_visits | "87" |
| {{4}} | avg_rating | "4.3" — renders "–" when no feedback that week (code emits "–", not "N/A") |
| {{5}} | avg_wait_minutes | "22" |
| {{6}} | no_show_count | "4" |
| {{7}} | highlight_text | "🏆 Best day: Sunday (28 customers)!" |

**Button component** (index 0) — 1 parameter:

| # | Field | Example |
|---|---|---|
| Button 1 suffix | owner_session_token | "eyJhbGci..." |

**Outbox payload `components` (built by `internal/jobs/weekly_summary.go`):**
```json
"components": [
  { "type": "header", "parameters": [ week_range ] },
  { "type": "body",   "parameters": [ shop_name, total_revenue_formatted, total_visits,
                                      avg_rating, avg_wait_minutes, no_show_count, highlight_text ] },
  { "type": "button", "sub_type": "url", "index": 0, "parameters": [ owner_session_token ] }
]
```

**Law 14:** Weekly summary ships in Phase 1. Owner retention mechanism.

**Amended 2026-06-21 (commit 8543ba8):** Header and body variables renumbered to independent per-component sequences (previously documented as a shared `{{1}}`–`{{8}}` run). Body reduced 8 → 7 parameters — `week_range` moved to the header component only. Reflects shipped code after the param-count fix.
 
---
 
## Template 9: `bb_marketing_broadcast`
 
**Category:** MARKETING | **Cost:** ~₹0.90 per conversation
**Trigger:** Manual campaign from owner dashboard (Phase 2 UI)
**Quota:** `whatsapp_marketing` — HARD BLOCKED at 100/month (₹299 plan). Never affects transactional.
 
```
Header: 🎉 {{1}} — Special Offer
 
Body:
{{2}}
{{3}}
 
Footer: BarberBase
 
Button 1 — URL: "Book Now"
  Static: https://barberbase.in/  Dynamic suffix: ENABLED (location_slug)
Button 2 — QUICK_REPLY: "Not Interested"
  Payload: OPT_OUT_MARKETING
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | offer_headline | "Tuesday Special: 20% off!" |
| {{3}} | offer_details | "Valid today only, 10 AM–4 PM." |
| Button 1 suffix | location_slug | "star-salon/koramangala" |
 
---
 
## Template 10: `bb_queue_cancelled`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** Customer or staff cancels a waiting/called entry
**Quota:** `whatsapp_transactional`
 
```
Body:
Your spot at {{1}} has been cancelled.
Token #{{2}} has been removed from the queue.
Want to rejoin? Visit: https://barberbase.in/{{3}}
 
Footer: BarberBase
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | token_number | "18" |
| {{3}} | location_slug | "star-salon/koramangala" |
 
---
 
## Template 11: `bb_queue_snoozed`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** Watchdog auto-snoozes remote customer whose turn arrived
**Quota:** `whatsapp_transactional`
 
```
Body:
You missed your turn at {{1}} (Token #{{2}}).
Your spot has been paused. Please visit the shop and ask staff to reactivate your token.
 
Footer: BarberBase
 
Button 1 — URL: "View My Status"
  Static: https://barberbase.in/q/status?t=  Dynamic suffix: ENABLED
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | token_number | "18" |
| Button 1 suffix | magic_link_token | "eyJhbGci..." |
 
---
 
## Template 12: `bb_shop_closing_early`
 
**Category:** UTILITY | **Cost:** ₹0 (within 24h session)
**Trigger:** Staff closes shop with "expire_remaining" when waiting customers exist
**Quota:** `whatsapp_transactional`
 
```
Body:
{{1}} has closed early today.
Sorry, your queue spot (Token #{{2}}) could not be served.
Visit us again soon: https://barberbase.in/{{3}}
 
Footer: BarberBase
```
 
| # | Field | Example |
|---|---|---|
| {{1}} | shop_name | "Star Salon" |
| {{2}} | token_number | "18" |
| {{3}} | location_slug | "star-salon/koramangala" |
 
---
 
## Cost Summary
 
| Bucket | Templates | Monthly cost/shop |
|---|---|---|
| transactional (queue flow) | 1,2,3,4,6,7,8,10,11,12 | ~₹55–68 total |
| authentication (OTP) | 5 | ~₹0.15/staff/month |
| appointment notifications | 6,7 | ~₹0.14/appointment |
| weekly summary | 8 | ~₹0.14/week |
| marketing | 9 | ~₹0.90/conversation |
 
Gross margins per plan: ₹299 → ~77% | ₹599 → ~87% | ₹999 → ~91%
