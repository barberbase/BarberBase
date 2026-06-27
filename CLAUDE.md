# BarberBase

Queue management platform for barbershops. Walk-in + appointment queue with real-time SSE updates and WhatsApp notifications via Bhejna.

## Structure

```
barberbase-core/          Go API server (Chi router, pgx, oapi-codegen)
  cmd/server/main.go      Entrypoint — wires all deps
  internal/
    api/                   OpenAPI generated handlers + hand-written admin/staff/public handlers
    auth/                  JWT (staff) + HMAC (customer session) auth
    bhejna/                WhatsApp client (Bhejna API)
    config/                Env-based config (DATABASE_URL, JWT_SECRET, HMAC_SECRET, etc.)
    domain/
      identity/            Customer identity resolution (shadow profiles, E.164)
      presence/            Arrival confirmation (PIN/GPS/NFC verification)
      queue/               Core queue commands + booking resolver
    jobs/                  Background: watchdog, end-of-day, weekly summary
    outbox/                Transactional outbox pattern (at-least-once delivery)
    push/                  Web Push (VAPID)
    realtime/              SSE manager (sync.Map, per-location streams)
    repository/            PostgreSQL repositories (pgx)
    webhook/               Inbound WhatsApp message processing

barberbase-frontend/      SvelteKit 5 + Cloudflare Pages
  src/routes/
    admin/                 Owner onboarding wizard + admin hub (services, staff, shop, whatsapp, analytics)
    dashboard/             Staff queue dashboard (live SSE, walk-in form, checkout)
    login/                 Staff OTP login (WhatsApp)
    q/                     Customer-facing queue status + appointment pages
    [tenant_slug]/         Public shop landing pages
  src/lib/
    stores/queue.svelte    QueueStore — reactive SSE-driven state
    components/            Button, Card, CheckoutModal, StatusIndicator, etc.
    sse.ts                 SSE client connecting to Go backend

openspec/                 Feature specs and archived change proposals
```

## Commands

```bash
# Backend
cd barberbase-core
make gen-api              # Regenerate from OpenAPI spec
make build                # Build binary
make test                 # Run Go tests
docker compose up -d      # PostgreSQL + deps

# Frontend
cd barberbase-frontend
npm run dev               # Vite dev server
npm run build             # Wrangler types + Vite build
npm run check             # Svelte-check + TypeScript
npm run test:e2e          # Playwright
```

## System Laws (enforced in code comments as "Law N")

These are inviolable architectural rules referenced throughout the Go codebase:

- **Law 1**: Lock `queue_sessions FOR UPDATE` before any queue mutation
- **Law 4**: All money in paise (integer), never float
- **Law 7**: Outbox events inside the same transaction as the state change
- **Law 8**: SSE broadcast after commit, never inside transaction
- **Law 10**: Visit charges are immutable snapshots — written once, never updated
- **Law 11**: Tenant isolation via JWT claims, never trust request body for tenant_id
- **Law 12**: Dispatch uses routing-mode-specific query, plain `FOR UPDATE`, never `SKIP LOCKED`
- **Law 18**: Token TTL 4h
- **Law 19**: Customer session tokens are HMAC-signed, stateless
- **Law 20**: 403 (not 401) for scope rejection so caller can distinguish
- **Law 21**: Push notification scoping

## Design System (dark, premium, machined-tool aesthetic)

Tokens defined in `barberbase-frontend/src/routes/layout.css` and `tailwind.config.ts`:

| Token | Value | Usage |
|-------|-------|-------|
| `canvas` | #080808 | Deepest bg (OLED-safe) |
| `matte` | #0E0E0E | Section backgrounds |
| `surface` | #141414 | Elevated cards |
| `titanium` | #1C1C1C | Inputs, popovers |
| `primary` | #E5E2D9 | Body text (Alabaster) |
| `muted` | #9F9B93 | Subtext |
| `dim` | #5A5854 | Placeholders, dividers |
| `gold-accent` | #C8A96B | Queue highlights only (≤5% surface) |

Fonts: Plus Jakarta Sans (display), Inter (body), Space Mono (numbers/labels).

**Never** use pure #000 or #fff. **Never** use gradient text. **Never** use side-stripe borders >1px.

## graphify

A persistent knowledge graph exists at `graphify-out/graph.json` (1,675 nodes, 3,361 edges, 29x token reduction).

**Before answering architecture or "how does X connect to Y" questions**, query the graph:

```
/graphify query "your question here"
/graphify path "NodeA" "NodeB"
/graphify explain "ConceptName"
```

Post-commit hook auto-rebuilds for code-only changes (free). If `graphify-out/.needs_update` exists, run `/graphify . --update`.

## Conventions

- Go: standard lib + pgx + Chi. No ORM. Repository pattern. All queries hand-written SQL.
- Frontend: Svelte 5 runes (`$state`, `$derived`, `$props`). Tailwind v4. No component library.
- API: OpenAPI-first — edit `api/openapi.yaml`, then `make gen-api`. Hand-written handlers wrap generated interface.
- Auth: Staff gets JWT (WhatsApp OTP login). Customers get stateless HMAC session tokens.
- Concurrency: `SELECT FOR UPDATE` on queue_sessions (Law 1). Advisory locks for background jobs.
- Outbox: All external side effects (WhatsApp, push, SSE) go through transactional outbox.
- Deploy: Go on Fly.io / Docker. Frontend on Cloudflare Pages.
