## Context

In the initial skeleton setup, the `ApiServer` struct was defined in package `main` within `cmd/server/main.go`. In Go, method receivers can only be defined on types belonging to the same package. Thus, defining API handlers in the `internal/api/` package (where `generated.go` resides as `package api`) on `ApiServer` was impossible.

This design refactors the server type to `api.Server` in `internal/api/server.go`, allowing handlers to be implemented as methods on `*Server` inside package `api`. It also details the technical approach to implement Staff OTP authentication, rate limiting, token issuance/refresh, and security middlewares.

## Goals / Non-Goals

**Goals:**
- Relocate the server struct to `package api` as `api.Server` and instantiate it in `cmd/server/main.go`.
- Implement rate limiting (3 requests per 10 mins per phone) using a per-phone `sync.Map` of `rate.Limiter`.
- Implement Staff OTP request logic (6-digit crypto/rand secure code, bcrypt hash, and Bhejna template WhatsApp delivery).
- Implement Staff OTP verification under a single SQL transaction using `SELECT FOR UPDATE` to avoid double-consumption and concurrent access race conditions.
- Issue and set HS256-signed Access JWT (15-min TTL) and Refresh JWT (30-day TTL) cookies with appropriate flags (`HttpOnly`, `Secure`, `SameSite=Strict`).
- Implement JWT context middleware (`RequireStaffJWT`) and body tenant injection validation middleware (`RejectBodyTenantID`).

**Non-Goals:**
- Implementation of staff queue handlers (e.g. CallNext, StartService). Only placeholders/stubs will be created.
- Modifying frontend application code.
- Implementing client-side persistent storage or session management outside standard cookies.

## Decisions

### Decision 1: Relocate server struct to `package api`
- **Rationale**: Defining `Server` inside `internal/api/` allows different handler files (e.g. `handlers_staff.go`) to define methods on `*Server` locally.
- **Alternative Considered**: Keeping the struct in package `main` and implementing handlers in package `main`. Rejected because it creates a monolithic `main.go` or splits `main` across many files, cluttering the command entrypoint and violating the intended layer separation.

### Decision 2: In-Memory Per-Phone Rate Limiter
- **Rationale**: An in-memory `sync.Map[string]*rate.Limiter` is fast, has zero network overhead, and is sufficient since the volume of login requests is low.
- **Alternative Considered**: Persisting rate limits in the database or Redis. Rejected because it adds database overhead or introduces an external dependency that is unnecessary for Phase 1.

### Decision 3: Atomic `SELECT FOR UPDATE` for OTP Verification
- **Rationale**: To prevent concurrent verify requests from replaying the same OTP or bypassing the attempt counter, we lock the `staff_otps` row inside a transaction at the start of verification.
- **Alternative Considered**: Standard `SELECT` followed by `UPDATE`. Rejected because it allows race conditions under concurrent requests where multiple threads read the OTP as unconsumed before any of them update it to consumed.

## Risks / Trade-offs

- **Risk**: Bhejna delivery service is offline.
  - **Mitigation**: Log the delivery failure, but return a success status (HTTP 200) to the client anyway, ensuring the OTP is still valid in case the SMS/WhatsApp message is delayed or delivered through a retry, and preventing information leakage about delivery status.
- **Risk**: Token refresh cookie path restriction.
  - **Mitigation**: The `bb_refresh` cookie path is restricted strictly to `/v1/auth/staff/refresh` to minimize exposure of the refresh token during standard API calls.
