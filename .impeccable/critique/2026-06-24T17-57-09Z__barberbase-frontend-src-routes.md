---
target: full frontend
total_score: 22
p0_count: 1
p1_count: 2
timestamp: 2026-06-24T17-57-09Z
slug: barberbase-frontend-src-routes
---
# BarberBase Full Frontend Critique

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | SSE live indicator and queue states are solid; no loading skeletons on dashboard initial render |
| 2 | Match System / Real World | 3 | Good domain language ("Walk-in", "Token #", "Chair"); "Dispatch" and "Queue Controller" leak system jargon |
| 3 | User Control and Freedom | 2 | No undo on staff actions; shop status "Cancel all waiting" is nuclear with minimal confirmation; uncloseable join modal on public shop page |
| 4 | Consistency and Standards | 1 | **Three distinct visual languages** across marketing, admin sub-pages, and public shop page |
| 5 | Error Prevention | 2 | Phone E.164 validation good; no confirmation on service deactivation; walk-in form variant selection state unclear |
| 6 | Recognition Rather Than Recall | 3 | Service variant checklist is informative; dashboard hardcodes "Hello, Barber" instead of actual staff name |
| 7 | Flexibility and Efficiency | 2 | No keyboard shortcuts, no bulk actions, no search/filter on queue or service catalog |
| 8 | Aesthetic and Minimalist Design | 3 | Where design system is used, excellent; dashboard action button density is heavy in "called" state |
| 9 | Error Recovery | 2 | 6 uses of `alert()` on dashboard break premium feel; PIN lockout has no digital recovery |
| 10 | Help and Documentation | 1 | No onboarding, no tooltips, no contextual help; WhatsApp JSON paste step assumes developer knowledge |
| **Total** | | **22/40** | **Acceptable — significant improvements needed** |

## Anti-Patterns Verdict

### LLM Assessment

**Partial fail.** The homepage reads as AI-generated SaaS template:
- **Identical card grid**: Features section is 4 same-sized icon+heading+text cards in a 2x2 grid
- **Numbered section markers**: "How it works" uses 1/2/3 step cards with identical structure (partially redeemed by custom step illustrations)
- **Tiny uppercase tracked eyebrows**: `text-xs font-bold text-dim uppercase tracking-widestUI` appears on nearly every page — about, contact, privacy, terms, admin, platform, q/status
- **Hero-metric template**: Analytics page has 4 identical stat cards with `text-3xl font-bold` + small label
- **Glassmorphism as default**: `backdrop-blur-xl` appears on 4+ pages (login, platform login, q/status, q/appointment)
- **Generic SaaS structure**: Hero + 3-step + features + testimonials + FAQ + CTA is the exact AI landing page template

**Clean on**: no gradient text, no side-stripe borders >1px.

The admin sub-pages and dashboard feel more authentic. The customer-facing q/status state machine is genuinely well-designed. But the marketing surface is template-grade.

### Deterministic Scan

**0 findings** across 44 rules (26 slop, 18 quality). The regex-based scanner checked all `.svelte`, `.css`, `.ts` files under `src/routes/` and `src/lib/`. Even with project config ignores disabled (`--no-config`), zero hits. Note: some rules (contrast, spacing monotony, flat type hierarchy) require rendered DOM analysis via Puppeteer to trigger — the static scan has lower coverage on computed-style issues.

**Assessment divergence**: The LLM review caught structural AI slop patterns (card grids, eyebrows, SaaS template) that the regex scanner isn't designed to detect — those are compositional issues, not individual CSS/HTML violations. No false positives to reconcile since the detector found nothing.

## Overall Impression

The design system itself is excellent — the canvas/matte/surface/titanium elevation ladder, gold-accent restraint, and font pairing are genuinely premium. But three different visual languages coexist in one product, and the page every customer actually touches (public shop) is the least connected to the brand. The backend sophistication (SSE, presence states, outbox pattern) dramatically outpaces the frontend polish on admin and error handling surfaces.

## What's Working

1. **The WhatsApp mockup hero** (`+page.svelte` lines 123-163): The sequenced bubble animation with gold-pulse turn notification communicates the entire product value prop without words. `prefers-reduced-motion` respect is a rare accessibility detail.

2. **Customer queue status state machine** (`q/status/+page.svelte`): The 7-state conditional rendering (remote → on_the_way → arrived → called → in_progress → completed → paused) is excellent progressive disclosure. Each state shows only what matters. The "It's Your Turn!" gold-ring treatment is emotionally correct.

3. **The design token system** (`layout.css`): The elevation ladder with micro-dot grid texture, 4px scrollbar, and `::selection` in gold-accent is thoughtful craft. When pages use this system, they feel cohesive and premium.

## Priority Issues

### [P0] Three Visual Languages in One Product
**Files**: `admin/services/+page.svelte`, `admin/staff/+page.svelte`, `admin/shop/+page.svelte`, `admin/whatsapp/+page.svelte`, `admin/analytics/+page.svelte`, `[tenant_slug]/[location_slug]/+page.svelte`

**Why it matters**: A user navigating from login → admin → admin/services crosses three distinct visual worlds. The 5 admin sub-pages use `bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900`, raw `slate-700/800` inputs, `text-white` instead of `text-primary`, and `focus:ring-amber-500` instead of design system focus states. The public shop page uses a third palette entirely — purple accents (`#a78bfa`, `#c084fc`), `#0b0f19` background, and `linear-gradient(135deg, #7c3aed, #4f46e5)` on the join CTA. This is the most critical UX trust issue.

**Fix**: Migrate all 6 files to the existing design token system (canvas/matte/surface/titanium, text-primary/muted/dim, gold-accent). The token system is already built and proven on the marketing and dashboard surfaces — this is pure adoption.

**Suggested command**: `/impeccable craft` to rebuild admin sub-pages and public shop page on-brand

### [P1] Dashboard Uses `alert()` for All Error Handling
**File**: `dashboard/+page.svelte`, 6 occurrences (lines ~329, 689, 704, 749, 760, 801)

**Why it matters**: Every dashboard action error surfaces via browser `alert()`. In a premium dark UI with custom error components elsewhere, this is a jarring regression. Staff hitting errors during a busy queue see a system dialog that breaks flow and doesn't match the product.

**Fix**: Replace with inline toast/snackbar or the existing red-950/30 error panel pattern already used on login pages.

**Suggested command**: `/impeccable harden dashboard/+page.svelte`

### [P1] Uncloseable Join Modal on Public Shop Page
**File**: `[tenant_slug]/[location_slug]/+page.svelte` lines 396-413

**Why it matters**: After tapping "Join via WhatsApp", a modal appears with no close button, no back button, no timeout. If the WhatsApp deeplink fails (wrong OS, WhatsApp not installed, link handler broken), the customer is trapped at the highest-stakes moment in the journey.

**Fix**: Add a close/back button to the modal. Add a fallback message for when the deeplink fails.

**Suggested command**: `/impeccable harden [tenant_slug]/[location_slug]/+page.svelte`

### [P2] Invalid Tailwind Double-Opacity Syntax
**Files**: `login/+page.svelte` (lines 103, 203), `dashboard/+page.svelte` (line 466), `platform/+page.svelte` (line 459), `platform/login/+page.svelte` (line 76), `admin/shop/+page.svelte` (line 94), `admin/whatsapp/+page.svelte` (line 141)

**Why it matters**: `border-system-error/30/50` and `bg-gold-accent/10/20` are invalid Tailwind — only one opacity modifier is allowed. These likely render as no border/wrong opacity, making error states and highlights silently broken.

**Fix**: Audit all `/X/Y` double-opacity patterns and pick one value.

**Suggested command**: `/impeccable audit`

### [P2] Global `select-none` Contradicts `::selection` Style
**File**: `layout.css` line 31

**Why it matters**: Body has `select-none` which blocks all text selection, but `::selection` is styled with gold-accent. Users can't copy phone numbers, queue tokens, or addresses. The premium selection highlight never fires.

**Fix**: Remove `select-none` from body. Apply it selectively on drag-sensitive elements only.

**Suggested command**: `/impeccable polish`

## Persona Red Flags

**Alex (Power User / Experienced Staff)**: No keyboard shortcuts on dashboard — can't press Enter to dispatch next. No search/filter in queue list; with 20+ entries, scrolling is the only option. No bulk operations. Version number displayed in stats cards is a system metric, not useful for staff.

**Jordan (First-Timer / New Shop Owner)**: Admin wizard step 1 asks for Category, Gender, Group, Variant, Duration, Price — 6 fields simultaneously for someone who just wants "Haircut - Rs 200". WhatsApp step 3 asks for raw JSON paste with no visual guide and no explanation of what Bhejna is. After wizard, admin hub shows 5 identical text-only cards with no icons and single-word descriptions.

**Casey (Mobile User)**: Dashboard is desktop-first with `lg:flex-row` — on mobile, Queue Controller and Walk-in form push actual queue entries below the fold. Admin tables use `<table>` without responsive treatment. `tracking-widestUI: 0.25em` on "BARBERBASE" header consumes significant horizontal space on 320px screens.

**Barbershop Owner (Domain)**: Analytics shows single-day view only — no trends, no week-over-week, no "Is my shop busier this week?" Staff table shows status but no daily metrics — "Who cut the most today?" is unanswerable. No revenue goals or targets.

**Walk-in Customer (Domain)**: Public shop page uses purple accent disconnected from the gold BarberBase brand they'll see in WhatsApp messages. After "Join via WhatsApp" modal opens, there's no escape if the deeplink fails. q/status shows no shop branding beyond the name — no address, no photo.

## Minor Observations

- **Font naming mismatch**: Tailwind config names tokens `satoshi` and `manrope`, but actual fonts are Plus Jakarta Sans and Inter. `font-satoshi` loads Plus Jakarta Sans. Misleading class names.
- **Login page blue glow**: `bg-blue-500/5` gradient glow (line 49) is off-palette. Everything else uses gold.
- **Header/nav duplication**: The header with logo, nav links, and mobile menu is copy-pasted across 6+ marketing pages. Any nav change requires touching all of them.
- **Homepage testimonials**: Three testimonials with specific names and Mumbai shop names — if placeholder, they're a liability.
- **No skip-to-content link**: Accessibility baseline missing.
- **Dashboard mobile menu**: Doesn't trap focus.
- **Star rating**: No aria-label per star in q/status.
- **Demo page**: Links to `/demo/playwright` which likely has no user-facing content. Dead-end CTA.
- **`SiteFooter.svelte`**: Used on marketing pages but not on admin/dashboard/queue, making the privacy policy link unreachable from those surfaces.

## Questions to Consider

1. Why does your highest-revenue page (public shop where customers join) look like it was built by a different team? It's the ONE page every customer touches, and it's the least connected to your brand.
2. If a barbershop owner completes the wizard, navigates to Admin > Services, and sees a completely different visual language — what does that do to their trust?
3. You've built real-time SSE with presence states and outbox patterns — but staff error handling is `alert()`?
4. The `select-none` on body blocks the `::selection` style you designed. Which one do you actually want?
5. What happens when the WhatsApp deeplink in the join modal fails? No close button, no fallback, no timeout. Highest-stakes moment, zero recovery.
