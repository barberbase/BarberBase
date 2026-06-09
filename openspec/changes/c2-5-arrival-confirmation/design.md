## Context

BarberBase requires verified arrival tracking for queue entries (C2.5). Physical verification is required to set a customer's presence state to `arrived`. The implementation must support customer PIN/GPS/NFC checks as well as staff override.

## Goals / Non-Goals

**Goals:**
- Implement the `presence.Service` owning all physical verification logic.
- Ensure `arrived` presence is never self-declared and is always physically verified (PIN via bcrypt, GPS via haversine, or staff override).
- Perform bcrypt and haversine calculations outside of database transactions.
- Implement transactional queue mutations that block-lock the parent `queue_sessions` row first (Law 1).
- Emit SSE updates only after transactions successfully commit (Law 8).
- Log all arrival attempts (success/failure) in `arrival_attempts` table.
- Implement customer `on_the_way` and `cancel` transitions, as well as staff-facing manual check-ins.

**Non-Goals:**
- Changing existing schemas or database structures.
- Re-routing logic or call-next changes beyond what is required.
- Evicting old IP limiters from memory in Phase 1 (memory is bounded by active IP counts).

## Decisions

### 1. In-memory Rate Limiting via `rate.Limiter`
- **Choice**: Map of IP strings to `*rate.Limiter` protected by a `sync.Mutex`.
- **Alternative**: Redis rate limiting.
- **Rationale**: Since BarberBase runs within a single instance in the target architecture, a local in-memory store is sufficient, faster, and does not add new infrastructure dependencies. Bounded memory size (active unique IPs in a shift) makes it safe for Phase 1.

### 2. Transaction Lock Ordering
- **Choice**: Always lock `queue_sessions` first (`FOR UPDATE`), then lock the specific `queue_entry` (`FOR UPDATE`).
- **Rationale**: Enforces Law 1 and prevents deadlocks. Every queue mutation in `ConfirmArrival`, `ConfirmOnTheWay`, `CancelMyEntry`, and `StaffConfirmArrival` follows this order.

### 3. Verification Offloading
- **Choice**: Perform bcrypt PIN checks and GPS Haversine calculations before starting the database transaction.
- **Rationale**: Keeps the transaction extremely fast, reducing lock contention on `queue_sessions` and preventing timeout failures under load.

## Risks / Trade-offs

- **[Risk]** Heavy PIN spamming attacks could consume CPU due to bcrypt verification.
  - *Mitigation*: Rate limit checking at step 1 (10/IP/hr) and step 2 (max 5 failed attempts per entry) limits the bcrypt calculations per client.
