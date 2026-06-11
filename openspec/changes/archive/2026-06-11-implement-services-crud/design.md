## Context

We are implementing the backend CRUD operations for the 3-level service hierarchy (service_categories -> service_groups -> service_variants) for administrators. Currently, there are no endpoints for admins to list, create, or modify these services in `barberbase-core`.

## Goals / Non-Goals

**Goals:**
- Implement the GET, POST, and PATCH services admin API endpoints in `internal/api/handlers_admin.go`.
- Implement repository functions in `internal/repository/service.go`.
- Enforce staff role checks (owner or manager) and location ownership validation.
- Ensure atomicity of the 3-step upsert process in `CreateServiceVariant`.
- Map database errors (such as unique key violations) to appropriate API error statuses (409 Conflict).

**Non-Goals:**
- Implementing other admin sub-units (e.g., C5.2, C5.3, C5.4).
- Modifying `visit_services` table or propagating changes to existing snapshots.

## Decisions

### 1. Database Transactions for Service Creation
- **Option A**: Run separate statements.
- **Option B (Chosen)**: Execute category upsert, group upsert, and variant insert within a single `pgx.Tx` block.
- **Rationale**: If any step fails (e.g., duplicate variant name constraint violation), the whole operation should roll back, keeping the DB state consistent.

### 2. Sentinel Errors for Domain Violations
- **Decision**: Define a domain-specific sentinel error `ErrVariantExists` in the repository package.
- **Rationale**: Avoid leaking raw SQL error details directly to the API handler. The repository scans for PG error code `23505` and returns `ErrVariantExists`, which the handler maps to HTTP 409 Conflict.

### 3. Role and Location Ownership Guards
- **Decision**: Validate the user role (`role` claim from JWT) and query the location's `service_display_mode` while ensuring `tenant_id` matches the JWT `tenant_id` within each of the three handlers.
- **Rationale**: Keeps authorization checks strict and localized as per the C5.1 spec, rather than introducing new global middleware.

## Risks / Trade-offs

- **Risk**: Hardcoding PostgreSQL unique constraint names for identifying unique index violations might break if schema indexes are renamed.
- **Mitigation**: Detect the exact unique constraint violation by checking pgx error code `23505` combined with checking which fields caused the error, or checking for `service_variants_location_id_group_id_name_key`.
