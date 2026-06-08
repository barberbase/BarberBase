# Purpose
Documents the fixed technology stack, infrastructure topology, and hard substitution constraints. Every implementation decision must fit within these boundaries.
 
# Use This File When
- Evaluating whether a technology choice is permitted
- Designing for memory or concurrency constraints
- Checking mandatory OS/PostgreSQL tuning settings
# Do Not Use This File For
- Environment variables (→ `13_infra_env_deployment.md`)
- API contracts (→ `openapi.yaml`)
- Queue correctness rules (→ `05_queue_locking_transactions.md`)
# Related Files
- `13_infra_env_deployment.md`
- `15_critical_laws.md`
# Source Of Truth Priority
Briefing for architecture decisions. No SQL or OpenAPI override applies here.
 
---
 
## Infrastructure Topology
 
```
[Cloudflare Edge — WAF + Turnstile + CDN]
        │
        ├── Static/SSR ──► [SvelteKit on Cloudflare Pages — barberbase.in]
        │
        └── API calls ───► [DigitalOcean Droplet $8/mo, 1GB RAM, Ubuntu]
                            [api.barberbase.in]
                                │
                          [Caddy — auto-SSL, reverse proxy]
                                │
                    ┌───────────┴───────────┐
              [Go binary]            [PostgreSQL 16]
              port 8080              port 5432
```
 
Single droplet. Single binary. No horizontal scaling in Phase 1.
 
---
 
## Stack Decisions (Hard Constraints)
 
| Component | Decision | MUST NOT substitute with |
|---|---|---|
| Backend | Go, modular monolith, single binary | Microservices, Node, Python |
| Database | PostgreSQL 16 | MongoDB, Supabase, SQLite, NoSQL |
| Cache | None. Redis dropped. | Redis, Memcached |
| SSE fanout | Go in-memory `sync.Map` + channels | Redis Pub/Sub, external broker |
| Rate limiting | `golang.org/x/time/rate` | Redis, external service |
| Frontend | SvelteKit (Svelte 5) | Next.js, React, Vue |
| Router | Chi (`go-chi/chi/v5`) | Gorilla, Gin, Fiber |
| DB driver | `pgx/v5` with `pgxpool` | GORM, sqlx, database/sql alone |
| API codegen | `oapi-codegen` | Hand-written handlers |
| IDs | UUID v7 everywhere | ULID, auto-increment integer |
| Infrastructure | Single $8 DigitalOcean droplet | Kubernetes, multi-server, managed DB |
 
---
 
## Memory Budget
 
Single droplet, 1GB RAM. Hard limits:
 
- `GOMEMLIMIT=250MiB` — Go runtime hard limit
- PostgreSQL: `shared_buffers=128MB`
- 2GB swapfile on OS (emergency only, not for normal operation)
- Docker log rotation: `max-size=10m max-file=3`
Do not design features that require large in-memory caches or unbounded connection pools.
 
---
 
## Mandatory PostgreSQL Configuration
 
```
shared_buffers=128MB
work_mem=8MB
statement_timeout=5s
lock_timeout=1s
idle_in_transaction_session_timeout=10s
max_connections=50
```
 
pgxpool: `MaxConns=20`
 
`lock_timeout=1s` means any `SELECT ... FOR UPDATE` that cannot acquire its lock within 1 second returns an error. Design queue mutations to be fast inside transactions.
 
`work_mem=8MB` is bounded explicitly: a sort/hash per pooled connection ×20 stays within the 1GB budget. Unbounded work_mem risks swap, whose latency trips lock_timeout.
 
---
 
## Mandatory OS Tuning
 
```bash
vm.swappiness=10
ulimit -n 65535
# 2GB swapfile configured at provisioning
GOMEMLIMIT=250MiB   # Also set in Docker Compose
```
 
---
 
## Multi-Tenant Isolation
 
No PostgreSQL Row Level Security (RLS).
 
Application-layer isolation only:
- Go middleware extracts `tenant_id` from JWT into `context.Context`
- Every repository query includes `WHERE tenant_id=$1` from context
- `tenant_id` is NEVER taken from request body
See `15_critical_laws.md` — Law 11.
 
---
 
## Horizontal Scaling Readiness
 
Single node today; vertical-first. These keep a future multi-node deployment PostgreSQL-only (no Redis, no broker):
 
- Sessions/auth are stateless (StaffJWT, HMAC CustomerSession, HMAC PAT); OTPs are in PostgreSQL (`staff_otps`). No sticky sessions required.
- SSE cross-node fanout uses PostgreSQL `LISTEN/NOTIFY` (see `08_sse_realtime.md`).
- Outbox/webhook workers are multi-node-safe via `SKIP LOCKED`. Singleton time-driven jobs guard with `pg_try_advisory_lock` (see `07_webhooks_outbox_workers.md`).
- In-process `x/time/rate` becomes per-node under scale-out — degrades gracefully (each node limits its share), not a functional blocker.
---
 
## Correctness Model
 
- PostgreSQL is the source of truth for all state
- SSE is notification-only. Never required for correctness.
- Clients must recover by re-fetching canonical HTTP state
- If SSE is down, the queue still works — staff refetch on load
- All queue mutations must be correct without SSE
See: `05_queue_locking_transactions.md`, `08_sse_realtime.md`
