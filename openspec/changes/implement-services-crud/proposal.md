## Why

BarberBase currently does not have a backend API or repository layer for managing the 3-level service hierarchy (service_categories, service_groups, service_variants) from the owner admin panel. Implementing this CRUD functionality is required to allow owners and managers to customize their catalog of services, prices, and booking rules.

## What Changes

- Add service repository layer to handle CRUD queries on service_categories, service_groups, and service_variants tables.
- Add three handlers under `/admin/locations/{location_id}/services` in handlers_admin.go to support:
  - GET: List all active services in a hierarchical format.
  - POST: Create a new service variant (and upsert its category and group inside a single transaction).
  - PATCH: Update variant properties (name, duration, price, is_active, is_popular) without modifying booking rules.
- Integrate authorization roles (owner, manager) and location ownership validation into the new admin handlers.

## Capabilities

### New Capabilities
- `services-crud`: Complete CRUD backend for the 3-level service hierarchy at `/admin/locations/{location_id}/services` and `/admin/locations/{location_id}/services/{variant_id}`.

### Modified Capabilities

## Impact

- `internal/api/handlers_admin.go` [NEW]
- `internal/repository/service.go` [NEW]
- No impact on `visit_services` table updates.
