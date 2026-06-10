## Why

We need to implement the Shop Status backend (C4.1) which allows staff to get and set the current shop status (open, closing soon, temporarily closed, closed). This is necessary for shop staff to control queueing behavior, enforce overrides, and cleanly end or pause queue sessions according to operational needs.

## What Changes

- Add a new repository file `internal/repository/location.go` with `GetEffectiveShopStatus`, `GetStaffShopStatus`, and `SetShopStatus`.
- Implement the GET `/staff/shop/status` handler in `internal/api/handlers_staff.go` to return composite shop status.
- Implement the PATCH `/staff/shop/status` handler in `internal/api/handlers_staff.go` to update shop status, properly handling queue active entries and session transitions transactionally.

## Capabilities

### New Capabilities
- `shop-status-management`: Manage staff-facing shop status, including temporary overrides, and update queue session status concurrently with entry states.

### Modified Capabilities

## Impact

- `internal/api/handlers_staff.go` is updated to expose the two new endpoints.
- `internal/repository/location.go` is created to handle all the data access and transactional logic.
- Database `location_status_overrides`, `queue_sessions`, and `queue_entries` are queried/updated, ensuring correct queue state machine behavior.
