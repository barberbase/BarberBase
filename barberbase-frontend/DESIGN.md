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
  dim: "#5A5854"
  gold-accent: "#C8A96B"
  system-success: "#10B981"
  system-warning: "#F59E0B"
  system-error: "#EF4444"
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
- **Tactile Hardware Simulation:** Simulated machined edges, tight transitions, and physical active scale-shrink responses.
- **Anti-Halation Surfaces:** Zero pure black and zero pure white to protect against OLED smearing and visual fatigue.
- **Ergonomic Density:** Focuses on queue states, wait-times, and status checkmarks clearly on a structured grid.

## 2. Colors

The color palette enforces a desaturated, highly legible layout using a warm gray scale and one micro Champagne Gold accent for queue alerts.

### Primary
- **Alabaster Primary** (#E5E2D9): Primary text color, optimized for high contrast and readability on dark canvas surfaces.

### Neutral
- **Canvas** (#080808): The deepest structural background layer. Prevents OLED black-smear.
- **Matte** (#0E0E0E): Mid-level scaffolding, standard section backgrounds.
- **Surface** (#141414): Elevated card panels and list containers.
- **Titanium** (#1C1C1C): Active input fields, popovers, and dropdown menus.
- **Pebble Grey** (#9F9B93): Muted subtext, metadata labels, and inactive state indicators.
- **Dim** (#5A5854): Inactive placeholders and dividers.

### Accents & System States
- **Champagne Gold** (#C8A96B): Reserved exclusively for active queue turn highlights and verified statuses.
- **System Success** (#10B981): Desaturated emerald for active check-ins.
- **System Warning** (#F59E0B): Low-luminance amber for delayed/snoozed entries.
- **System Error** (#EF4444): Soft red for cancellations.

### Named Rules
**The Gold Accent Rule.** Champagne Gold is used strictly for queue verification highlights and the active "Your Turn" state. Its use must not exceed 5% of any screen surface to maintain its visual weight.

## 3. Typography

**Display Font:** "Plus Jakarta Sans", sans-serif
**Body Font:** "Inter", sans-serif
**Label/Mono Font:** "Space Mono", monospace

**Character:** Geometric bold sans-serif headers paired with clean, compact body copy and high-density numeric layouts.

### Hierarchy
- **Display** (Bold (800), 1.75rem, 1.2, tracking-tightest (-0.045em)): Used for main titles and hero headers.
- **Headline** (Semi-Bold (600), 1.25rem, 1.3): Section headers and card headings.
- **Body** (Regular (400), 0.875rem, 1.5): Standard UI copy, long prose, and list content.
- **Label** (Medium (500), 0.75rem, 1.4, tracking-widestUI (0.25em), UPPERCASE): Tabular labels, tabs, and small UI button labels.

### Named Rules
**The Tabular Alignment Rule.** All numbers, queue positions, and time stamps must use the monospace font stack (`Space Mono`) to ensure strict alignment across cards and tables.

## 4. Elevation

The system is flat-by-default, utilizing flat color surfaces of increasing lightness to create depth rather than soft blur shadows. Shadows are strictly functional.

### Shadow Vocabulary
- **Tactile Glow** (`box-shadow: 0 0 12px rgba(200, 169, 107, 0.15)`): Used exclusively on the active queue indicator element.

### Named Rules
**The Tonal Scaffolding Rule.** Depth is created using solid surface shifts: Canvas (#080808) -> Matte (#0E0E0E) -> Surface (#141414) -> Titanium (#1C1C1C).

## 5. Components

### Buttons
- **Shape:** Rounded Pill (999px)
- **Primary:** Warm Alabaster (`bg-primary text-canvas`) with px-6 py-3.5 padding.
- **Tactile Interaction:** Active scale shrink transitions (`active:scale-[0.98]`) executing in 150ms.

### Cards / Containers
- **Corner Style:** Rounded-XL (12px)
- **Border:** 1px border with variable opacity (`border-white/[0.03]` to `border-white/[0.05]`).
- **Machined Edge:** Simulation highlight (`box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03)`).

### Status Indicators
- **Style:** Compact 10px tag, Space Mono font with border.
- **Active State:** Champagne Gold background (`bg-gold-accent text-canvas border-gold-accent`).

## 6. Do's and Don'ts

### Do:
- **Do** use `bg-canvas` for layout wrappers and let components stack above it using `bg-matte` or `bg-surface`.
- **Do** align all active timer badges with the Champagne Gold accent to denote real-time responsiveness.
- **Do** set custom scrollbars in long lists to keep a minimal 4px profile.

### Don't:
- **Don't** use pure black (#000000) or pure white (#ffffff) anywhere in the application.
- **Don't** use gradient text or `background-clip: text` combined with gradients.
- **Don't** add side-stripe accent borders greater than 1px on cards or alert components.
