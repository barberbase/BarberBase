## Why

Placing the `ApiServer` in package `main` prevents Go packages (such as `internal/api`) from defining handler methods on it due to Go's non-local method receiver rules. Moving the server struct definition into `internal/api` as `api.Server` allows modular implementation of staff OTP login, JWT issuance, and middlewares under `internal/api/handlers_staff.go` to provide secure staff dashboard access.

## What Changes

- **Server Struct Refactor**: Relocate the server struct from `cmd/server/main.go` to `internal/api/server.go` as `api.Server` with injected dependencies (`Pool`, `Bhejna`, `Config`).
- **Initialization**: Update `cmd/server/main.go` to initialize `api.Server` with the database connection pool, Bhejna client, and config, and pass it to `api.Handler`.
- **OTP Request rate-limiting & delivery**: Limit OTP requests to 3 per phone number per 10 minutes (per-phone `sync.Map` rate limiters). Generate a 6-digit cryptographically secure OTP, hash it with bcrypt (cost=10), save it to `staff_otps`, and trigger the WhatsApp delivery via Bhejna client.
- **OTP Verification & Session Issuance**: Implement atomic verification of OTPs against `staff_otps` (max 5 attempts, 5-minute TTL). Upon success, issue HS256-signed Access JWT (15-min TTL) and Refresh JWT (30-day TTL) as `bb_access` and `bb_refresh` HttpOnly cookies.
- **Access Token Refresh**: Authenticate requests with `bb_refresh` cookie to issue new `bb_access` cookies without regenerating the refresh token.
- **Authentication Middleware**: Create `RequireStaffJWT` middleware to validate Access JWTs and inject tenant, location, staff, and role information into the request context.
- **Tenant Middleware**: Create `RejectBodyTenantID` middleware to reject mutating staff requests containing a top-level `tenant_id` field in the request body with a `400` status.

## Capabilities

### New Capabilities
- `staff-auth-session`: Secure staff authentication utilizing WhatsApp OTP delivery, JWT access/refresh token issuance, and tenant context isolation via middlewares.

### Modified Capabilities
<!-- Existing capabilities whose REQUIREMENTS are changing (not just implementation).
     Only list here if spec-level behavior changes. Each needs a delta spec file.
     Use existing spec names from openspec/specs/. Leave empty if no requirement changes.
-->

## Impact

- **Affected code**: `cmd/server/main.go`
- **New files**:
  - `internal/api/server.go`
  - `internal/api/handlers_staff.go`
  - `internal/auth/otp.go`
  - `internal/auth/jwt.go`
  - `internal/auth/middleware.go`
  - `pkg/middleware/tenant.go`
- **Database Tables**: Reads and writes to `staff_members` and `staff_otps`.
- **Dependencies**: Uses standard Go dependencies like `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto`, and `golang.org/x/time/rate`.
