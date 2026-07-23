# Handoff Package — 2026-07-24

Drafted by Sonnet S3. Five independent deliverables. Each implementation unit is
execution-ready for a Sonnet implementer without further research: every claim
about current code carries a `file:line` citation; anything not directly
verified is marked `[VERIFY]`.

Two `openapi.yaml` copies exist in this repo: `architecture/openapi.yaml` is
the **source of truth** (per `~/.claude/projects/.../openapi-source-of-truth.md`
memory — edit this one; `make gen-api` clobbers `barberbase-core/api/openapi.yaml`
from it). All line citations below are against `architecture/openapi.yaml`
unless stated otherwise.

---

# (A) Appointments End-to-End — Delta Only

## Current-state inventory (verified, not assumed)

| Piece | Status | Evidence |
|---|---|---|
| `POST /appointments/book` (staff-only) | **Shipped** | `barberbase-core/internal/api/handlers_public.go:1422-1500` (`BookAppointment`), transaction in `barberbase-core/internal/domain/queue/booking_resolver.go:231-536` |
| `POST /staff/appointments/{id}/checkin` | **Shipped** | `handlers_public.go:1503-1560`, transaction `booking_resolver.go:538-726`, registered as manual route `handlers_public.go:1394-1395` |
| `GET /appointments/my` (magic-link) | **Shipped** | `handlers_public.go:1573-1650`, generated route `internal/api/generated.go:3350` |
| `POST /appointments/my/cancel` | **Shipped** | `handlers_public.go:1654-1690`, calls `queue.CancelScheduledAppointment` (`internal/domain/queue/appointment_token.go:53-`), also reachable via WhatsApp `CANCEL_APT:` quick-reply payload (`internal/webhook/processor.go:711`) — **both entry points share one function**, no duplicated logic |
| `GET /public/locations/{id}/appointment-slots` | **Shipped** | `handlers_public.go:1254-1390` (`GetAppointmentSlots`), 30-min grid, capacity = active staff count (ponytail comment at `handlers_public.go:1327`) |
| `GET /staff/appointments` | **Shipped** | `internal/api/handlers_staff.go:2700-2790` (`GetStaffAppointments`) |
| Customer-facing `/q/appointment` page | **Shipped**, cancel-only | `barberbase-frontend/src/routes/q/appointment/+page.svelte` — renders appointment, has a full cancel confirm flow (`+page.svelte:44-65, 148-179`). **No reschedule action anywhere in this file.** |
| `bb_appointment_confirmed` outbox send | **Shipped** | Inserted at booking time, `booking_resolver.go:426-465` (template 6, `notification.send` type) |
| `bb_appointment_reminder` outbox send | **Shipped, code-complete** | Inserted at booking time for day-before 6PM (`booking_resolver.go:467-509`), skipped if that instant is already past (`booking_resolver.go:471`). Dispatch: generic `notification.send` handler (`internal/outbox/handlers/notification.go`) — `TemplateToNotificationType` map includes `"bb_appointment_reminder": "appointment_reminder"` at `internal/outbox/handlers/notification.go:56`. Worker registers `"notification.send": n` at `internal/outbox/worker.go:50`. **This is NOT a gap** — the task brief's premise here does not hold; see A1 below for the one real gap (test coverage), not the send path itself. |
| No-show sweep | **Shipped** | `barberbase-core/internal/jobs/end_of_day.go:162-175` — `UPDATE appointments SET status='no_show' WHERE status='scheduled' AND scheduled_start_at < end-of-business-day`, comment confirms this is "the ONLY automatic path for unresponsive appointment customers" |
| Reschedule flow | **MISSING** | `architecture/11_appointments_booking.md:40` labels it "Phase 2 (not implemented)". `grep -rn "reschedule\|Reschedule"` across `barberbase-core/internal` and `barberbase-frontend/src` finds only: the enum comment `internal/api/generated.go:304`, a `default:` case in `internal/webhook/processor.go:700` that treats `no_show`/`rescheduled` identically (dead branch — nothing ever sets `rescheduled`), a UI badge label that's never triggered (`+page.svelte:40`), and an unrelated debounce comment (`status/+page.svelte:103`). **Zero reschedule code paths exist.** |
| Button-tap appointment check-in | **MISSING** | `grep -n "appointment\|Appointment" internal/api/handlers_device.go` returns **zero matches**. `DeviceCallNext` (`handlers_device.go:56`) only ever calls `queue.CallNext` against already-`waiting` entries — it has no notion of a scheduled appointment. There is no hardware-triggered path from `appointments.status='scheduled'` to a queue entry; the only check-in entry point is the StaffJWT dashboard button hitting `POST /staff/appointments/{id}/checkin`. |

## Units

### Unit A1 — Reminder-send test coverage (verify-only, no new send path)
**Depends:** none
**Read:**
- `barberbase-core/internal/domain/queue/booking_resolver.go:426-509` (outbox insert logic, both templates)
- `barberbase-core/internal/outbox/handlers/notification.go:36-56` (`TemplateToNotificationType`, `NotificationPayload.TemplateCode`)
- `barberbase-core/internal/api/handlers_public_test.go:977-1047` (`TestC42_BookAppointment_Idempotency` — existing pattern for asserting on `BookAppointment` side effects)

**Write:**
- `barberbase-core/internal/domain/queue/commands_test.go` (add new test function only; do not touch existing tests) — OR a new file `barberbase-core/internal/domain/queue/booking_resolver_test.go` if `commands_test.go` doesn't already cover booking. Pick whichever file already imports the test harness used by `TestC42_BookAppointment_Idempotency`; do not create a second harness.

**Accept:**
- A new test asserts that after `QueueRepository.BookAppointment`, exactly two `outbox_events` rows exist with `type='notification.send'` and payload `template_code` values `bb_appointment_confirmed` and `bb_appointment_reminder` respectively.
- A second case asserts the day-before-reminder is **skipped** (only the confirmation row exists) when `scheduled_start_at` is within the next 24h (i.e. `localDayBefore` per `booking_resolver.go:470` is already in the past) — this exercises the `if localDayBefore.After(time.Now().In(loc))` guard at `booking_resolver.go:471`.
- A third case asserts `process_after` on the reminder row equals 18:00 location-time the day before `scheduled_start_at`, converted to UTC (`booking_resolver.go:505`, `.UTC()`).

**Test:**
- `go test ./internal/domain/queue/... -run TestAppointmentReminderOutbox -v`
- No behavior change expected — if this test fails, that is a real regression in already-shipped code, not something to "fix" as part of this unit; escalate rather than patch blind.

---

### Unit A2 — FROZEN-GATE: Reschedule endpoint (backend)
**Depends:** none
**FROZEN-GATE: requires human-approved diff.** Touches `architecture/openapi.yaml` (frozen — edit source, not the generated `barberbase-core/api/openapi.yaml` copy) and requires `make gen-api` to regenerate `internal/api/generated.go` (frozen, generated file).

**Read:**
- `barberbase-core/internal/domain/queue/appointment_token.go:53-` (`CancelScheduledAppointment` — the sibling status-transition function; reschedule should follow the same "flip status inside caller's tx" shape)
- `barberbase-core/internal/domain/queue/booking_resolver.go:231-536` (`BookAppointment` — slot-validation logic in Steps 2-5 to reuse for revalidating the new slot)
- `barberbase-core/internal/api/handlers_public.go:1654-1690` (`CancelMyAppointment` — handler shape to mirror: resolve token, `tx.Begin`, domain call, respond)
- `architecture/11_appointments_booking.md:34-42` (lifecycle — `'scheduled' → 'rescheduled'` is the only state transition currently undefined)
- `architecture/15_critical_laws.md` (Law 7, Law 8, Law 11 — apply to any new tx path)

**Write:**
- `architecture/openapi.yaml` — new path, exact diff below (insert after the `/appointments/my/cancel` block, currently ending around line 1330-ish per earlier grep at `openapi.yaml:1319-1330`; re-locate by searching for `/appointments/my/cancel:` before inserting)
- `barberbase-core/internal/domain/queue/appointment_token.go` — new function `RescheduleAppointment`
- `barberbase-core/internal/api/handlers_public.go` — new handler `RescheduleMyAppointment`, registered via `RegisterManualRoutes` (`handlers_public.go:1392-1419`) **only if** codegen wiring proves awkward; prefer the generated route from the openapi diff below so the endpoint gets normal `ServerInterfaceWrapper` treatment like `GetMyAppointment`/`CancelMyAppointment` (`generated.go:3350,3353`) — do not hand-wire unless codegen genuinely can't express this shape.
- `barberbase-core/internal/domain/queue/appointment_token_test.go` (new or extend) and `barberbase-core/internal/api/my_appointment_http_test.go` (extend — this file already covers the `/appointments/my` family per its name)
- **Regenerate, do not hand-edit:** `barberbase-core/internal/api/generated.go`, `barberbase-core/api/openapi.yaml` — both via `make gen-api` after the `architecture/openapi.yaml` diff is approved.

**Proposed openapi.yaml diff (for human approval):**
```yaml
  /appointments/my/reschedule:
    post:
      operationId: rescheduleMyAppointment
      summary: Reschedule own appointment via magic-link token
      description: >
        'scheduled' appointment moves to the new scheduled_start_at after
        revalidating business hours and barber-overlap exactly as
        /appointments/book does. Old reminder outbox row (if still pending)
        is cancelled and a new one is scheduled off the new date. Status
        stays 'scheduled' — 'rescheduled' as a terminal status is NOT used
        (that enum value historically meant "moved," but the lifecycle
        doc treats scheduled->rescheduled->scheduled as churn; simplest
        correct model is: reschedule mutates scheduled_start_at in place
        and appointment.status never leaves 'scheduled' until check-in/
        cancel/no-show).
      security:
        - CustomerSession: []
      parameters:
        - name: X-Session-Token
          in: header
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [scheduled_start_at]
              properties:
                scheduled_start_at:
                  type: string
                  format: date-time
      responses:
        '200':
          description: Rescheduled
          content:
            application/json:
              schema:
                type: object
                properties:
                  status: { type: string, enum: [scheduled] }
                  scheduled_start_at: { type: string, format: date-time }
        '401':
          description: Invalid or expired link
        '404':
          description: Appointment not found
        '409':
          description: Not reschedulable (already checked_in/cancelled/no_show), or new slot unavailable
        '422':
          description: New slot fails business-hours/overtime/barber-overlap validation
```

**Design note for the implementer:** the existing `status` enum already has a
`rescheduled` value (`generated.go:304`) that nothing sets. Two legitimate
designs: (1) mutate `scheduled_start_at` in place, keep `status='scheduled'`
(what the diff above assumes — simplest, matches how `GetMyAppointment`'s
`cancellable: status == "scheduled"` check already works unmodified), or
(2) cancel the old row and insert a fresh one with `status='scheduled'`,
leaving the old row `status='rescheduled'` as an audit trail. Option 2 is
more work (touches outbox reminder cleanup, magic-link token, WhatsApp
confirmation resend) for no clear product win — **default to option 1**
unless a human says otherwise; it's the smaller diff and the DB already
tracks these (verified: `appointments.updated_at` exists and is
trigger-maintained — `001_complete_schema.sql:1480` `trg_appointments_updated_at`;
`appointments.cancelled_by VARCHAR(20) CHECK IN ('customer','staff','system')`
exists in the appointments block).

**Accept:**
- `POST /v1/appointments/my/reschedule` with a valid session token and a new `scheduled_start_at` inside business hours and without barber overlap returns `200` with the updated `scheduled_start_at`.
- Reusing `BookAppointment`'s Step 3 (business-hours check, `booking_resolver.go:285-316`) and Step 5 (barber-overlap check, `booking_resolver.go:338-355`) logic — factor these into a shared helper rather than copy-pasting; the reschedule tx must exclude the appointment's own row from the overlap query (`WHERE ... AND id != $current_appointment_id`, unlike the book-time overlap check which has no such row to exclude).
- Attempting reschedule on a `checked_in`/`cancelled`/`no_show` appointment returns `409`.
- New slot violating business hours or overtime returns `422` with the same error strings `BookAppointment` uses (`"shop is closed on this day"`, `"scheduled time is outside business hours"`, `"scheduled time is in the past"` — `handlers_public.go:1479-1480`) for frontend copy reuse.
- The pending `bb_appointment_reminder` outbox row (if `status='pending'` and not yet dispatched) is either updated (`process_after`) or replaced — must not silently double-send at the old time. Query: `UPDATE outbox_events SET process_after = $new_time, payload = $new_payload WHERE tenant_id=$t AND type='notification.send' AND payload->>'source_id'=$apt_id AND payload->>'template_code'='bb_appointment_reminder' AND status='pending'` inside the same tx (Law 7).
- Law 1: lock nothing new here — appointments aren't queue_sessions, no `FOR UPDATE` on `queue_sessions` needed since this never touches a queue entry (appointment is still pre-checkin).

**Test:**
- `go test ./internal/domain/queue/... -run TestRescheduleAppointment -v` — cover: happy path, past-checked-in-appointment rejection, business-hours rejection, barber-overlap rejection (including self-exclusion — reschedule to the exact same slot must NOT self-conflict), reminder-row update.
- `go test ./internal/api/... -run TestRescheduleMyAppointment -v` — HTTP-level: 401 on bad token, 404 on unknown id, 200/409/422 status mapping.

---

### Unit A3 — Reschedule UI on `/q/appointment`
**Depends:** A2 (needs the live endpoint; can be built against a mocked response in parallel but must not merge before A2 ships)
**Read:**
- `barberbase-frontend/src/routes/q/appointment/+page.svelte` (full file — cancel flow at lines 44-65, 148-179 is the pattern to mirror)
- `barberbase-frontend/src/routes/q/appointment/+page.server.ts` (load pattern — token from `?t=`, `X-Session-Token` header)
- `barberbase-core/internal/api/handlers_public.go:1254-1390` (`GetAppointmentSlots` — reuse for a slot picker; response shape: `{date, total_duration_minutes, slots: [{time, estimated_end_time, available, reason_unavailable?}]}`)
- `architecture/openapi.yaml` around `/public/locations/{location_id}/appointment-slots` (grep confirmed present at `openapi.yaml:961`) for the exact query params (`date`, `variant_ids[]`)

**Write:**
- `barberbase-frontend/src/routes/q/appointment/+page.svelte` (add reschedule UI block, same file, alongside the existing cancel block at line 148)
- `barberbase-frontend/src/routes/q/appointment/+page.server.ts` (no server load change needed — reschedule is a client `fetch` exactly like `cancelAppointment()` at `+page.svelte:44`; only touch if slot-fetching needs a server-side proxy, which it does not — the appointment-slots endpoint is public/unauthenticated)
- New Playwright e2e: `barberbase-frontend/src/routes/q/appointment/reschedule.e2e.ts` (or extend an existing appointment e2e file if one exists — `[VERIFY]` none was found in the file listing; `src/routes/q/appointment/` currently has only `+page.server.ts` and `+page.svelte`, no e2e file)

**Accept:**
- `status === 'scheduled'` shows a "Reschedule" button beside "Cancel Appointment" (currently a single full-width cancel button at `+page.svelte:172-177`; make it two buttons in a row, matching the existing `flex gap-2` pattern used in the cancel-confirm step at `+page.svelte:155`).
- Tapping Reschedule shows a day-picker (reuse the location's `appointment-slots` date grid — same variant IDs as the original booking, taken from `apt.services` already loaded) then a time-slot grid; only `available: true` slots are selectable.
- Submitting calls `POST /v1/appointments/my/reschedule` with `X-Session-Token` header (same auth pattern as `cancelAppointment()`, `+page.svelte:48-51`), and on `200` updates the local `apt.scheduled_start_at` reactively (mirror the `status = 'cancelled'` local-state pattern at `+page.svelte:53`) without a full page reload.
- 409/422 errors render inline using the existing `cancelError`-style pattern (`+page.svelte:11, 56-58`), not a browser alert.

**Test:**
- Playwright: load `/q/appointment?t=<valid_token>`, click Reschedule, pick a slot, submit, assert the displayed date/time label updates and the status badge stays "Confirmed".
- Playwright: attempt reschedule on a `checked_in` fixture appointment, assert the Reschedule button is absent (guard identical to the existing `{#if status === 'scheduled'}` block at `+page.svelte:148`).

---

### Unit A4 — Button-tap appointment check-in (backend, hardware-constrained design)
**Depends:** none (independent of A2/A3)

**Design constraint the implementer must respect:** a single hardware button
(per `barberbase-core/migrations/002_station_devices.sql` — `station_buttons`
has no per-press selection UI) cannot let staff *pick which* scheduled
appointment to check in. The only hardware-sane behavior is: pressing the
button checks in **the earliest still-`scheduled` appointment for today at
this button's bound staff/location that falls within a grace window around
now** — if there is exactly one match, check it in; if zero or more than one
match, do nothing useful with a physical button (ambiguous) and the press
should fall through to a normal call-next (existing `DeviceCallNext`
behavior) rather than silently failing. This keeps the device layer simple:
**one endpoint, one deterministic rule, explicit "not applicable" fallthrough**
— do not build a multi-appointment picker onto a $2 button.

**Read:**
- `barberbase-core/internal/api/handlers_device.go` (full file — `DeviceCallNext` at line 56 is the function to extend; the exact response-shape table for it is reproduced verbatim in deliverable (E) below — reuse those exact status/result strings for consistency, don't invent new ones)
- `barberbase-core/internal/domain/queue/booking_resolver.go:538-726` (`CheckInAppointment` — the transaction to invoke, unchanged; do not duplicate its logic, call it)
- `barberbase-core/migrations/002_station_devices.sql` (`station_buttons.staff_member_id` — nullable, NULL = pooled) — **note: migration 002 is NOT auto-applied by `repository.Migrate()`** (`internal/repository/migration.go:14-44` only runs the file path it's given, tests pass `001_complete_schema.sql` only). **Prod status (verified against the repo-root `MIGRATIONS_APPLIED.md` ledger as of 2026-07-24): 002 is applied on dev, PENDING on prod** — applying it manually via psql is a prerequisite for this unit AND for the already-shipped device layer generally; it is part of the deploy checklist, not this unit's diff.
- `barberbase-core/internal/api/handlers_device_test.go` (full file — test patterns to mirror, especially `TestDeviceCallNext_BarberBoundAdvance` at line 137 and the negative-auth-pin pattern at `TestDeviceCallNext_NegativeAuthPins`, line 280)

**Write:**
- `barberbase-core/internal/api/handlers_device.go` — extend `DeviceCallNext` (`handlers_device.go:56`) with a pre-step: before calling `queue.CallNext`, query for exactly-one due scheduled appointment matching the button's `staff_member_id` (or pooled, location-wide, if NULL) within `[now - 20min, now + 20min]` of `scheduled_start_at` (reuse `CheckinGraceMinutes = 20`, `booking_resolver.go:215`, for symmetry with the late-checkin grace already used at `booking_resolver.go:666`). If exactly one match: call `queue.QueueRepository.CheckInAppointment` instead of `queue.CallNext`, return `{"result":"appointment_checked_in","token_number":<int>}` at `200`. Otherwise (0 or 2+ matches): fall through to existing call-next behavior unchanged.
- `barberbase-core/internal/api/handlers_device_test.go` — new test cases only, do not modify existing ones.

**Accept:**
- Exactly one due scheduled appointment for the button's bound staff (or pooled at that location) → device press checks it in via the existing `CheckInAppointment` transaction (Law 1: `queue_sessions FOR UPDATE` already inside that function, `booking_resolver.go:569-577` — reused unchanged, not reimplemented), returns `200 {"result":"appointment_checked_in","token_number":N}`, and fires the same post-commit SSE broadcast pattern already used for the walk-in advance case (`handlers_device.go` success branch, per (E) below).
- Zero or 2+ matching appointments → behaves exactly as `DeviceCallNext` does today (no behavior change to the existing walk-in path — this is additive only).
- All existing `handlers_device_test.go` tests still pass unmodified — this is the acceptance bar for "additive, not a rewrite."
- Rate limiting (`getDeviceRateLimiter`, `handlers_device.go:38-44`, 1/3s per device) applies identically to both branches — do not add a second limiter.

**Test:**
- `go test ./internal/api/... -run TestDeviceCallNext_AppointmentCheckin -v` — cases: exactly-one-match checks in; zero-match falls through to `no_waiting`/`advanced` as appropriate; two-match falls through (documents the deliberate ambiguity punt); grace-window boundary (21 minutes early/late is NOT a match, 19 minutes is).
- Re-run full existing suite: `go test ./internal/api/... -run TestDeviceCallNext -v` (regression gate).

---

# (B) Analytics Shadcn Redesign

## Current state
- Page: `barberbase-frontend/src/routes/admin/analytics/+page.svelte` + `+page.server.ts` — single day snapshot, fetched from `GET /staff/analytics/daily?date=` (`barberbase-core/internal/api/handlers_staff.go:490`, schema `DailyAnalytics` at `architecture/openapi.yaml` — required fields `[business_date, total_visits, total_revenue_paise, barber_breakdown]`).
- Response includes `cancelled_count` (optional int) which the DB/API already returns but the page **never renders** — confirmed absent from `+page.svelte`.
- No date-range/trend endpoint exists — single-day only. A multi-day chart requires either N client-side calls to the existing endpoint or a new backend endpoint (out of scope here — flagged, not built, per "no new chart libs without justification" and to keep this a frontend-only redesign).
- No chart library in `barberbase-frontend/package.json` (`dependencies`: `bits-ui`, `clsx`, `marked`, `qrcode-generator`, `tailwind-merge`, `tailwind-variants` — nothing chart-shaped). The only existing "chart" is a hand-rolled CSS width-bar for barber revenue share (`+page.svelte:124-137`, `bg-gold-accent/30` span with inline `width:X%`).
- shadcn workflow: `barberbase-frontend/src/lib/components/ui/README.md` — fetch via `npx shadcn-svelte@latest add <name>`, then run `barberbase-frontend/scripts/shadcnize.sh src/lib/components/ui/<name>/*.svelte` which remaps shadcn's semantic classes (`bg-primary`, `text-muted-foreground`, etc.) onto DESIGN.md tokens (`bg-gold-accent`, `text-muted`, etc. — full sed mapping in `scripts/shadcnize.sh`). Currently installed: `badge`, `button`, `card`, `label`, `switch`, `table`.
- DESIGN.md tokens relevant to charts: Gold Accent Rule — gold reserved for the single highlighted data point, ≤5% of surface, never a default series color (`DESIGN.md:94`); Tabular Alignment Rule — all numbers use Space Mono (`DESIGN.md:111`); Anti-Halation — no pure black/white, use canvas→matte→surface→titanium for depth (`DESIGN.md:69,146-147`); no gradient text (`DESIGN.md:148`).

## Units

### Unit B1 — Surface `cancelled_count`, restyle stat tiles via shadcn workflow
**Depends:** none
**Read:**
- `barberbase-frontend/src/routes/admin/analytics/+page.svelte` (full file — stat tile grid at lines 73-105)
- `barberbase-frontend/src/lib/components/ui/badge/` (existing component — use for the no-shows/cancelled tiles instead of plain divs, consistent with how `platform/devices/+page.svelte:195-197` already uses `Badge` for status)
- `DESIGN.md` (System colors: `system-warning: #F59E0B`, `system-error: #EF4444` — for no-show/cancelled tile accents; do NOT use `gold-accent` on these per the ≤5%-surface rule already stretched thin by the existing revenue-share bar)

**Write:**
- `barberbase-frontend/src/routes/admin/analytics/+page.svelte` only (no new files — this is a restyle + one new tile, not new components)

**Accept:**
- 5th stat tile added for `data.cancelled_count` (nullable — hide tile if `null`/`undefined`, same guard style already used for `no_show_count`).
- Existing 4 tiles keep their current data bindings; only markup/class changes (swap raw `<div class="shadow-lg...">` for `Card.Root`/`Card.Content` if not already using them — confirm via re-read, since research indicates they may already; if already using `Card`, this unit is a no-op on structure and only adds the tile).
- `shadow-lg`/`shadow-xl` classes removed from tile cards in favor of the Tonal Scaffolding Rule (`DESIGN.md:121` — depth via surface-color steps, not blur) — replace with `bg-surface` on a `bg-matte` page background, or similar one-step-lighter treatment; do not introduce new shadow utilities.

**Test:**
- No existing test file was found for this route [VERIFY]; add a minimal Playwright smoke test `barberbase-frontend/src/routes/admin/analytics/analytics.e2e.ts` (new file) asserting: page loads, 5 stat tiles render when `cancelled_count` is present, 4 render when it's `null`.

---

### Unit B2 — 7-day trend sparkline (hand-rolled SVG, no new dependency)
**Depends:** B1 (shares the page; avoid parallel merge conflicts on the same file)
**Read:**
- `barberbase-frontend/src/routes/admin/analytics/+page.server.ts` (load pattern — extend to fire 7 parallel `fetch`es for the trailing 7 days against the *existing* `GET /staff/analytics/daily?date=` endpoint; `Promise.all`, no backend change)
- `barberbase-core/internal/api/handlers_staff.go:490` (`GetDailyAnalytics` — confirms `date` query param behavior, defaults to today)
- Dataviz skill guidance (loaded via `Skill` tool at implementation time — mark-spec method, color formula, accessible-in-both-themes requirement) — this page is permanently dark (per `scripts/shadcnize.sh` comment: "the app is permanently dark"), so the validator only needs to pass the dark-mode check.

**Write:**
- `barberbase-frontend/src/routes/admin/analytics/+page.server.ts` (add the 7-call `Promise.all` fetch, new `trend` field in the returned data)
- New file: `barberbase-frontend/src/lib/components/charts/Sparkline.svelte` — hand-rolled inline `<svg>` line chart, ~40-60 lines. **No new npm dependency.** ponytail: a 7-point line is a `<polyline>` with computed `points`; do not reach for a charting library for this.
- `barberbase-frontend/src/routes/admin/analytics/+page.svelte` (render the sparkline for `total_visits` and `total_revenue_paise` trend, two small cards)

**Accept:**
- `Promise.all` of 7 calls, not sequential — page load time must not scale linearly with day count added later; if a future unit extends this to 30 days, sequential fetching would be the wrong shape, so get this right now even at 7.
- Any single day's fetch failing does not blank the whole page — missing days render as gaps in the sparkline (`null` in the data array, skip that point in the `points` string), matching the existing per-field error tolerance already in `+page.server.ts` (401→redirect, other errors→`analyticsError` string, page still renders).
- Sparkline line color: `muted` (`#9F9B93`) or `primary` (`#E5E2D9`) for the trend line itself; `gold-accent` reserved for a single dot marking *today* only — this is the "≤5% surface, highlight only" application of the Gold Accent Rule to a chart context.
- Axis/tooltip numbers (if a tooltip is added — optional, keep it simple) use `font-mono` per the Tabular Alignment Rule.
- No pure `#000`/`#fff` anywhere in the SVG — background transparent (inherits `bg-matte`/`bg-surface` card), gridlines (if any) at `stroke-white/[0.06]` matching existing border conventions (`border-white/[0.04]` used elsewhere in the codebase, e.g. `q/appointment/+page.svelte:93`).

**Test:**
- Extend `analytics.e2e.ts` (from B1): assert the sparkline SVG renders with 7 data points (7 `<circle>`/segment markers or a `points` attribute with 7 coordinate pairs) after page load; assert a day with a simulated fetch failure still renders 6 points without crashing the page (mock/stub the failing day's API response in the Playwright fixture).

---

### Unit B3 — Barber breakdown table: shadcn Table polish, no chart rewrite
**Depends:** none (independent of B1/B2, touches a different part of the same file — coordinate merge order manually)
**Read:**
- `barberbase-frontend/src/routes/admin/analytics/+page.svelte:23-26, 124-137` (existing barber breakdown table + CSS revenue-share bar)
- `barberbase-frontend/src/lib/components/ui/table/` (existing shadcn `Table` primitives — confirm the barber breakdown already uses these; research indicates it does per the `import * as Table` in `+page.svelte:2-5`)

**Write:**
- `barberbase-frontend/src/routes/admin/analytics/+page.svelte` only

**Accept:**
- **Do not replace the working CSS width-bar** (`+page.svelte:124-137`) with a new chart component — it already works, is already token-compliant (`bg-gold-accent/30`), and rebuilding it would violate the "no new abstraction for a value that never changes" ladder. The only change here is adding a `Table.Head`-driven sort toggle (by revenue, by visits) if the table currently has none — `[VERIFY current sort behavior before adding; if already sortable, this unit is a no-op — close it without a diff]`.
- If sorting is added: client-side `Array.prototype.sort`, no new dependency, sort state as a single `$state` variable.

**Test:**
- Extend `analytics.e2e.ts`: click a sortable column header, assert row order changes.

---

# (C) Platform Onboarding Page

## Current state
- `barberbase-frontend/src/routes/platform/+page.server.ts:5-115` — single `provision` action, calls `POST /v1/admin/setup` with `X-Platform-Admin-Key` header, returns `{success, tenant_id, location_id, owner_staff_member_id, arrival_pin, owner_phone, public_path}` on `201`.
- `barberbase-frontend/src/routes/platform/+page.svelte` — success panel (lines 129-273) shows the PIN once, a public shop link, and copyable IDs. **No links to devices, services, or staff setup** — the flow dead-ends at "Provision Another Shop."
- `barberbase-frontend/src/routes/platform/devices/+page.server.ts:28-34` — **already reads `location_id`/`tenant_id` from URL query params** (`url.searchParams.get(...)`), so a deep link `/platform/devices?tenant_id=X&location_id=Y` works with **zero backend or devices-page changes**. This is the cheapest possible integration point.
- `GET /v1/admin/devices` (`ListStationDevices`, `barberbase-core/internal/api/handlers_device.go:331-427`) is registered only as a manual route (`handlers_public.go:1401`) and is **confirmed absent** from `architecture/openapi.yaml` (`grep -n "/admin/devices" architecture/openapi.yaml` shows only `POST /admin/devices` at line 2858, `PATCH .../{device_id}` at line 2902, `POST .../{device_id}/buttons` at line 2931 — no `get:` under any of these). Code comment confirms this is intentional-but-owed: `handlers_device.go:326-330`, "not yet in openapi.yaml; the one-line spec addition is queued for the next frozen-file session."
- Post-provisioning service/staff creation (`CreateServiceVariant`, `internal/api/handlers_admin.go:132`; `CreateStaffMember`, `internal/api/handlers_admin.go:358`) are both **StaffJWT-gated**, not `PlatformAdminKey`-gated — the platform console (which only holds a `PlatformAdminKey`, never a JWT for the shop it just created) cannot call these directly without minting a JWT for the brand-new owner, which is a meaningfully bigger change (session minting, security review) for a "shortcut." The shop's owner can already do this today via `/admin/services` and `/admin/staff` after logging in with WhatsApp OTP at `/login` using the phone number the console already displays (`+page.svelte:196`).

## Units

### Unit C1 — FROZEN-GATE: add `GET /admin/devices` to openapi.yaml
**Depends:** none
**FROZEN-GATE: requires human-approved diff.** Touches `architecture/openapi.yaml` and regenerates `internal/api/generated.go` + `barberbase-core/api/openapi.yaml` (both frozen/generated).

**Read:**
- `barberbase-core/internal/api/handlers_device.go:331-427` (`ListStationDevices` — exact response shape to schema-ify)
- `architecture/openapi.yaml` around lines 2858-2937 (the three existing `/admin/devices*` paths — match their `security`/style exactly)

**Write:**
- `architecture/openapi.yaml` only (the diff below); regeneration (`make gen-api`) happens after human approval, producing changes to `internal/api/generated.go` and `barberbase-core/api/openapi.yaml` as a *separate*, mechanical, reviewable step — not hand-edited.

**Proposed diff** (insert as a `get:` sibling under the existing `/admin/devices:` path key, `architecture/openapi.yaml:2858`):
```yaml
  /admin/devices:
    get:
      operationId: listStationDevices
      summary: List station devices and buttons for a location
      security:
        - PlatformAdminKey: []
      parameters:
        - name: location_id
          in: query
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [devices, staff]
                properties:
                  devices:
                    type: array
                    items:
                      type: object
                      required: [id, label, is_active, buttons]
                      properties:
                        id: { type: string, format: uuid }
                        label: { type: string }
                        is_active: { type: boolean }
                        last_seen_at: { type: string, format: date-time, nullable: true }
                        buttons:
                          type: array
                          items:
                            type: object
                            required: [id, button_code]
                            properties:
                              id: { type: string, format: uuid }
                              button_code: { type: string }
                              label: { type: string, nullable: true }
                              staff_member_id: { type: string, format: uuid, nullable: true }
                  staff:
                    type: array
                    items:
                      type: object
                      required: [id, name, role]
                      properties:
                        id: { type: string, format: uuid }
                        name: { type: string }
                        role: { type: string }
        '400':
          description: Missing/invalid location_id
        '401':
          description: Missing or invalid PlatformAdminKey
    post:
      # existing createStationDevice operation — unchanged, do not touch
```
Field types verified against the exact Go structs `buttonOut`, `deviceOut`, `staffOut` at `handlers_device.go:341-353, 400-404, 426`.

**Accept:**
- `make gen-api` (run by implementer after this diff is approved) produces a diff to `internal/api/generated.go` that adds `ListStationDevices` to the generated `ServerInterface` **without** removing the existing manual registration at `handlers_public.go:1401` — the manual route currently exists specifically because the generated `/v1` mount enforces "no apiKey schemes" (comment, `handlers_public.go:1397-1399`); after this diff, decide explicitly whether the generated wrapper can now carry the `PlatformAdminKey` security scheme cleanly, or whether the manual shadow-route must stay. **Do not silently drop the manual route** — `TestDeviceRoutes_FullRouterAuth` (`handlers_device_test.go:242-273`) is a regression pin specifically guarding against the generated mount ever becoming reachable unauthenticated; re-run it after `make gen-api` and treat any failure as a hard stop.
- `barberbase-core.Server` still compiles and implements the (possibly now-larger) generated interface without needing a new `Unimplemented` fallback for this operation.

**Test:**
- `go build ./...` after `make gen-api` (compile gate).
- `go test ./internal/api/... -run TestDeviceRoutes_FullRouterAuth -run TestListStationDevices -v` (must both still pass, unmodified).

---

### Unit C2 — Success-panel shortcuts (frontend only, no backend change)
**Depends:** none (independent of C1 — the devices page already works today via the manual route; C1 only affects `openapi.yaml` hygiene, not runtime behavior)
**Read:**
- `barberbase-frontend/src/routes/platform/+page.svelte:129-273` (success panel — full block to extend)
- `barberbase-frontend/src/routes/platform/devices/+page.server.ts:28-34` (confirms `?tenant_id=&location_id=` deep-link already works, zero devices-page changes needed)

**Write:**
- `barberbase-frontend/src/routes/platform/+page.svelte` only

**Accept:**
- Success panel gets one new section, "Next Steps," with three links:
  1. `/platform/devices?tenant_id={form.tenant_id}&location_id={form.location_id}` — "Set up hardware devices" (works today, zero backend change, per the confirmed query-param read in `devices/+page.server.ts:28-34`).
  2. `/login` with copy: "Owner logs in here with {form.owner_phone} to add services and staff" — no new endpoint; reuses the existing `/admin/services` and `/admin/staff` panels the owner reaches after WhatsApp-OTP login (`barberbase-frontend/src/routes/admin/services/`, `.../staff/` — already exist per the project structure in `CLAUDE.md`).
  3. The existing public shop link (`+page.svelte:171-191`), unchanged, just repositioned into the same "Next Steps" block for visual grouping.
- No new backend calls, no new StaffJWT minting, no new PlatformAdminKey-scoped endpoints for services/staff — ponytail: the shortcut is a link to infrastructure that already does the job, not a rebuild of `/admin/services` inside `/platform`.

**Test:**
- Playwright: after a successful provision, assert the three links render with the correct `tenant_id`/`location_id`/`owner_phone` interpolated from the actual form response (not hardcoded fixture values — assert against whatever the mocked `/v1/admin/setup` response returns in the test).

---

# (D) SSE / Web Push Runtime Debugging Runbook

**For a future Claude Code session running ON the production droplet**, via `docker compose`. This is an operational runbook, not a code-change spec — no `Write` targets; every command below is either read-only or explicitly scoped and reversible.

## Facts this runbook depends on (verified, cited)

- **Containers**: `barberbase-postgres` (postgres:16-alpine) and `barberbase-api` (`barberbase-core/docker-compose.yml`, `container_name` fields) — confirm actual production compose file has the same names before running any command below; if the droplet uses a different compose file, adjust names, do not assume.
- **SSE endpoint**: `GET /stream/{location_id}` (`architecture/openapi.yaml:1537`, `operationId: subscribeToQueueStream`). Auth is a **query param**, not a header: `?token=<StaffJWT>` for staff, `?t=<CustomerSession>` for customers (`openapi.yaml:1554-1557, 1565-1570`) — an `Authorization:` header curl will NOT authenticate this endpoint; use the query param.
- **SSE event shapes** (`barberbase-core/internal/realtime/manager.go:15-19`, wire format `MarshalSSE`, lines 23-30):
  - `event: queue_changed\ndata: {"type":"queue_changed","location_id":"<uuid>","queue_version":<int>}\n\n`
  - `event: heartbeat\ndata: {"type":"heartbeat","queue_version":<int>}\n\n` — every **30 seconds** (`manager.go:153`), `location_id` omitted on heartbeats.
- **Push subscribe endpoint**: `POST /v1/staff/push/subscribe` (`handlers_push.go`, `SubscribePush`), StaffJWT auth, body `{endpoint, p256dh, auth}` all required, persists to `staff_members.push_endpoint`, `.push_p256dh`, `.push_auth`, `.push_enabled=true` (`handlers_push.go` UPDATE, exact columns confirmed). Success → `204`.
- **`outbox_events` exact columns** (`barberbase-core/migrations/001_complete_schema.sql:1328-1349`): `id, tenant_id, type, payload, status, attempts, max_attempts, last_error, process_after, locked_until, created_at, dispatched_at`. `status` CHECK constraint: `'pending' | 'processing' | 'dispatched' | 'failed'`.
- **Web push outbox type string**: exactly `'web_push.send'` — confirmed at `internal/outbox/worker.go:54` (map key) and the special-case dispatch check `internal/outbox/worker.go:134`. **Gotcha to flag if you grep the codebase**: `worker.go:54` also registers `"web_push.send": &stubHandler{}` in the handler map with comment `// C6.5 replaces` — this entry is dead/stale, actual dispatch bypasses the map entirely via the `if event.Type == "web_push.send"` special case at `worker.go:134` calling `w.pushHandler.HandleWebPushSend` directly. Don't be misled by the stub if you read the map.
- **410 Gone cleanup**: `internal/outbox/handlers/push_notification.go:196-205` — on `resp.StatusCode == http.StatusGone`, runs `UPDATE staff_members SET push_enabled=false, push_endpoint=NULL, push_p256dh=NULL, push_auth=NULL WHERE id=$1`. There is **no separate `push_subscriptions` table** — subscription state lives directly on `staff_members` columns `push_endpoint`, `push_p256dh`, `push_auth`, `push_enabled` (confirmed via `001_complete_schema.sql` staff_members block).
- **Outbox claim/backoff** (`worker.go:90-107, 146-187`): `FOR UPDATE SKIP LOCKED`, 500ms poll ticker; success → `status='dispatched', dispatched_at=NOW()`; terminal error → `status='failed', attempts=max_attempts` (stops retrying); retryable error backoff: attempt 1 → 30s, attempt 2 → 2m, attempt 3+ → 10m (`backoffFor`, `worker.go:178-187`).
- **`notification_events`** rows written per push attempt: `channel='web_push'`, `notification_type='push_call_next'`, `status IN ('sent','failed')`, `error_message='410_gone'` on stale-subscription cleanup specifically (exact string, confirmed against `push_notification_test.go:288-289`).

## Diagnostic procedures

### D-1. SSE connect with a real staff token
```bash
# Get a location_id and a valid StaffJWT (see prod-access memory for JWT minting).
curl -N "https://api.barberbase.in/v1/stream/<location_id>?token=<STAFF_JWT>"
```
**Expected output** within 30s: a `heartbeat` event —
```
event: heartbeat
data: {"type":"heartbeat","queue_version":<N>}

```
If a queue mutation happens on another connection during the session, a
`queue_changed` event with a matching or higher `queue_version` should
appear immediately (broadcast is fire-and-forget, non-blocking per-subscriber
channel of capacity 16 — `manager.go:60`, `manager.go:138-144` — a slow
client can silently drop events, "client refetches on reconnect" by design;
this is NOT a bug if seen).
**Failure signature — no heartbeat after 35s**: check `docker logs
barberbase-api` for panics in the realtime package; check the token isn't
expired (Law 18: 4h TTL); check `curl -I` (no `-N`) returns `200` with
`content-type: text/event-stream` at all before assuming the stream itself
is broken.

### D-2. Push-subscribe round trip
```bash
curl -X POST https://api.barberbase.in/v1/staff/push/subscribe \
  -H "Authorization: Bearer <STAFF_JWT>" \
  -H "Content-Type: application/json" \
  -d '{"endpoint":"<browser-endpoint>","p256dh":"<key>","auth":"<key>"}'
```
**Expected**: `204 No Content`. Then verify persistence:
```bash
docker exec -it barberbase-postgres psql -U bb_user -d barberbase -c \
  "SELECT id, push_enabled, push_endpoint IS NOT NULL AS has_endpoint FROM staff_members WHERE id = '<staff_id>';"
```
**Expected row**: `push_enabled = t`, `has_endpoint = t`.

### D-3. Trace a `web_push.send` outbox event to completion
```bash
docker exec -it barberbase-postgres psql -U bb_user -d barberbase -c \
  "SELECT id, status, attempts, max_attempts, process_after, locked_until, last_error, created_at, dispatched_at
   FROM outbox_events
   WHERE type = 'web_push.send'
   ORDER BY created_at DESC
   LIMIT 10;"
```
**Expected for a healthy event**: `status = 'dispatched'`, `dispatched_at`
non-null, within ~500ms-30s of `created_at` (poll interval + claim lock
window). **If `status = 'failed'` and `attempts = max_attempts`**: check
`last_error` — this is terminal, the worker will not retry it; that is
correct behavior for a hard error (e.g., malformed payload), not a hang.
**If `status = 'processing'` and `locked_until` is in the past**: the worker
should reclaim it on its next 500ms tick (`worker.go:97`, `status =
'processing' AND locked_until < NOW()` is one of the claimable conditions)
— if it's been stuck past that, the worker process itself may be down;
check `docker compose ps` and `docker logs barberbase-api | tail -100`.

Cross-reference per-staff delivery outcome:
```bash
docker exec -it barberbase-postgres psql -U bb_user -d barberbase -c \
  "SELECT source_id AS staff_member_id, channel, notification_type, status, error_message, created_at
   FROM notification_events
   WHERE channel = 'web_push' AND source_type = 'staff_member'
   ORDER BY created_at DESC
   LIMIT 20;"
```
Columns verified against `001_complete_schema.sql:1174-1218`: there is **no
`staff_member_id` column** on `notification_events` — for web_push rows the
staff member is `source_id` with `source_type='staff_member'` (schema comment
at `001_complete_schema.sql:1182-1183`). `status` values here are the
notification-events set (`queued|sent|delivered|failed|blocked_quota|skipped_opt_out`),
distinct from the outbox_events status set.

### D-4. Verify HTTP-410 cleanup disabled a stale subscription
After confirming a `notification_events` row with `error_message = '410_gone'` for a given `staff_member_id` (query above), confirm the disable took effect:
```bash
docker exec -it barberbase-postgres psql -U bb_user -d barberbase -c \
  "SELECT id, push_enabled, push_endpoint, push_p256dh, push_auth FROM staff_members WHERE id = '<staff_id>';"
```
**Expected**: `push_enabled = f`, all three `push_*` columns `NULL`. If
`push_enabled` is still `t`, the 410-handler branch (`push_notification.go:196-205`)
did not run for this event — check `docker logs barberbase-api` for the log
line `"web_push: 410 Gone for staff %s, disabling push"` (exact string,
`push_notification.go:197`) around the event's `created_at` timestamp.

## SAFE-COMMANDS
```
docker compose ps
docker compose logs [service] [--tail=N] [-f]
docker logs barberbase-api [--tail=N] [-f]
docker logs barberbase-postgres [--tail=N] [-f]
docker exec -it barberbase-postgres psql -U bb_user -d barberbase -c "SELECT ..."   # SELECT only
curl (any GET, or POST to a documented public/staff endpoint using a real or test token)
docker compose restart <single-service-name>   # only if a human has approved a restart
```

## NEVER-COMMANDS
```
docker compose down -v          # NEVER — destroys the postgres data volume
docker volume rm ...            # NEVER
UPDATE / DELETE without a WHERE clause on a specific primary key       # NEVER
UPDATE / DELETE against outbox_events, staff_members, or any table without
  first SELECTing the exact row(s) and confirming the WHERE matches only
  the intended row(s)
Re-running a migration file (001_*.sql, 002_*.sql) against the live DB    # NEVER —
  001 is idempotent-guarded by repository.Migrate()'s bootstrap logic only on
  an EMPTY database; re-running against a populated DB is not something this
  runbook should attempt. If 002_station_devices.sql is suspected NOT applied
  in prod, that is a decision for a human, not an automatic fix.
docker compose down (without -v) followed by up  # avoid unless a human
  explicitly requests a full restart — prefer `docker compose restart <service>`
  for a single container
```

---

# (E) Firmware Spec v1 — ESP32 / Hardware-Button Device Contract

Transport-agnostic: the same HTTP contract below applies whether the
physical device is a WiFi ESP32 button, a serial dongle bridged by a local
gateway, or a 4G-connected standalone unit. **The backend does not know or
care which** — it only sees `POST /v1/device/call-next` requests bearing a
device secret. This spec targets the shipped v1 contract; do not add new
server-side capabilities here — this is firmware-side only.

## Live contract (verified against shipped code)

**Endpoint**: `POST /v1/device/call-next` (`barberbase-core/internal/api/handlers_public.go:1400`, handler `DeviceCallNext` at `barberbase-core/internal/api/handlers_device.go:56`). No StaffJWT — auth is entirely in-handler via the device token.

**Auth header**: `X-Device-Token: <secret>` (`handlers_device.go:60`, exact header name). Secret format: `"bbd_" + base64url(32 random bytes)` (`internal/device/token.go:14-22`); server stores only `SHA-256(secret)`, never plaintext (`device.Hash()`, `token.go:26-29`) — **the plaintext secret is shown to the operator exactly once**, at device-creation time (`CreateStationDevice`, `handlers_device.go:186-241`, response `{"id","label","secret"}`, `201`). Firmware must be provisioned with this secret at flash/setup time; there is no re-fetch mechanism if it's lost — a lost secret means creating a new device record.

**Request body**:
```json
{ "button_code": "<string, required>", "pressed_at": <int64 unix seconds, optional> }
```
(`handlers_device.go:95-98`, exact Go struct). `button_code` maps to a `station_buttons` row scoped to the authenticated device (`SELECT staff_member_id FROM station_buttons WHERE device_id=$1 AND button_code=$2`, `handlers_device.go:115-119`) — a device can have multiple buttons (e.g. one per chair), each optionally bound to a specific `staff_member_id` (NULL = pooled/shop-wide dispatch).

**Every response** (exact, from `handlers_device.go`):

| Condition | Status | Body |
|---|---|---|
| Missing `X-Device-Token` | `401` | `{"code":"UNAUTHORIZED","message":"missing X-Device-Token header"}` |
| Token not found / revoked | `401` | `{"code":"UNAUTHORIZED","message":"invalid or revoked device token"}` |
| Auth DB error | `500` | `{"code":"INTERNAL_ERROR","message":"internal server error"}` |
| Rate limit exceeded | `429` | **empty body** (only `w.WriteHeader(429)` — do not expect JSON here) |
| Bad/missing `button_code` | `400` | `{"code":"INVALID_REQUEST","message":"button_code is required"}` |
| Stale press (`pressed_at` given and `time.Since(pressed_at) > 60s`) | `200` | `{"result":"stale_discarded"}` |
| Unknown `button_code` for this device | `404` | `{"code":"NOT_FOUND","message":"unknown button_code for this device"}` |
| Button-lookup DB error | `500` | `{"code":"INTERNAL_ERROR","message":"internal server error"}` |
| No dispatchable queue entry | `200` | `{"result":"no_waiting"}` |
| Lock contention | `503` | `{"error":"lock_timeout_retry"}`, header `Retry-After: 1` |
| Other dispatch error | `500` | `{"code":"INTERNAL_ERROR","message":"internal server error"}` |
| Success | `200` | `{"result":"advanced","called_token_number":<int>}` |

**Stale threshold**: exactly `60 * time.Second` (`handlers_device.go:32`, constant `staleThreshold`). A press older than 60s (per the firmware/gateway's own `pressed_at` clock) is discarded server-side, not queued or retried.

**Per-device rate limit**: exactly `rate.Every(3*time.Second), 1` — **1 request per 3 seconds per device** (not per button; multiple buttons on one device share the limiter), burst size 1 (`handlers_device.go:38-44`). A second press within 3s of the first gets `429` regardless of which button was pressed.

**Note on `pressed_at` and clock skew**: the server only checks `time.Since(pressed_at) > staleThreshold` — a **future** `pressed_at` (clock ahead) produces a negative duration and is treated as fresh, never rejected as stale. `[VERIFY]` no test covers this case (`handlers_device_test.go` has no future-timestamp test) — firmware should still send an honest timestamp, but should not rely on the server to catch a badly-skewed clock in the "too far ahead" direction.

## Firmware spec

### Provisioning
- Device record + secret created once via `POST /admin/devices` (`PlatformAdminKey`-gated, operator console at `/platform/devices` per deliverable C) — this happens **before** firmware flashing, out of band.
- Firmware is flashed/configured with: the device secret (`X-Device-Token` value), the API base URL, and WiFi credentials (or equivalent transport config for non-WiFi variants).
- **WiFi provisioning approach**: captive-portal AP mode on first boot (ESP32 SoftAP + a minimal HTML form) is the standard low-effort pattern — ponytail: do not build a companion mobile app or BLE provisioning flow for v1; a $2 button doesn't need one. Store WiFi creds + device secret in NVS (non-volatile storage), never in a way that survives a factory-reset-and-resell without being wiped (use `nvs_flash_erase()` on a long-press factory-reset gesture).
- **Secret storage**: NVS, not printed/exposed after initial provisioning. If a device is physically compromised, the fix is revoking the device (`PATCH /admin/devices/{id}` `is_active:false`, already shipped, `handlers_device.go:294-323`) — firmware has no revocation-detection responsibility beyond handling the resulting `401` gracefully (see feedback table below).

### Button debounce
- Hardware debounce in firmware: **minimum 200-300ms** between a physical edge trigger and treating it as a new "press" — standard mechanical-switch debounce, unrelated to and smaller than the server's 3s rate limit. This prevents one physical tap from generating multiple HTTP requests; it does not replace the server-side rate limit, which is the actual anti-spam authority.
- Do not implement client-side rate limiting beyond debounce — the server is authoritative (429 handling below covers the rest). Duplicating the 3s window in firmware risks drift from the server's actual limiter and adds no value.

### Retry policy — and why NOT to retry a stale press
- **On `200 {"result":"stale_discarded"}`**: do **not** retry. The whole point of the 60s staleness check is that a press queued during a network blip (e.g. gateway buffered it during a 4G reconnect) is stale by the time it lands — resending it just re-confirms staleness or, worse, if resent with a *fresh* `pressed_at` (i.e., firmware lies about when the press happened to dodge the check), silently advances the queue for a press that didn't happen in real time from the customer's perspective. **Never rewrite `pressed_at` on retry.** Treat `stale_discarded` as terminal, log it locally (LED/beep pattern below), move on.
- **On `429`**: back off and do not immediately retry — the debounce layer should already prevent hitting this from a single physical press, so a 429 in practice means either two staff pressed buttons on the same device within 3s (legitimate contention — the second press is correctly dropped, no retry needed, the situation resolves itself on the next real press) or a firmware bug double-firing. Do not auto-retry on a timer; surface the "busy" feedback (below) and let the next real press try again naturally.
- **On `503 {"error":"lock_timeout_retry"}` with `Retry-After: 1`**: this is the one case that **should** retry — honor the `Retry-After` header, wait that many seconds, retry once with the **same original `pressed_at`** (not a refreshed one — if the retry itself now exceeds 60s staleness, that's correct behavior, let it discard rather than falsifying the timestamp).
- **On `401`/`404`/`500`**: do not retry automatically in a tight loop. `401` means the device was revoked or the secret is wrong — this needs human intervention (re-provisioning), not a retry storm. `404` means the button_code itself is misconfigured (firmware/server mismatch) — also a provisioning-time problem. `500` — a single retry after a few seconds is reasonable (transient server error), but cap it (e.g., 1 retry, then give up and show the failure state) rather than an unbounded backoff loop that could itself look like abuse against the rate limiter.
- **Network unreachable** (no HTTP response at all — WiFi drop, DNS failure): queue exactly the most recent press locally with its original `pressed_at`, retry once connectivity returns, and rely on the server's 60s staleness check to correctly discard it if too much time passed. Do not queue *multiple* unsent presses — a button is "call next," not a counter; only the latest unsent press is ever meaningful, and replaying a backlog of stale presses is exactly the failure mode the staleness check exists to prevent.

### LED / beeper feedback per response
| Response | Feedback |
|---|---|
| `200 advanced` | Single confirming beep + solid green LED flash (~300ms) |
| `200 no_waiting` | Two short beeps (distinct from success) + amber LED flash — "nothing to call" |
| `200 stale_discarded` | No beep (avoid confusing staff into thinking it worked) + brief dim red LED flash |
| `429` | No beep, no retry — brief amber flash ("busy, try again") |
| `503` (before the honored retry) | No feedback yet — wait for the retry's outcome before signaling anything to staff |
| `401` / `404` | Solid red LED (persistent, not a flash) until power-cycled or reconfigured — signals "this button needs an admin," not a transient issue |
| `500` (after the one retry) | Three short beeps + red flash — distinct failure signature from `stale_discarded` |
| Network unreachable | Slow-pulsing amber LED (offline indicator) instead of any beep pattern above |

### Offline behavior
- While WiFi/network is down: device stays in the slow-pulse-amber "offline" state (above), buttons remain physically pressable but each press just re-queues the "most recent press" per the retry policy above (overwriting, not appending) and attempts send on the next connectivity check (poll every 5-10s for reconnect — cheap, no need for push-based reconnect detection on an ESP32).
- No local queue persistence across a power cycle — if the device loses power while offline, the unsent press is lost. This is intentional: a lost "call next" during an outage is operationally recoverable (staff notices and manually advances via the dashboard), and building persistent-across-reboot queuing for a single button press is disproportionate effort. ponytail: skip it; revisit only if field data shows this is a real pain point, not a theoretical one.
