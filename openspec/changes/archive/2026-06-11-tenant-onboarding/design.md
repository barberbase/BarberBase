## Context

Currently, there is no automatic or programmatic way to bootstrap a new tenant on the platform. All tenant creation, location setup, and staff setup must be performed manually or is not exposed via API. 

To support operations, a new operator-facing endpoint is introduced to provision a new tenant, its initial location, and its owner account in a single atomic transaction.

## Goals / Non-Goals

**Goals:**
- Provide a single atomic API endpoint `POST /v1/admin/setup` to provision a tenant, location, and owner.
- Authenticate utilizing the header `X-Platform-Admin-Key` with a constant-time comparison.
- Generate a secure, 6-digit random arrival PIN, storing it as plaintext and as a bcrypt hash.
- Ensure that if any insert fails, the database state rolls back completely.

**Non-Goals:**
- Setting up Mode B (own WABA) configuration during the initial setup (this is handled post-onboarding).
- Support for multiple locations during the bootstrap request.

## Decisions

### 1. Unified Setup Endpoint
Rather than having the operator call multiple endpoints (create tenant, create location, create owner), which can lead to partial failures and inconsistent states, we implement a single, unified `POST /v1/admin/setup` endpoint. 

### 2. Header-based Middleware Authentication
We use a platform admin key (`PLATFORM_ADMIN_KEY` env var) passed via the `X-Platform-Admin-Key` header. This is authenticated by a middleware that performs `crypto/subtle.ConstantTimeCompare` to mitigate timing attacks.

### 3. Crytographically Secure PIN Generation
The PIN is generated using `crypto/rand` to ensure uniform distribution and prevent predictability. It is hashed using `bcrypt` for secure matching during check-in, but is also stored in plaintext on the database as `arrival_pin_plain` so the staff can view it in the dashboard.

## Risks / Trade-offs

- **Risk:** Storing PIN in plaintext (`arrival_pin_plain`) in the DB.
  - **Mitigation:** The DB is internal and secure. The plaintext PIN is necessary because the shop staff needs to see it in their admin dashboard to instruct users who check in physically.
