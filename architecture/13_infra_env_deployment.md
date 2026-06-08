# Purpose
Complete reference for infrastructure topology, all environment variables, Docker/Caddy configuration, Go module dependencies, OS tuning, and backup strategy.
 
# Use This File When
- Setting up the production or development environment
- Checking required environment variables
- Configuring PostgreSQL or Docker
- Adding a new Go dependency
# Do Not Use This File For
- Architecture technology decisions (→ `02_architecture_constraints.md`)
- Queue locking rules (→ `05_queue_locking_transactions.md`)
# Related Files
- `02_architecture_constraints.md` — technology constraint decisions
- `06_bhejna_whatsapp.md` — Bhejna credential setup
# Source Of Truth Priority
Briefing for infrastructure decisions. Env var list is authoritative here.
 
---
 
## Infrastructure Topology
 
```
[Cloudflare Edge — WAF + Turnstile + CDN]
        │
        ├── Static/SSR ──► [SvelteKit on Cloudflare Pages — barberbase.in]
        │
        └── API calls ───► [DigitalOcean Droplet $8/mo, 1GB RAM, Ubuntu 24.04]
                            [api.barberbase.in]
                                │
                          [Caddy — auto-SSL, reverse proxy]
                                │
                    ┌───────────┴───────────┐
              [Go binary]            [PostgreSQL 16]
              port 8080              port 5432
              Docker container       Docker container
```
 
Single droplet. Single binary. No horizontal scaling.
 
---
 
## Environment Variables
 
```bash
# Database
DATABASE_URL              # postgres://bb_user:${DB_PASSWORD}@postgres:5432/barberbase
 
# Security
JWT_SECRET                # 32+ byte random — staff JWT signing
HMAC_SECRET               # 32+ byte random — magic link token + state token signing
AES_ENCRYPTION_KEY        # Exactly 32 bytes — AES-256-GCM for bhejna_api_key_encrypted
 
# Bhejna — Mode A (shared platform number)
BHEJNA_API_URL            # https://bhejna-api.codenxtlab.tech
BHEJNA_API_KEY            # api_key from BarberBase's Bhejna portal account
BHEJNA_WEBHOOK_SECRET     # webhook_secret set in Bhejna portal (HMAC-SHA256 verification)
BHEJNA_FROM_PHONE         # BarberBase platform phone, e.g. +912212345678
 
# Web Push (VAPID) — Staff PWA
VAPID_PUBLIC_KEY          # base64url EC P-256 public key. Also set as PUBLIC_VAPID_PUBLIC_KEY in Cloudflare Pages env for frontend.
VAPID_PRIVATE_KEY         # base64url EC P-256 private key. Never sent to client. Used by webpush-go for signing.
VAPID_SUBJECT             # Contact URI, e.g. mailto:ops@barberbase.in
 
# Cloudflare R2 — pg_dump backups
R2_ACCOUNT_ID
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
R2_BUCKET_NAME
 
# Runtime
ENVIRONMENT               # production | development
GOMEMLIMIT                # 250MiB (also set in Docker Compose)
```
 
**Mode B shops:** Their Bhejna api_keys and webhook_secrets live in `locations.bhejna_api_key_encrypted` and `locations.bhejna_webhook_secret_encrypted`. Decrypted at runtime with `AES_ENCRYPTION_KEY`. No per-shop env vars.
 
---
 
## PostgreSQL Configuration
 
File: `postgresql.conf`
 
```
shared_buffers                     = 128MB
work_mem                           = 8MB
statement_timeout                  = 5s
lock_timeout                       = 1s
idle_in_transaction_session_timeout = 10s
max_connections                    = 50
```
 
pgxpool: `MaxConns=20`
 
`lock_timeout=1s`: Any `SELECT ... FOR UPDATE` that cannot acquire lock within 1 second fails. Callers receive a retriable error. Design queue mutations to be short.
 
`work_mem=8MB`: Bounded explicitly — a sort/hash per pooled connection ×20 stays within the 1GB RAM budget. Unbounded work_mem can force swap, whose latency trips lock_timeout.
 
---
 
## OS Tuning
 
```bash
vm.swappiness=10         # Prefer RAM, use swap only as emergency
ulimit -n 65535          # File descriptor limit for many connections
# Provision a 2GB swapfile at setup time
```
 
---
 
## Docker Configuration
 
```yaml
# docker-compose.yml relevant constraints
services:
  app:
    environment:
      GOMEMLIMIT: 250MiB
    logging:
      driver: json-file
      options:
        max-size: 10m
        max-file: "3"
  postgres:
    volumes:
      - postgres_data:/var/lib/postgresql/data
```
 
---
 
## Caddy Configuration
 
Caddy handles TLS (auto-renewal via Let's Encrypt) and reverse proxy.
 
```
api.barberbase.in {
    reverse_proxy localhost:8080
}
```
 
Cloudflare proxies `barberbase.in` to Cloudflare Pages. `api.barberbase.in` bypasses Cloudflare proxy and points directly to the droplet (or via Cloudflare with orange-cloud to protect the IP).
 
---
 
## Go Module Dependencies
 
```
github.com/go-chi/chi/v5                    # Router
github.com/oapi-codegen/nethttp-middleware  # OpenAPI request validation
github.com/go-playground/validator/v10      # Struct validation
github.com/jackc/pgx/v5                     # PostgreSQL driver
github.com/golang-jwt/jwt/v5                # JWT signing/verification
github.com/google/uuid                      # UUID v7 generation
golang.org/x/time/rate                      # In-process rate limiting
golang.org/x/crypto                         # bcrypt for PIN hashing
github.com/SherClockHolmes/webpush-go       # VAPID signing + Web Push payload encryption (Staff PWA)
```
 
Dev dependency:
```
github.com/oapi-codegen/oapi-codegen/v2     # Generate handlers from openapi.yaml
```
 
**Run `oapi-codegen` before writing any handler.** Output: `internal/api/generated.go`. Never edit generated.go.
 
---
 
## Backup Strategy
 
`pg_dump` every 6 hours → compressed → uploaded to Cloudflare R2 bucket.
 
Implemented in a Go background goroutine or systemd timer. Uses `R2_*` env vars.
 
Retention: 7 days of backups in R2 (implement lifecycle rule in R2 bucket settings).
 
Run `pg_dump` with `statement_timeout=0` (e.g. `PGOPTIONS='-c statement_timeout=0'`, or a
dedicated backup role with the timeout reset). The global `statement_timeout=5s` will abort
`pg_dump`'s `COPY` on larger tables, causing silent backup failure as data grows. Apply the
same exemption to heavy reporting queries (weekly summary, daily hisab).
 
---
 
## Project Directory Structure
 
```
barberbase-core/
├── api/openapi.yaml
├── cmd/server/main.go
├── internal/
│   ├── api/
│   │   ├── generated.go           ← oapi-codegen output. NEVER edit.
│   │   ├── server.go              ← Server struct with dependencies (Pool, Bhejna, Config) defined here so all handler files in this package can add methods on *Server
│   │   ├── handlers_public.go
│   │   ├── handlers_staff.go
│   │   ├── handlers_admin.go
│   │   ├── handlers_webhook.go
│   │   ├── handlers_agent.go
│   │   └── handlers_push.go       ← POST /staff/push/subscribe, /staff/push/call-next
│   ├── domain/
│   │   ├── queue/
│   │   │   ├── commands.go
│   │   │   ├── state_machine.go
│   │   │   └── booking_resolver.go
│   │   ├── identity/
│   │   │   ├── resolver.go
│   │   │   └── merge.go
│   │   ├── presence/arrival.go
│   │   └── errors.go
│   ├── repository/
│   │   ├── queue.go, visit.go, customer.go
│   │   ├── location.go, service.go
│   │   └── webhook.go, outbox.go
│   ├── webhook/
│   │   ├── processor.go           ← SKIP LOCKED worker
│   │   ├── intent_resolver.go
│   │   └── message_classifier.go
│   ├── outbox/
│   │   ├── worker.go
│   │   └── handlers/
│   │       ├── notification.go
│   │       ├── feedback_scheduler.go
│   │       └── push_notification.go  ← web_push.send dispatch handler
│   ├── push/
│   │   └── vapid.go               ← VAPID signing, PAT generation + verification
│   ├── realtime/manager.go        ← sync.Map SSE
│   ├── jobs/
│   │   ├── watchdog.go
│   │   ├── end_of_day.go
│   │   └── weekly_summary.go
│   ├── auth/
│   │   ├── jwt.go, otp.go, middleware.go
│   └── bhejna/
│       └── client.go
├── pkg/
│   ├── apperrors/errors.go
│   └── middleware/tenant.go
├── migrations/
│   └── 001_complete_schema.sql      ← single authoritative schema; no 002 (no deployed DB yet)
├── Dockerfile
├── docker-compose.yml
└── Caddyfile
```
