# shadcn-svelte primitives

Fetched from the shadcn-svelte registry, then rewritten onto DESIGN.md tokens
with `scripts/shadcnize.sh` (shadcn's `bg-primary` → `bg-gold-accent`, etc.).
DESIGN.md's @theme in `src/routes/layout.css` is the only token source — there
is no shadcn CSS-variable layer. To add a component:

    npx shadcn-svelte@latest add <name>   # or fetch registry JSON manually
    ./scripts/shadcnize.sh src/lib/components/ui/<name>/*.svelte
