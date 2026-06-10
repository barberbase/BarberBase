## ADDED Requirements

### Requirement: Get Staff Shop Status
The system MUST provide a composite view of the shop's operational status, queue session status, manual overrides, and arrival pin.

#### Scenario: Staff queries shop status with no active overrides
- **WHEN** staff queries GET `/staff/shop/status`
- **THEN** system returns composite status with `shop_status` as "open" and `manual_override_active` as false.

#### Scenario: Staff queries shop status with active override
- **WHEN** staff queries GET `/staff/shop/status` with an active `location_status_overrides` row (unexpired, not cleared)
- **THEN** system returns the override status, `manual_override_active` true, and `override_expires_at`.

#### Scenario: Expired override is ignored
- **WHEN** staff queries GET `/staff/shop/status` and the latest `location_status_overrides` row has `expires_at` in the past
- **THEN** system ignores it and returns composite status with `shop_status` as "open" and `manual_override_active` as false.

### Requirement: Set Shop Status
The system MUST allow staff to change the shop status and explicitly enforce state constraints on queue sessions. All requests MUST enforce Law 11 tenant isolation based on the JWT claims.

#### Scenario: Status is set to temporarily_closed with expiration
- **WHEN** PATCH `/staff/shop/status` sets `status` to "temporarily_closed" with `expires_in_minutes`=30
- **THEN** `location_status_overrides` receives an entry with `expires_at` equal to `NOW() + 30min`.

#### Scenario: Status is set to temporarily_closed indefinitely
- **WHEN** PATCH `/staff/shop/status` sets `status` to "temporarily_closed" with `expires_in_minutes` omitted
- **THEN** `location_status_overrides` receives an entry with `expires_at` IS NULL.

#### Scenario: Active queue entries block closure without explicit action
- **WHEN** PATCH `/staff/shop/status` sets `status` to "closed", active queue entries exist, and no `modal_action` is provided
- **THEN** system returns a 422 error with `active_entry_count`.

#### Scenario: Closed shop automatically expires remaining entries
- **WHEN** PATCH `/staff/shop/status` sets `status` to "closed" and `modal_action` is "expire_remaining"
- **THEN** system atomically sets queue entries to `expired` state and queue session to `closed`.

#### Scenario: Opening shop clears overrides
- **WHEN** PATCH `/staff/shop/status` sets `status` to "open"
- **THEN** system sets `cleared_at` on all active overrides and does NOT insert a new override row.
