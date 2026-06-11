## Why

To support platform growth, we need a secure, one-shot operator-only bootstrap API endpoint that can provision a new tenant, set up its initial location, generate a secure arrival PIN, and create the owner staff profile in a single atomic transaction.

## What Changes

- Add a POST `/v1/admin/setup` endpoint for platform provisioning.
- Implement a platform admin key middleware (`X-Platform-Admin-Key`) to authenticate provisioning requests using constant-time comparison.
- Add database support for atomic tenant, location, and owner staff insertion with unique constraint handling.
- Generate a secure 6-digit arrival PIN, storing both plaintext and a bcrypt hash.

## Capabilities

### New Capabilities
- `tenant-onboarding`: Provides a secure one-shot platform API to bootstrap new tenants, locations, and owners.

### Modified Capabilities

## Impact

- **API:** Adds `/v1/admin/setup` endpoint to `openapi.yaml`.
- **Database:** Modifies `internal/repository/location.go`.
- **Server/Auth:** Updates `internal/api/handlers_admin.go` and `cmd/server/main.go` with middleware and handler.
- **Config:** Adds `PLATFORM_ADMIN_KEY` configuration.
