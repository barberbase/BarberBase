## ADDED Requirements

### Requirement: OTP Generation and Delivery
The system SHALL generate a 6-digit OTP code using a cryptographically secure random number generator (`crypto/rand`). It SHALL store the bcrypt hash (cost=10) of the OTP code in the `staff_otps` table with an expiration time of 5 minutes (`NOW() + INTERVAL '5 minutes'`). It SHALL call the Bhejna client to send the OTP using the `bb_staff_otp` template. If Bhejna delivery fails, the system SHALL log the failure and return HTTP 200 without rolling back the database transaction.

#### Scenario: Successful OTP generation and delivery
- **WHEN** a valid and active staff member's phone number is submitted to request an OTP
- **THEN** the system generates a 6-digit secure OTP, inserts its bcrypt hash into the database with a 5-minute TTL, triggers WhatsApp delivery via Bhejna, and returns HTTP 200 with an expiry message.

### Requirement: OTP Request Rate Limiting
The system SHALL enforce a rate limit of a maximum of 3 OTP requests per phone number within any 10-minute window using an in-memory per-phone rate limiter (`sync.Map` of `rate.Limiter`). If the limit is exceeded, the system SHALL immediately reject the request with HTTP 429 without querying or modifying the database.

#### Scenario: Request OTP rate limit exceeded
- **WHEN** a phone number makes a 4th OTP request within a 10-minute window
- **THEN** the system immediately rejects the request with HTTP 429 and does not touch the database.

### Requirement: Request OTP Unknown or Inactive Staff
If the phone number submitted is not registered or the corresponding staff member is inactive (`is_active = false`), the system SHALL immediately return HTTP 401 with a generic error code and message, ensuring that it does not reveal whether the phone number is missing versus inactive.

#### Scenario: Request OTP for unknown or inactive phone number
- **WHEN** an unknown or inactive phone number is submitted to request an OTP
- **THEN** the system queries `staff_members` and returns HTTP 401 with code `UNAUTHORIZED`.

### Requirement: OTP Verification and Consumed State
The system SHALL verify the submitted OTP code against the latest active and unexpired OTP record in `staff_otps` inside a single database transaction using `SELECT FOR UPDATE`. The system SHALL count and increment verification attempts; if attempts reach 5, the OTP SHALL be considered invalid. If verification succeeds, the system SHALL mark the OTP as consumed (`consumed_at = NOW()`), verify that the staff member is still active, and commit the transaction. Replayed or expired OTPs SHALL fail verification.

#### Scenario: Verification fails for already consumed OTP
- **WHEN** a staff member attempts to verify an OTP that has already been marked as consumed
- **THEN** the system rejects the request with HTTP 401.

### Requirement: Session Token Issuance and Cookies
Upon successful OTP verification, the system SHALL issue an HS256-signed Access JWT (15-minute TTL) containing claims for `tenant_id`, `location_id`, `staff_member_id`, and `role`. It SHALL also issue an HS256-signed Refresh JWT (30-day TTL) containing the `staff_member_id` as the subject. The system SHALL return the Access JWT as a cookie `bb_access` (HttpOnly, Secure, SameSite=Strict, Path=/, Max-Age=900) and the Refresh JWT as a cookie `bb_refresh` (HttpOnly, Secure, SameSite=Strict, Path=/v1/auth/staff/refresh, Max-Age=2592000). The response body SHALL contain the staff details.

#### Scenario: Session token issuance on successful verification
- **WHEN** a valid phone number and matching active OTP are submitted for verification
- **THEN** the system marks the OTP as consumed, issues the access and refresh tokens, sets the `bb_access` and `bb_refresh` cookies, and returns HTTP 200 with staff details.

### Requirement: Token Refresh Flow
The system SHALL support refreshing access tokens via the `POST /v1/auth/staff/refresh` endpoint. It SHALL read the `bb_refresh` cookie, parse and verify it using the `JWT_SECRET`, look up the staff member in the database, and verify they are active. If successful, the system SHALL issue a new `bb_access` cookie with a fresh Access JWT and return HTTP 200 without reissuing the `bb_refresh` token.

#### Scenario: Successful token refresh
- **WHEN** a valid, unexpired refresh token cookie `bb_refresh` is submitted to the refresh endpoint and the staff member is active
- **THEN** the system issues a new `bb_access` cookie containing a new Access JWT and returns HTTP 200.

### Requirement: JWT Middleware Verification
The system SHALL protect staff-only routes using middleware that extracts the Bearer token from the `Authorization` header, parses and validates the `StaffClaims` (rejecting any tokens signed with `alg: none` or non-HS256 algorithms), and injects the claims into the request context.

#### Scenario: JWT middleware rejects invalid signing algorithm
- **WHEN** a request is made with a token signed using `alg: none` or an asymmetric algorithm
- **THEN** the middleware rejects the request with HTTP 401.

### Requirement: Tenant Body Rejection
The system SHALL apply middleware to all mutating staff routes (excluding public `/auth/*` endpoints) that peeks at the JSON request body. If the body contains a top-level `"tenant_id"` key, the system SHALL reject the request with HTTP 400 and message `"tenant_id must not be provided in request body"`. The middleware SHALL ensure the body remains readable by subsequent handlers.

#### Scenario: Mutating route rejects request body containing tenant_id
- **WHEN** a mutating staff request is sent with `tenant_id` present in the top-level request body
- **THEN** the middleware immediately returns HTTP 400.
