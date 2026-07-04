#!/usr/bin/env bash
# Map shadcn-svelte semantic classes onto DESIGN.md tokens (layout.css @theme).
# Run on every file fetched from the shadcn-svelte registry before use.
# DESIGN.md stays authoritative: primary=Alabaster text, muted=warm-gray text,
# so shadcn's action/surface vocabulary is rewritten instead of remapped in CSS.
# dark: variants are dropped — the app is permanently dark and the base classes
# already map to dark tokens.
set -euo pipefail
for f in "$@"; do
  sed -i \
    -e 's/\bbg-primary\b/bg-gold-accent/g' \
    -e 's/\btext-primary-foreground\b/text-canvas/g' \
    -e 's/\bbg-secondary\b/bg-surface/g' \
    -e 's/\btext-secondary-foreground\b/text-primary/g' \
    -e 's/\bbg-muted\b/bg-surface/g' \
    -e 's/\btext-muted-foreground\b/text-muted/g' \
    -e 's/\bbg-background\b/bg-canvas/g' \
    -e 's/\btext-foreground\b/text-primary/g' \
    -e 's/\bbg-card\b/bg-matte/g' \
    -e 's/\btext-card-foreground\b/text-primary/g' \
    -e 's/\bbg-popover\b/bg-titanium/g' \
    -e 's/\btext-popover-foreground\b/text-primary/g' \
    -e 's/\bbg-accent\b/bg-titanium/g' \
    -e 's/\btext-accent-foreground\b/text-primary/g' \
    -e 's/\bbg-destructive\b/bg-system-error/g' \
    -e 's/\btext-destructive\b/text-system-error/g' \
    -e 's/\bborder-destructive\b/border-system-error/g' \
    -e 's/\bring-destructive\b/ring-system-error/g' \
    -e 's/\bbg-input\b/bg-titanium/g' \
    -e 's/\bborder-input\b/border-white\/10/g' \
    -e 's/\bborder-border\b/border-white\/[0.06]/g' \
    -e 's/\bring-ring\b/ring-gold-accent/g' \
    -e 's/\bborder-ring\b/border-gold-accent/g' \
    -e 's/\boutline-ring\b/outline-gold-accent/g' \
    -e "s/dark:[^ \"']*//g" \
    -e 's/  */ /g' \
    "$f"
done
