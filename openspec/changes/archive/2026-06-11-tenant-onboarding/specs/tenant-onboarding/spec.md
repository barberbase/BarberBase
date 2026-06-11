## ADDED Requirements

### Requirement: Provision Tenant Setup
The system SHALL support tenant provisioning via a single POST request to `/v1/admin/setup`.
The request body MUST contain `tenant_name`, `tenant_slug`, `owner_name`, `owner_phone`, `location_name`, and `location_slug`.
It MAY contain `address` and `timezone`.
The system SHALL perform the following setup atomically within a single database transaction:
1. Insert a new tenant.
2. Insert a new location referencing the tenant.
3. Insert a new owner staff member referencing the tenant and location.
The system SHALL generate a secure 6-digit arrival PIN, storing its plaintext value in `arrival_pin_plain` and its bcrypt hash in `arrival_pin_hash`.
The system SHALL return the provisioned identifiers and the plaintext PIN.
If any database insert fails or is rolled back, the entire transaction MUST roll back.

#### Scenario: Successful tenant provisioning
- **WHEN** a platform admin sends a POST to `/v1/admin/setup` with a valid `X-Platform-Admin-Key` header and valid request parameters
- **THEN** the system creates the tenant, location, and owner staff member atomically, returning HTTP 201 with the created IDs and plaintext PIN

#### Scenario: Unauthorized provisioning request
- **WHEN** a client sends a POST to `/v1/admin/setup` with an invalid or missing `X-Platform-Admin-Key` header
- **THEN** the system rejects the request immediately, returning HTTP 401 Unauthorized

#### Scenario: Invalid location slug prefix
- **WHEN** a platform admin sends a POST to `/v1/admin/setup` where `location_slug` does not have the prefix `<tenant_slug>/`
- **THEN** the system returns HTTP 422 Unprocessable Entity with error code `INVALID_LOCATION_SLUG`

#### Scenario: Tenant slug conflict
- **WHEN** a platform admin requests setup with a `tenant_slug` that already exists
- **THEN** the transaction rolls back, and the system returns HTTP 409 Conflict with error code `TENANT_SLUG_CONFLICT`

#### Scenario: Location slug conflict
- **WHEN** a platform admin requests setup with a `location_slug` that already exists
- **THEN** the transaction rolls back, and the system returns HTTP 409 Conflict with error code `LOCATION_SLUG_CONFLICT`

#### Scenario: Owner phone conflict
- **WHEN** a platform admin requests setup with an `owner_phone` that is already registered
- **THEN** the transaction rolls back, and the system returns HTTP 409 Conflict with error code `OWNER_PHONE_CONFLICT`

#### Scenario: Owner can request OTP immediately after setup
- **WHEN** a platform admin successfully provisions a new tenant and owner, and then the owner requests an OTP via `POST /auth/staff/request-otp`
- **THEN** the system accepts the request and returns HTTP 200 OK

#### Scenario: PIN plain and PIN hash are consistent
- **WHEN** a platform admin successfully provisions a new tenant and location, and the system stores `arrival_pin_plain` and `arrival_pin_hash`
- **THEN** verifying the plaintext PIN against the bcrypt hash succeeds
