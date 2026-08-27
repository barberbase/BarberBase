---
name: BarberBase Design System
description: Visual architecture, UI design tokens, and Svelte 5 components for Barberbase.
colors:
  canvas: "#080808"
  matte: "#0E0E0E"
  surface: "#141414"
  titanium: "#1C1C1C"
  primary: "#E5E2D9"
  muted: "#9F9B93"
  placeholder: "#7C7872"
  dim: "#5A5854"
  gold-accent: "#C8A96B"
  system-success: "#2A7F62"
  system-warning: "#D48D2A"
  system-error: "#C94A4A"
typography:
  display:
    fontFamily: "Plus Jakarta Sans, sans-serif"
    fontSize: "1.75rem"
    fontWeight: 800
    lineHeight: 1.2
    letterSpacing: "-0.045em"
  body:
    fontFamily: "Inter, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: "normal"
  mono:
    fontFamily: "Space Mono, monospace"
    fontSize: "0.75rem"
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: "0.25em"
rounded:
  sm: "4px"
  md: "8px"
  lg: "12px"
  full: "999px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.canvas}"
    rounded: "{rounded.full}"
    padding: "14px 24px"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.primary}"
    rounded: "{rounded.full}"
    padding: "14px 24px"
  card-matte:
    backgroundColor: "{colors.matte}"
    rounded: "{rounded.lg}"
    padding: "20px"
---

# Design System: BarberBase

## 1. Overview

**Creative North Star: "The Machined Barber Tool"**

The BarberBase design system models the visual feel of precision grooming tools—machined metal, tactile micro-textures, and high-contrast indicators. Designed to eliminate waiting area overcrowding, it serves barbershop owners, staff, and customers through frictionless, real-time queues. The UI evokes quality and functional speed, using structural surface elevation to prevent cognitive load.

It explicitly rejects generic, cluttered SaaS templates with irrelevant widgets and bright, overstimulating neon colors that cause astigmatism halation on mobile screens.

**Key Characteristics:**
- **Tactile Hardware Simulation:** Simulated machined edges, tight transitions, and physical active scale-shrink responses (`active:scale-[0.98]`).
- **Anti-Halation Surfaces:** Zero pure black and zero pure white to protect against OLED smearing and visual fatigue.
- **Ergonomic Density:** Focuses on queue states, wait-times, and status checkmarks clearly on a structured grid.
- **Mobile-First Touch Architecture:** Dynamic viewport units (`100dvh`), `overscroll-behavior-y: none`, and 48px touch targets.

## 2. Colors & Design Tokens

The color palette enforces a desaturated, highly legible layout using a warm gray scale and one micro Champagne Gold accent for queue alerts.

### Primary & Text Foreground
- **Alabaster Primary** (`#E5E2D9`): Primary text color, optimized for high contrast and readability on dark canvas surfaces.
- **Pebble Grey / Muted** (`#9F9B93`): Muted subtext, secondary metadata labels, and inactive state indicators.
- **Placeholder / Dim Text** (`#7C7872`): Input placeholders, secondary micro-copy, and small helper hints. Guaranteed WCAG contrast against dark surfaces under mobile shop glare.

### Neutral Stack (Elevation via Solid Surface Shifts)
- **Canvas** (`#080808`): The deepest structural background layer. Prevents OLED black-smear.
- **Matte** (`#0E0E0E`): Mid-level scaffolding, standard section backgrounds.
- **Surface** (`#141414`): Elevated card panels and list containers.
- **Titanium** (`#1C1C1C`): Active input fields, popovers, and dropdown menus.
- **Dim / Hairline** (`#5A5854`): Reserved strictly for subtle hairline borders, dividers, and inactive icons.

### Accents & System States (Anti-Halation Grounded Tones)
- **Champagne Gold** (`#C8A96B`): Reserved exclusively for active queue turn highlights ("Your Turn"), verified statuses, and primary focal highlights.
- **System Success (Olive/Sage)** (`#2A7F62`): Desaturated, grounded green for active check-ins, completed visits, and live status.
- **System Warning (Warm Amber/Brass)** (`#D48D2A`): Warm industrial amber for delayed/snoozed entries and pending actions.
- **System Error (Muted Terracotta)** (`#C94A4A`): Earthy brick red for cancellations, missed slots, and critical alerts.

### Named Rules
1. **The Gold Accent Rule.** Champagne Gold is used strictly for queue verification highlights and the active "Your Turn" state. Its use must not exceed 5% of any screen surface to maintain its focal visual weight. Never use gold for small body text under 14px.
2. **The Placeholder Contrast Rule.** Text placeholders must use `#7C7872` (or lighter) to guarantee legibility under bright barbershop overhead lighting.
3. **The Anti-Halation Rule.** Avoid pure saturated primary reds/greens against OLED dark backgrounds to eliminate visual bleeding and halo artifacts.

## 3. Typography

**Display Font:** "Plus Jakarta Sans", sans-serif
**Body Font:** "Inter", sans-serif
**Label/Mono Font:** "Space Mono", monospace

### Hierarchy
- **Display** (Bold (800), 1.75rem, 1.2, tracking-tightest (-0.045em)): Used for main titles and hero headers.
- **Headline** (Semi-Bold (600), 1.25rem, 1.3): Section headers and card headings.
- **Body** (Regular (400), 0.875rem, 1.5): Standard UI copy, long prose, and list content.
- **Label** (Medium (500), 0.75rem, 1.4, tracking-widestUI (0.25em), UPPERCASE): Tabular labels, tabs, and small UI button labels.

### Named Rules
**The Tabular Alignment Rule.** All numbers, queue positions, and time stamps must use the monospace font stack (`Space Mono`) to ensure strict alignment across cards and tables.

## 4. Calendar, Scheduling & Queue State Color Matrix

How appointments, queue states, and calendar slots are color-coded without breaking the 5% gold rule:

| State / Role | Background & Border | Text Color | Visual Meaning |
|---|---|---|---|
| **Now Serving / Active Turn** | `bg-gold-accent text-canvas border-gold-accent` | `#080808` | "Your Turn" / In Chair (Only 1 active entry at a time) |
| **Checked In / Arrived** | `bg-system-success/15 border-system-success/30` | `#2A7F62` | Customer is physically in the shop |
| **Scheduled / Available Slot** | `bg-surface border-white/[0.08] hover:border-white/20` | `#E5E2D9` | Pre-booked appointment or open time slot |
| **On the Way / Delayed** | `bg-system-warning/15 border-system-warning/30` | `#D48D2A` | Customer travelling or queue paused |
| **No-Show / Cancelled** | `bg-system-error/15 border-system-error/30` | `#C94A4A` | Missed appointment or cancelled ticket |
| **Completed / Past** | `bg-matte border-white/[0.03]` | `#9F9B93` | Finished service, historical record |

## 5. Buttons & Interactive Controls Matrix

Every interactive component implements standard hardware states:

| Component / Variant | Default State | Hover State | Active State | Disabled State |
|---|---|---|---|---|
| **Button: Primary** | `bg-primary text-canvas rounded-full font-bold` | `bg-[#D5D2C9]` | `active:scale-[0.98]` | `opacity-40 pointer-events-none` |
| **Button: Accent (Focal)** | `bg-gold-accent text-canvas rounded-full font-bold` | `brightness-105` | `active:scale-[0.98]` | `opacity-40 pointer-events-none` |
| **Button: Secondary / Outline** | `bg-transparent text-primary border border-white/10` | `bg-white/[0.03] border-white/20` | `active:scale-[0.98]` | `opacity-40 pointer-events-none` |
| **Button: Destructive** | `bg-system-error/15 text-system-error border border-system-error/30` | `bg-system-error/25` | `active:scale-[0.98]` | `opacity-40 pointer-events-none` |
| **Input / Form Field** | `bg-canvas border-white/[0.08] text-primary placeholder-[#7C7872]` | `border-white/20` | `border-gold-accent ring-1 ring-gold-accent/40 bg-surface` | `opacity-40 pointer-events-none` |

## 6. Mobile & Performance Rules

1. **Dynamic Viewport:** Use `min-height: 100dvh` to prevent jumping when mobile browser URL bars expand/collapse.
2. **Overscroll Behavior:** Set `overscroll-behavior-y: none` to prevent rubber-band bounce breaking fixed dark headers.
3. **No Heavy Box Shadows on Mobile:** Use solid surface shifts (`#080808` → `#0E0E0E` → `#141414`) for depth. Reserve tactile glow exclusively for the single active queue item.
4. **Touch Targets:** All primary buttons, action pills, and inputs must maintain a minimum 44px (preferably 48px) touch target.

## 7. Do's and Don'ts

### Do:
- **Do** use `bg-canvas` for layout wrappers and let components stack above it using `bg-matte` or `bg-surface`.
- **Do** use Champagne Gold as a fill background with dark `#080808` text for active queue badges.
- **Do** format all time stamps and queue token numbers with `Space Mono`.

### Don't:
- **Don't** use pure black (`#000000`) or pure white (`#FFFFFF`) anywhere.
- **Don't** use neon, ungrounded red/green alert colors.
- **Don't** use gradient text or decorative glassmorphism blurs.
- **Don't** use Champagne Gold on small body text or secondary icons.
