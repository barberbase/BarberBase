---
target: barberbase-frontend/src/routes
total_score: 22
p0_count: 1
p1_count: 1
timestamp: 2026-06-25T06-35-44Z
slug: barberbase-frontend-src-routes
---
# Design Critique: barberbase-frontend/src/routes

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Strong SSE live indicator + queue position updates. Missing: no skeleton states during reconnection |
| 2 | Match System / Real World | 3 | Domain language is accurate (Walk-in, Token, Called). Minor: "Single-Tap Dispatch" is dev jargon |
| 3 | User Control and Freedom | 2 | No undo on "Mark No-Show" or checkout. No confirmation on "Call Next" |
| 4 | Consistency and Standards | 2 | Public shop page uses raw CSS while everything else is Tailwind. Button radius varies across pages |
| 5 | Error Prevention | 2 | "Call Next" has zero confirmation. Discount field accepts decimals despite Law 4 (paise = integers) |
| 6 | Recognition Rather Than Recall | 3 | Clear labels, color-coded badges, inline prices/durations. Breadcrumbs on admin sub-pages |
| 7 | Flexibility and Efficiency | 1 | No keyboard shortcuts on dashboard. No bulk actions. No search/filter on service catalog |
| 8 | Aesthetic and Minimalist Design | 3 | Coherent design system, restrained gold accent. Noise: Version counter, emoji badges |
| 9 | Error Recovery | 2 | Inline errors, SSE auto-reconnect. But no retry buttons on network errors |
| 10 | Help and Documentation | 1 | No onboarding hints, no tooltips, no help links. WhatsApp setup has some instructional text |
| **Total** | | **22/40** | **Acceptable** |

## Anti-Patterns Verdict

**LLM assessment**: The product surfaces (dashboard, queue status, checkout) feel hand-built and product-specific. The marketing/static pages (about, contact, demo, platform) lean toward generic SaaS composition. Specific tells: numbered "How it works" steps on homepage and platform page, tiny uppercase tracked eyebrows (`text-xs font-bold text-muted uppercase tracking-wider`) repeated across analytics, queue status, and checkout modal, hero-metric template in analytics summary cards. The WhatsApp phone mockup on the homepage is the strongest anti-slop signal — genuinely creative and product-specific. No gradient text, no side-stripe borders >1px.

**Deterministic scan**: 0 findings across 51 files. The detector's regex-based checks found zero rule violations. Clean pass.

**Synthesis**: The detector confirms no hard anti-pattern violations (no gradient text, no glassmorphism abuse, no side-stripes). The LLM-identified tells (eyebrow labels, numbered steps, metric cards) are structural patterns the regex scanner doesn't cover — they're compositional, not syntactic. The backdrop-blur usage on login/status pages is borderline but not flagged.

## Overall Impression

The core product UI — dashboard, queue status, checkout — is solid and purpose-built. The "machined barber tool" aesthetic comes through in the dark surface hierarchy, restrained gold accent, and tactile button interactions. The biggest opportunity is operational efficiency: the staff dashboard lacks the keyboard shortcuts, quick-action patterns, and confirmation safeguards that a tool used 50+ times per day demands. The marketing pages are serviceable but generic.

## What's Working

1. **WhatsApp mockup on homepage** (`+page.svelte`): Product-specific hero, not generic SaaS. The sequenced bubble animations with gold pulse on "It's your turn!" make the value proposition visceral. `prefers-reduced-motion` is respected.

2. **Queue status state machine** (`q/status/+page.svelte`): Seven distinct visual states (remote → on_the_way → arrived → called → in_progress → completed → paused) with appropriate emotional tone per state. The "It's Your Turn!" ring emphasis and "All Done!" feedback stars show genuine UX thinking.

3. **Onboarding wizard** (`admin/+page.svelte`): Progressive disclosure with skip-at-every-step. Progress dots are clear. "You can change all of this later in settings" reduces anxiety. Right pattern for owners who may not have all info at once.

## Priority Issues

### [P0] "Call Next" has no confirmation dialog
**What**: The most consequential action in the app — calling a customer — fires on a single tap with zero confirmation.
**Why it matters**: In a barbershop with wet hands, accidental taps happen. One misfire disrupts a customer's wait and erodes trust. This button is used 30-50 times per day per shop.
**Fix**: Add a 1-second "hold to confirm" interaction, or show who will be called next before the tap fires.
**File**: `dashboard/+page.svelte`, lines 336-349
**Suggested command**: `/impeccable harden dashboard/+page.svelte`

### [P1] Public shop page uses entirely different styling system
**What**: `[tenant_slug]/[location_slug]/+page.svelte` has ~570 lines of raw CSS (`shop-container`, `variant-card`, `join-btn`) while everything else uses Tailwind utility classes.
**Why it matters**: Two styling paradigms means tokens drift apart. Visual inconsistency risk (e.g., `border-radius: 0.5rem` vs `rounded-xl`). No machined-edge, no micro-dot grid, no font aliases. Maintenance burden doubles.
**Fix**: Migrate to Tailwind utility classes using existing design tokens.
**File**: `[tenant_slug]/[location_slug]/+page.svelte`, lines 424-992
**Suggested command**: `/impeccable polish [tenant_slug]/[location_slug]/+page.svelte`

### [P2] Checkout modal has no "quick cash" shortcut
**What**: Every single checkout requires staff to interact with payment amount fields, even though the default (full amount, cash) covers ~90% of transactions.
**Why it matters**: This is the highest-frequency interaction in the app. Split-payment UI is premature complexity for what is usually "cash, full amount." Every extra second here multiplies across 30-50 daily checkouts.
**Fix**: Default to single "full amount, cash" state. Add a "Split payment?" toggle that reveals multi-line UI only when needed.
**File**: `CheckoutModal.svelte`
**Suggested command**: `/impeccable distill CheckoutModal.svelte`

### [P2] Emoji badges clash with premium "machined-tool" aesthetic
**What**: Status indicators use emoji (crown, scissors, globe, envelope, runner, checkmark) throughout dashboard and admin pages.
**Why it matters**: The design system specifies a machined-tool aesthetic, but emoji reads as informal/playful. On different devices, emoji renders differently, breaking visual consistency.
**Fix**: Replace emoji with small SVG icons or colored dot indicators. The SSE status dot in the dashboard header is the right pattern — extend it.
**Files**: `admin/staff/+page.svelte`, `admin/shop/+page.svelte`, `dashboard/+page.svelte`
**Suggested command**: `/impeccable polish dashboard/+page.svelte`

### [P3] No loading/skeleton states during data fetch
**What**: When SSE reconnects or analytics date changes, the UI shows stale data with no indication that new data is coming.
**Why it matters**: Staff might act on outdated queue information during reconnection windows.
**Fix**: Add opacity transition or skeleton pulse on queue list during refetch.
**Files**: `dashboard/+page.svelte`, `admin/analytics/+page.svelte`
**Suggested command**: `/impeccable harden dashboard/+page.svelte`

## Persona Red Flags

**Jordan (First-Time Customer)**: The WhatsApp join flow has a critical leak: select services → CAPTCHA → "Join via WhatsApp" → WhatsApp opens → user must press Send → only then in queue. If Jordan doesn't press Send in WhatsApp, they think they've joined but haven't. No fallback messaging on the web page confirms this. The appointment page is a stub ("coming soon") with no redirect to walk-in alternative — dead end.

**Casey (Distracted Mobile Customer)**: Queue status page uses `backdrop-blur-xl` which causes jank on low-end Android phones common in the target market (Mumbai barbershop customers). PIN input has `maxlength="6"` but label says "4-Digit Counter PIN" — inconsistency at a trust-sensitive moment. "Cancel My Spot" button text is `text-xs`, too small for its importance.

**Alex (Power-User Shop Owner/Staff)**: Dashboard shows "Hello, Barber" hardcoded — doesn't show actual staff name, a problem in multi-staff shops. No way to see who made the last action on a queue entry. Analytics has no export. No keyboard shortcuts for the tool used dozens of times daily. Admin hub has no navigation back to public-facing site.

## Minor Observations

- `layout.css` line 13: `--color-dim: #7A7770` differs from CLAUDE.md spec `#5A5854`. One is wrong.
- `animate-fade-in` CSS animation defined identically in 3 separate files — should be in `layout.css`.
- SiteHeader does not pass `activePage` on homepage or privacy page — nav links won't highlight correctly.
- Mobile menu has no "close on outside click" or "close on Escape" behavior.
- FAQ accordion uses `{#if}` conditional rendering instead of CSS height transition, causing layout shift.
- Privacy page `main` tag lacks `id="main-content"`, breaking skip-to-content link.
- Walk-in form `allVariants()` as derived function call may cause unnecessary re-renders.

## Questions to Consider

1. **Why does the checkout modal exist at all?** In most Mumbai barbershops, the customer pays and leaves. Could "Complete Service" be a single tap that marks the visit done, with payment details optionally entered later via analytics/admin?

2. **Is the WhatsApp-to-join flow leaking customers?** Three screens and a context switch (web → WhatsApp → press Send). What percentage who click "Join via WhatsApp" actually complete Send? If below 80%, the core value proposition is silently broken.

3. **The dashboard shows "Version: N" to staff. What decision does this help them make?** If none, remove it. If it's for debugging, gate it behind developer mode.
